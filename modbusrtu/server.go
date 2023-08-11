package modbusrtu

import (
	"encoding/binary"
	"errors"
	"io"

	"github.com/soypat/peamodbus"
)

var ErrBadCRC = errors.New("bad CRC")

type Server struct {
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
			closeErr:    errors.New("not yet connected"),
			data:        cfg.DataModel,
			port:        port,
			pendingAddr: cfg.Address,
		},
	}
	return sv
}

// HandleNext reads the next message on the network and handles it automatically.
// This call is blocking.
func (sv *Server) HandleNext() (err error) {
	var pdu []byte
	var addr uint8
	for {
		pdu, addr, err = sv.state.TryRx(false)
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
		if exc, ok := err.(peamodbus.Exception); ok && len(pdu) > 2 {
			return sv.handleException(exc, peamodbus.FunctionCode(pdu[0]))
		}
		// TODO: Handle error, we should probably empty out the port contents?
		return err
	}
	req, dataoffset, err := peamodbus.DecodeRequest(pdu)
	if err != nil {
		return err
	}
	sendbuf := sv.state.txbuf[:]
	sendbuf[0] = addr
	plen, err := req.PutResponse(sv.state.data, sendbuf[1:], pdu[dataoffset:])
	if err != nil {
		if exc, ok := err.(peamodbus.Exception); ok {
			return sv.handleException(exc, req.FC)
		}
		return err
	}
	endOfData := 1 + plen
	crc := generateCRC(sendbuf[0:endOfData])
	binary.LittleEndian.PutUint16(sendbuf[endOfData:], crc)
	_, err = sv.state.port.Write(sendbuf[:endOfData+2])
	return err
}

func (sv *Server) handleException(exc peamodbus.Exception, fc peamodbus.FunctionCode) error {
	exc.PutResponse(sv.state.rxbuf[:2], fc)
	_, err := sv.state.port.Write(sv.state.rxbuf[:2])
	return err
}
