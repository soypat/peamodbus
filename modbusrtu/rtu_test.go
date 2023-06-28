package modbusrtu

import (
	"io"
	"testing"
	"time"

	"github.com/soypat/peamodbus"
)

func TestIntegration(t *testing.T) {
	const (
		devAddr   = 1
		startAddr = 1
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
	cli := NewClient(r1w2, 100e9*time.Millisecond)
	srv := NewServer(r2w1, ServerConfig{Address: devAddr, DataModel: data})
	endGoroutine := make(chan struct{})
	go func() {
		for {
			err := srv.HandleNext()
			if err != nil {
				t.Fatal("fatal", err)
			}
			select {
			case <-endGoroutine:
				return
			default:
			}
		}
	}()

	var buf [125]uint16
	err := cli.ReadHoldingRegisters(devAddr, startAddr, buf[:nRegs])
	if err != nil {
		t.Fatal(err)
	}
	for i := startAddr; i < startAddr+nRegs; i++ {
		if buf[i] != data.GetHoldingRegister(i) {
			t.Fatalf("expected %v, got %v at %v", data.GetHoldingRegister(i), buf[i], i)
		}
	}
	endGoroutine <- struct{}{}
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
