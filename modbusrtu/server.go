package modbusrtu

import (
	"errors"
	"io"

	"github.com/soypat/peamodbus"
)

var ErrBadCRC = errors.New("bad CRC")

type Server struct {
	rx      peamodbus.Rx
	tx      peamodbus.Tx
	state   connState
	address uint8
}

// ServerConfig provides configuration parameters to NewServer.
type ServerConfig struct {
	// Device address in the range 1-247 (inclusive).
	Address uint8

	// DataModel defines the data bank used for data access operations
	// such as read/write operations with coils, discrete inputs, holding registers etc.
	// If nil a default data model will be chosen.
	DataModel peamodbus.DataModel
}

func NewServer(port io.ReadWriter, cfg ServerConfig) *Server {
	if port == nil {
		panic("nil port")
	}
	if cfg.Address < 1 || cfg.Address > 247 {
		panic("invalid address")
	}
	if cfg.DataModel == nil {
		cfg.DataModel = &peamodbus.BlockedModel{}
	}
	sv := &Server{
		address: cfg.Address,
		state: connState{
			closeErr: errors.New("not yet connected"),
			data:     cfg.DataModel,
			port:     port,
		},
	}
	sv.rx.RxCallbacks, sv.tx.TxCallbacks = sv.state.callbacks()
	return sv
}

// HandleNext reads the next message on the network and handles it automatically.
// This call is blocking.
func (sv *Server) HandleNext() (err error) {
	var packet []byte
	var addr uint8
	for {
		packet, addr, err = sv.state.TryRx()
		if err != nil {
			if errors.Is(err, peamodbus.ErrMissingPacketData) {
				continue
			}
			break
		}
		if addr == sv.address {
			break // We got a packet meant for us.
		}
	}
	if err != nil {
		// TODO: Handle error, we should probably empty out the port contents?
		return err
	}

	err = sv.rx.Receive(packet)
	if err != nil {
		return err
	}
	return sv.state.HandlePendingRequests(&sv.tx)
}

func generateCRC(b []byte) (crc uint16) {
	const (
		startCRC = 0xFFFF
		xorCRC   = 0xA001
	)
	crc = startCRC
	for i := 0; i < len(b); i++ {
		crc ^= uint16(b[i])
		for n := 0; n < 8; n++ {
			crc >>= 1
			if crc&1 != 0 {
				crc ^= xorCRC
			}
		}
	}
	return crc
}

func generateLRC(b []byte) (lrc uint8) {
	for i := 0; i < len(b); i++ {
		lrc += b[i]
	}
	return uint8(-int8(lrc)) // This is how you take two's complement in Go.
}
