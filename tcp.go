package peamodbus

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"strings"
	"sync"
	"time"
)

const (
	defaultKeepalive = 2 * time.Hour
	defaultPort      = ":502"
)

// Server is a Modbus TCP Server implementation. A Server listens on a network
// and awaits a client's request. Servers are typically sensors or actuators in an industrial setting.
type Server struct {
	state serverState

	keepalive  time.Duration
	tcpTimeout time.Duration
	rx         Rx
	tx         Tx
	address    net.TCPAddr
}

type ServerConfig struct {
	// Formatted as IP with port. i.e: "192.168.1.35:502"
	// By default client uses port 502.
	Address string
	// It is recommended to enable KeepAlive on both client and server connections
	// in order to poll whether either has crashed. By default is set to 2 hours
	// as specified by the Modbus TCP/IP Implementation Guidelines.
	// KeepAlive time.Duration

	// Timeout is the maximum amount of time a dial will wait for a connect to complete. If Deadline is also set, it may fail earlier.
	NetTimeout time.Duration
	// DataModel defines the data bank used for data access operations
	// such as read/write operations with coils, discrete inputs, holding registers etc.
	// If nil a default data model will be chosen.
	DataModel DataModel
}

func NewServer(cfg ServerConfig) (*Server, error) {
	// if cfg.KeepAlive == 0 {
	// 	cfg.KeepAlive = defaultKeepalive
	// }
	if cfg.DataModel == nil {
		cfg.DataModel = &StaticModel{}
	}
	cfg.Address = strings.Replace(cfg.Address, "localhost", "127.0.0.1", 1)
	address, err := netip.ParseAddrPort(cfg.Address)
	if err != nil {
		return nil, err
	}

	sv := &Server{
		state:      serverState{closeErr: net.ErrClosed, data: cfg.DataModel},
		tcpTimeout: cfg.NetTimeout,
		// keepalive:  cfg.KeepAlive,
		address: *net.TCPAddrFromAddrPort(address),
	}
	sv.rx.RxCallbacks, sv.tx.TxCallbacks = sv.state.callbacks()
	return sv, nil
}

func (sv *Server) Accept(ctx context.Context) error {
	if sv.state.IsConnected() {
		return errors.New("already connected or incorrectly initialized client/server")
	}
	listener, err := net.ListenTCP("tcp", &sv.address)
	if err != nil {
		return err
	}
	if sv.tcpTimeout > 0 {
		listener.SetDeadline(time.Now().Add(sv.tcpTimeout))
	}
	conn, err := listener.AcceptTCP()
	if err != nil {
		listener.Close()
		return err
	}
	sv.state.mu.Lock()
	listener.SetDeadline(time.Time{})
	sv.state.closeErr = nil
	sv.state.listener = listener
	sv.state.mu.Unlock()
	sv.rx.SetRxTransport(conn)
	sv.tx.SetTxTransport(conn)
	return nil
}

func (sv *Server) HandleNext() error {
	_, err := sv.rx.ReadNext()
	if err != nil {
		return err
	}
	return sv.state.HandlePendingRequests(&sv.tx)
}

func (sv *Server) Err() error {
	return sv.state.Err()
}

func (sv *Server) IsConnected() bool {
	return sv.state.IsConnected()
}

// serverState stores the persisting state of a websocket server connection.
// Since this state is shared between frames it is protected by a mutex so that
// the Client implementation is concurrent-safe.
type serverState struct {
	mu       sync.Mutex
	listener *net.TCPListener
	data     DataModel
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
	fc          FunctionCode
	// First value usually contains Modbus address value.
	reqVal1 uint16
	// Second value usually contains Modbus quantity of addresses to read.
	reqVal2 uint16
}

// NewPendingRequest adds a request to the LIFO queue.
func (cs *serverState) NewPendingRequest(r request) {
	cs.mu.Lock()
	cs.pendingRequests = append(cs.pendingRequests, r)
	cs.mu.Unlock()
}

func (cs *serverState) HandlePendingRequests(tx *Tx) (err error) {
	cs.mu.Lock()
	i := len(cs.pendingRequests) - 1
	defer func() {
		cs.pendingRequests = cs.pendingRequests[:i]
		cs.mu.Unlock()
	}()
	for ; i >= 0; i-- {
		req := cs.pendingRequests[i]
		switch req.fc {
		case FCReadHoldingRegisters:
			mbap := ApplicationHeader{Transaction: req.transaction, Protocol: 0, Unit: req.unit}
			quantityBytes := req.reqVal2 * 2
			err = cs.data.Read(tx.buf[:quantityBytes], req.fc, req.reqVal1)
			if err != nil {
				err = fmt.Errorf("handling fc=0x%#X accessing data model: %w", req.fc, err)
				break
			}
			// tx.TxCallbacks.OnDataAccess(tx, req.fc, req.reqVal1, tx.buf[:quantityBytes])
			_, err = tx.ResponseReadHoldingRegisters(mbap, tx.buf[:quantityBytes])
		default:
			err = fmt.Errorf("unhandled request function code 0x%#X (%d)", req.fc, req.fc)
		}
	}
	if i < 0 {
		i = 0
	}
	return err
}

func (cs *serverState) callbacks() (RxCallbacks, TxCallbacks) {
	return RxCallbacks{
			OnError: func(rx *Rx, err error) {
				cs.CloseConn(err)
				rx.trp.Close()
			},
			OnException: func(rx *Rx, exceptCode uint8) error {
				return fmt.Errorf("got exception with code %d", exceptCode)
			},
			// See dataHandler method on cs.
			OnDataAccess: cs.dataRequestHandler,
			OnUnhandled: func(rx *Rx, fc FunctionCode, r io.Reader) error {
				fmt.Printf("got unhandled function code %d\n", fc)
				io.ReadAll(r)
				return nil
			},
		}, TxCallbacks{
			OnError: func(tx *Tx, err error) {
				cs.CloseConn(err)
				tx.trp.Close()
			},
		}
}

func (cs *serverState) dataRequestHandler(rx *Rx, fc FunctionCode, r io.Reader) (err error) {
	var buf [256]byte
	var n int
	switch fc {

	// Simplest Modbus request case consisting of a address and a quantity
	case FCReadHoldingRegisters, FCReadInputRegisters, FCReadCoils, FCReadDiscreteInputs,
		FCWriteSingleCoil, FCWriteSingleRegister, FCWriteMultipleRegisters:
		n, err = io.ReadAtLeast(r, buf[:], 4)
		if err != nil {
			break
		} else if n != 4 {
			err = fmt.Errorf("expected 4 bytes of ReadHolding data, got %d", len(buf))
			break
		}
		startAddress := binary.BigEndian.Uint16(buf[:2])
		quantity := binary.BigEndian.Uint16(buf[2:4])
		cs.NewPendingRequest(request{
			transaction: rx.LastRecievedMBAP.Transaction,
			unit:        rx.LastRecievedMBAP.Unit,
			fc:          fc,
			reqVal1:     startAddress,
			reqVal2:     quantity,
		})

	default:
		fmt.Println("unhandled function code", fc)
	}
	return err
}
