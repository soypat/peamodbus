package modbustcp

import (
	"fmt"
	"net"
	"sync"

	"github.com/soypat/peamodbus"
)

// serverState stores the persisting state of a websocket server connection.
// Since this state is shared between frames it is protected by a mutex so that
// the Client implementation is concurrent-safe.
type serverState struct {
	mu       sync.Mutex
	lastMBAP applicationHeader
	listener *net.TCPListener
	conn     net.Conn
	data     peamodbus.ObjectModel
	closeErr error
	//
	pendingRequests []request
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

// CloseConn closes the connection so that future calls to Err() return argument err.
func (cs *serverState) CloseConn(err error) {
	if err == nil {
		panic("cannot close connection with nil error")
	}
	cs.mu.Lock()
	cs.closeErr = err
	cs.listener.Close()
	cs.mu.Unlock()
}

// IsConnected returns true if there is an active connection to a modbus client. It is shorthand for cs.Err() == nil.
//
// IsConnected is safe to call concurrently.
func (cs *serverState) IsConnected() bool { return cs.Err() == nil }

type request struct {
	// MBAP data includes transaction number and unit.
	transaction uint16
	unit        uint8
	req         peamodbus.Request
}

// NewPendingRequest adds a request to the LIFO queue.
func (cs *serverState) NewPendingRequest(r request) {
	cs.mu.Lock()
	cs.pendingRequests = append(cs.pendingRequests, r)
	cs.mu.Unlock()
}

func (cs *serverState) HandlePendingRequests(tx *peamodbus.Tx) (err error) {
	const mbapsize = 7
	var scratch, txBuf [256 + mbapsize]byte
	cs.mu.Lock()
	i := len(cs.pendingRequests) - 1
	defer func() {
		cs.pendingRequests = cs.pendingRequests[:i] // Remove handled requests from list.
		cs.mu.Unlock()
	}()
	for ; i >= 0; i-- {
		req := cs.pendingRequests[i]
		if req.req.FC == peamodbus.FCWriteSingleCoil {
			_ = req
		}
		plen, err := req.req.Response(tx, cs.data, txBuf[mbapsize:], scratch[:])
		if err != nil {
			break
		}
		mbap := applicationHeader{Transaction: req.transaction, Protocol: 0, Unit: req.unit}
		mbap.Length = uint16(plen + 1)
		_ = mbap.Put(txBuf[:mbapsize])
		_, err = cs.conn.Write(txBuf[:plen+mbapsize])
		if err != nil {
			break
		}
	}
	if i < 0 {
		i = 0 // Captured in defered closure.
	}
	return err
}

func (cs *serverState) callbacks() (peamodbus.RxCallbacks, peamodbus.TxCallbacks) {
	return peamodbus.RxCallbacks{
			OnError: func(rx *peamodbus.Rx, err error) {
				cs.CloseConn(err)
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
			OnError: func(tx *peamodbus.Tx, err error) {
				cs.CloseConn(err)
			},
		}
}

func (cs *serverState) dataModelRequestHandler(rx *peamodbus.Rx, req peamodbus.Request) error {
	fmt.Println("got data model request", req, cs.lastMBAP)
	cs.NewPendingRequest(request{transaction: cs.lastMBAP.Transaction, unit: cs.lastMBAP.Unit, req: req})
	return nil
}

func (cs *serverState) dataRequestHandler(rx *peamodbus.Rx, fc peamodbus.FunctionCode, data []byte) (err error) {
	fmt.Println("unhandled function code", fc)
	return nil
}
