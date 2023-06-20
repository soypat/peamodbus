package modbusrtu

import (
	"encoding/binary"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/soypat/peamodbus"
)

// serverState stores the persisting state of a websocket server connection.
// Since this state is shared between frames it is protected by a mutex so that
// the Client implementation is concurrent-safe.
type serverState struct {
	mu         sync.Mutex
	data       peamodbus.DataModel
	closeErr   error
	port       io.ReadWriter
	targetAddr uint8
	// pendingRequest is the request that is currently being processed.
	// While set the server will wait for reply.

	pendingRequest peamodbus.Request

	murx       sync.Mutex
	lastRxTime time.Time
	dataStart  int
	dataEnd    int
	rxbuf      [512]byte
}

// Err returns the error responsible for a closed connection. The wrapped chain of errors
// will contain exceptions, io.EOF or a net.ErrClosed error.
//
// Err is safe to call concurrently.
func (cs *serverState) Err() error {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	return cs.closeErr
}
func (cs *serverState) ResetRxBuf() {
	cs.murx.Lock()
	cs.resetRxBuf()
	cs.murx.Unlock()
}
func (cs *serverState) resetRxBuf() {
	cs.dataStart = 0
	cs.dataEnd = 0
}

// IsConnected returns true if there is an active connection to a modbus client. It is shorthand for cs.Err() == nil.
//
// IsConnected is safe to call concurrently.
func (cs *serverState) IsConnected() bool { return cs.Err() == nil }

// NewPendingRequest adds a request to the LIFO queue.
func (cs *serverState) NewPendingRequest(r peamodbus.Request) {
	cs.mu.Lock()
	cs.pendingRequest = r
	cs.mu.Unlock()
}

func (cs *serverState) HandlePendingRequests(tx *peamodbus.Tx) (err error) {
	const mbapsize = 1 + 2 // | address(uint8) | data... |  crc(uint16) |
	var scratch, txBuf [256 + mbapsize]byte
	cs.mu.Lock()
	defer cs.mu.Unlock()
	txBuf[0] = cs.targetAddr
	req := cs.pendingRequest
	plen, err := req.Response(tx, cs.data, txBuf[1:], scratch[:])
	if err != nil {
		return err
	}

	endOfData := 1 + plen
	crc := generateCRC(txBuf[0:endOfData])
	binary.LittleEndian.PutUint16(txBuf[endOfData:], crc)
	_, err = cs.port.Write(txBuf[:endOfData+2])
	return err
}

func (cs *serverState) callbacks() (peamodbus.RxCallbacks, peamodbus.TxCallbacks) {
	return peamodbus.RxCallbacks{
			OnError: func(rx *peamodbus.Rx, err error) {
				println(err.Error())
				// cs.CloseConn(err)
			},
			OnException: func(rx *peamodbus.Rx, exceptCode uint8) error {
				return fmt.Errorf("got exception with code %d", exceptCode)
			},
			OnDataModelRequest: cs.dataModelRequestHandler,
			// See dataHandler method on cs.
			OnDataAccess: cs.dataRequestHandler,
			OnUnhandled: func(rx *peamodbus.Rx, fc peamodbus.FunctionCode, buf []byte) error {
				fmt.Printf("got unhandled function code %d\n", fc)
				return nil
			},
		}, peamodbus.TxCallbacks{
			OnError: nil,
		}
}

func (cs *serverState) dataModelRequestHandler(rx *peamodbus.Rx, req peamodbus.Request) error {
	println("got data model request", req.String())
	cs.NewPendingRequest(req)
	return nil
}

func (cs *serverState) dataRequestHandler(rx *peamodbus.Rx, fc peamodbus.FunctionCode, data []byte) (err error) {
	println("unhandled function code", fc.String())
	return nil
}
