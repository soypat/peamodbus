package modbusrtu

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/soypat/peamodbus"
)

var (
	errYetToConnect = errors.New("yet to connect")
	errBadCRC       = errors.New("bad CRC")
)

// connState stores the persisting state of a websocket server connection.
// Since this state is shared between frames it is protected by a mutex so that
// the Client implementation is concurrent-safe.
type connState struct {
	mu       sync.Mutex
	data     peamodbus.DataModel
	closeErr error
	port     io.ReadWriter

	// pendingRequest is the request that is currently being processed.
	// While set the server will wait for reply.
	pendingAddr    uint8
	pendingRequest peamodbus.Request

	murx       sync.Mutex
	lastRxTime time.Time
	dataStart  int
	dataEnd    int
	rxbuf      [512]byte
}

func (state *connState) TryRx(isResponse bool) (pdu []byte, address uint8, err error) {
	state.murx.Lock()
	defer state.murx.Unlock()
	if state.dataEnd > 256 {
		// Wrap around before overloading buffer.
		state.resetRxBuf()
	}
	buf := state.rxbuf[:]
	buffered := state.dataEnd - state.dataStart
	if buffered < 2 {
		// If we have read less than 2 bytes, we can't infer the length of the PDU.
		// We read to reach 2 bytes. We do this since some read ops are blocking.
		n, _ := state.port.Read(buf[state.dataEnd : state.dataEnd+2-buffered])
		if n != 0 {
			state.lastRxTime = time.Now()
			state.dataEnd += n
			buffered += n
		}
		if buffered < 2 {
			if err != nil {
				return nil, 0, err
			}
			return nil, 0, peamodbus.ErrMissingPacketData // Still missing data
		}
	}
	var pdulen uint16
	if isResponse {
		_, pdulen, err = peamodbus.InferResponsePacketLength(buf[state.dataStart+1 : state.dataEnd])
	} else {
		_, pdulen, err = peamodbus.InferRequestPacketLength(buf[state.dataStart+1 : state.dataEnd])
	}

	if err != nil && !errors.Is(err, peamodbus.ErrMissingPacketData) {
		if errors.Is(err, peamodbus.ErrBadFunctionCode) {
			state.closeErr = err // Close connection, probably desynced.
		}
		return nil, address, err
	}
	missingData := int(pdulen) + 3 - buffered
	n, err := state.port.Read(buf[state.dataEnd : state.dataEnd+missingData])
	if n != 0 {
		state.lastRxTime = time.Now()
		state.dataEnd += n
		if missingData > n {
			return nil, 0, peamodbus.ErrMissingPacketData // Still missing data
		}
	}
	if err != nil {
		return nil, 0, err
	}

	// Gotten to this point we have a complete Address+PDU+Checksum packet.
	address = buf[state.dataStart]
	packet := buf[state.dataStart : state.dataStart+int(pdulen)+3]
	// Check CRC.
	crc := generateCRC(packet[:len(packet)-2])
	if binary.BigEndian.Uint16(packet[len(packet)-2:]) != crc {
		return nil, address, errBadCRC
	}
	return packet[1 : len(packet)-2], address, nil
}

// Err returns the error responsible for a closed connection. The wrapped chain of errors
// will contain exceptions, io.EOF or a net.ErrClosed error.
//
// Err is safe to call concurrently.
func (cs *connState) Err() error {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	return cs.closeErr
}

func (cs *connState) ResetRxBuf() {
	cs.murx.Lock()
	cs.resetRxBuf()
	cs.murx.Unlock()
}

func (cs *connState) resetRxBuf() {
	if cs.dataStart != cs.dataEnd {
		cs.dataEnd = copy(cs.rxbuf[:], cs.rxbuf[cs.dataStart:cs.dataEnd])
	} else {
		cs.dataEnd = 0
	}
	cs.dataStart = 0
}

// NewPendingRequest adds a request to the LIFO queue.
func (cs *connState) NewPendingRequest(r peamodbus.Request) {
	cs.mu.Lock()
	cs.pendingRequest = r
	cs.mu.Unlock()
}

func (cs *connState) HandlePendingRequests(tx *peamodbus.Tx) (err error) {
	const mbapsize = 1 + 2 // | address(uint8) | data... |  crc(uint16) |
	var scratch, txBuf [256 + mbapsize]byte
	cs.mu.Lock()
	defer cs.mu.Unlock()
	txBuf[0] = cs.pendingAddr
	req := cs.pendingRequest
	plen, err := req.PutResponse(tx, cs.data, txBuf[1:], scratch[:])
	if err != nil {
		return err
	}

	endOfData := 1 + plen
	crc := generateCRC(txBuf[0:endOfData])
	binary.LittleEndian.PutUint16(txBuf[endOfData:], crc)
	_, err = cs.port.Write(txBuf[:endOfData+2])
	return err
}

func (cs *connState) callbacks() (peamodbus.RxCallbacks, peamodbus.TxCallbacks) {
	return peamodbus.RxCallbacks{
			OnError: func(rx *peamodbus.Rx, err error) {
				println(err.Error())
				// cs.CloseConn(err)
			},
			OnException: func(rx *peamodbus.Rx, exceptCode peamodbus.Exception) error {
				return exceptCode
			},
			OnData: cs.dataAccessHandler,
			// See dataHandler method on cs.
			OnDataLong: cs.dataRequestHandler,
			OnUnhandled: func(rx *peamodbus.Rx, fc peamodbus.FunctionCode, buf []byte) error {
				fmt.Printf("got unhandled function code %d\n", fc)
				return nil
			},
		}, peamodbus.TxCallbacks{
			OnError: nil,
		}
}

func (cs *connState) dataAccessHandler(rx *peamodbus.Rx, req peamodbus.Request) error {
	if cs.data == nil {
		println("got invalid data model request", req.String())
		return nil // ignore
	}
	println("got data model request", req.String())
	cs.NewPendingRequest(req)
	return nil
}

func (cs *connState) dataRequestHandler(rx *peamodbus.Rx, fc peamodbus.FunctionCode, data []byte) (err error) {
	println("unhandled function code", fc.String())
	return nil
}
