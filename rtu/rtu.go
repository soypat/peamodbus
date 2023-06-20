package rtu

import (
	"errors"
	"io"
	"time"

	"github.com/soypat/peamodbus"
)

type Server struct {
	rx      peamodbus.Rx
	tx      peamodbus.Tx
	state   serverState
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
		state: serverState{
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
		packet, addr, err = sv.tryRx()
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

func (sv *Server) tryRx() (packet []byte, address uint8, err error) {
	sv.state.murx.Lock()
	defer sv.state.murx.Unlock()

	buf := sv.state.rxbuf[:]
	n, err := sv.state.port.Read(buf[sv.state.dataEnd:])
	if n != 0 {
		sv.state.lastRxTime = time.Now()
	}
	sv.state.dataEnd += n
	if err != nil {
		return nil, 0, err
	} else if n == 0 && sv.state.dataEnd == sv.state.dataStart {
		return nil, 0, peamodbus.ErrMissingPacketData // No data read.
	}
	var plen uint16
	// Remove RTU/Serial address byte.
	address = buf[sv.state.dataStart]
	possiblePacket := buf[sv.state.dataStart+1 : sv.state.dataEnd]
	_, plen, err = peamodbus.InferRequestPacketLength(possiblePacket)
	if err != nil {
		return nil, address, err
	}
	if len(possiblePacket) < int(plen) {
		return nil, address, peamodbus.ErrMissingPacketData
	}
	packetStart := sv.state.dataStart
	packetEnd := sv.state.dataStart + int(plen)
	if packetEnd > 256 {
		// Wrap around before overloading buffer. Next packet read will overwrite.
		sv.state.resetRxBuf()
	}
	sv.state.dataStart = packetEnd
	return buf[packetStart:packetEnd], address, nil
}
