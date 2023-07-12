package modbusrtu

import (
	"fmt"
	"io"
	"testing"
	"time"

	"github.com/soypat/peamodbus"
)

func TestCRC(t *testing.T) {
	testCases := []struct {
		msgWithAddr []byte
		expected    uint16
	}{
		{
			msgWithAddr: []byte{0x56}, // Read 2 registers starting at 0x0000 from device 0x56.
			expected:    0x7e3f,
		},
		{
			msgWithAddr: []byte{0x56, 0x03, 0x00, 0x00, 0x00, 0x02}, // Read 2 registers starting at 0x0000 from device 0x56.
			expected:    0xecc9,
		},
	}
	for _, tC := range testCases {
		t.Run(fmt.Sprintf("len=%d", len(tC.msgWithAddr)), func(t *testing.T) {
			got := generateCRC(tC.msgWithAddr[:])
			if got != tC.expected {
				t.Fatalf("expected %x, got %x", tC.expected, got)
			}
		})
	}
}
func TestIntegration(t *testing.T) {
	const (
		numTests  = 100
		devAddr   = 1
		startAddr = 3
		nRegs     = 1
	)
	data := peamodbus.ConcurrencySafeDataModel(&peamodbus.BlockedModel{})
	for i := startAddr; i < startAddr+nRegs; i++ {
		u16 := uint16(1) // generateCRC([]byte{byte(i), byte(i >> 8)})
		data.SetHoldingRegister(i, u16)
	}

	r1, w1 := io.Pipe()
	r2, w2 := io.Pipe()
	r1w2 := rw{label: "Client Pipe", Reader: r1, Writer: w2, t: t}
	r2w1 := rw{label: "Server Pipe", Reader: r2, Writer: w1, t: t}
	cli := NewClient(ClientConfig{RxTimeout: 200 * time.Millisecond})
	cli.SetTransport(r1w2)
	srv := NewServer(r2w1, ServerConfig{Address: devAddr, DataModel: data})

	var buf [125]uint16
	for test := 0; test < numTests; test++ {
		go srv.HandleNext()
		err := cli.ReadHoldingRegisters(devAddr, startAddr, buf[:nRegs])
		if err != nil {
			t.Fatal(err)
		}
		for i := startAddr; i < startAddr+nRegs; i++ {
			read := buf[i-startAddr]
			if got, exc := data.GetHoldingRegister(i); got != read {
				t.Fatalf("expected %v, got %v at %v (exception=%q)", got, read, i, exc.Error())
			}
		}
		t.Logf("========== TEST %d PASS ==========", test)
	}

}

type rw struct {
	label string
	io.Reader
	io.Writer
	t testing.TB
}

func (r rw) Read(p []byte) (n int, err error) {
	r.t.Helper()
	if r.t != nil {
		r.t.Logf("%v: attempt read %v bytes", r.label, len(p))
	}
	n, err = r.Reader.Read(p)
	if r.t != nil {
		if err != nil {
			r.t.Logf("%v: read %v bytes ERR=%q (%q)", r.label, n, err, p[:n])
		} else {
			r.t.Logf("%v: read %v bytes (%q)", r.label, n, p[:n])
		}
	}
	return n, err
}

func (r rw) Write(p []byte) (n int, err error) {
	r.t.Helper()
	if r.t != nil {
		r.t.Logf("%v: attempt write %v bytes (%q)", r.label, len(p), p)
	}
	n, err = r.Writer.Write(p)
	if r.t != nil {
		if err != nil {
			r.t.Logf("%v: wrote %v bytes ERR=%q", r.label, n, err)
		} else {
			r.t.Logf("%v: wrote %v bytes", r.label, n)
		}
	}
	return n, err
}
