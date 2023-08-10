package modbustcp

import (
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
	conn     *net.TCPConn
	data     peamodbus.DataModel
	closeErr error
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
