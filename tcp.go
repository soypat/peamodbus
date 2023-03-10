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
// Server's methods are not all safe for concurrent use.
type Server struct {
	state      serverState
	tcpTimeout time.Duration
	rx         Rx
	tx         Tx
	address    net.TCPAddr
}

// ServerConfig provides configuration parameters to NewServer.
type ServerConfig struct {
	// Formatted numeric IP with port. i.e: "192.168.1.35:502"
	Address string
	// It is recommended to enable KeepAlive on both client and server connections
	// in order to poll whether either has crashed. By default is set to 2 hours
	// as specified by the Modbus TCP/IP Implementation Guidelines.
	// KeepAlive time.Duration

	// ConnectTimeout is the maximum amount of time a call to Accept will wait for a connect to complete.
	ConnectTimeout time.Duration

	// DataModel defines the data bank used for data access operations
	// such as read/write operations with coils, discrete inputs, holding registers etc.
	// If nil a default data model will be chosen.
	DataModel DataModel
}

// NewServer returns a Server ready for use.
// `localhost` in a server address is replaced with `127.0.0.1`
func NewServer(cfg ServerConfig) (*Server, error) {
	if cfg.DataModel == nil {
		cfg.DataModel = &BlockedModel{}
	}
	cfg.Address = strings.Replace(cfg.Address, "localhost", "127.0.0.1", 1)
	address, err := netip.ParseAddrPort(cfg.Address)
	if err != nil {
		return nil, err
	}

	sv := &Server{
		state:      serverState{closeErr: net.ErrClosed, data: cfg.DataModel},
		tcpTimeout: cfg.ConnectTimeout,
		// keepalive:  cfg.KeepAlive,
		address: *net.TCPAddrFromAddrPort(address),
	}
	sv.rx.RxCallbacks, sv.tx.TxCallbacks = sv.state.callbacks()
	return sv, nil
}

// Accept begins listening on the server's TCP address. If the server already
// has a connection this method returns an error. By design Servers can only maintain one connection.
// File an issue if this does not cover your use case.
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

// HandleNext reads the next message on the network and handles it automatically.
// This call is blocking.
func (sv *Server) HandleNext() error {
	_, err := sv.rx.ReadNext()
	if err != nil {
		return err
	}
	return sv.state.HandlePendingRequests(&sv.tx)
}

// Err returns the error that caused disconnection. Is safe for concurrent use.
func (sv *Server) Err() error {
	return sv.state.Err()
}

// IsConnected returns true if the server has an active connection. Is safe for concurrent use.
func (sv *Server) IsConnected() bool {
	return sv.state.IsConnected()
}

// Addr returns the address of the last active connection. If the server has not
// yet initialized a connection it returns an empty *net.TCPAddr.
func (sv *Server) Addr() net.Addr {
	sv.state.mu.Lock()
	defer sv.state.mu.Unlock()
	if sv.state.listener == nil {
		return &net.TCPAddr{}
	}
	return sv.state.listener.Addr()
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
	var buffer [256]byte
	cs.mu.Lock()
	i := len(cs.pendingRequests) - 1
	defer func() {
		cs.pendingRequests = cs.pendingRequests[:i] // Remove handled requests from list.
		cs.mu.Unlock()
	}()
	for ; i >= 0; i-- {
		req := cs.pendingRequests[i]
		mbap := ApplicationHeader{Transaction: req.transaction, Protocol: 0, Unit: req.unit}
		switch req.fc {

		case FCReadHoldingRegisters, FCReadCoils, FCReadDiscreteInputs, FCReadInputRegisters:
			// These are client requests. We respond state of coils or registers.
			shortSize := req.fc == FCReadHoldingRegisters || req.fc == FCReadInputRegisters
			quantityBytes := req.reqVal2
			if quantityBytes > uint16(len(buffer)) {
				err = fmt.Errorf("short buffer in HandlePendingRequests")
				break
			}
			address := req.reqVal1
			if shortSize {
				quantityBytes *= 2
			}

			err = cs.data.Read(buffer[:quantityBytes], req.fc, address, req.reqVal2)
			if err != nil {
				err = fmt.Errorf("handling fc=%#X accessing data model: %w", req.fc, err)
				break
			}
			if req.fc == FCReadHoldingRegisters {
				_, err = tx.ResponseReadHoldingRegisters(mbap, buffer[:quantityBytes])
			} else if req.fc == FCReadCoils {
				_, err = tx.ResponseReadCoils(mbap, buffer[:quantityBytes])
			} else if req.fc == FCReadInputRegisters {
				_, err = tx.ResponseReadInputRegisters(mbap, buffer[:quantityBytes])
			} else if req.fc == FCReadDiscreteInputs {
				_, err = tx.ResponseReadDiscreteInput(mbap, buffer[:quantityBytes])
			} else {
				panic("unhandled case") // unreachable.
			}

		case FCWriteSingleRegister, FCWriteSingleCoil:
			address := req.reqVal1
			value := req.reqVal2
			binary.BigEndian.PutUint16(buffer[:2], value)
			err = cs.data.Write(req.fc, address, 1, buffer[:2])
			if err != nil {
				err = fmt.Errorf("handling fc=%#X accessing data model: %w", req.fc, err)
				break
			}
			if req.fc == FCWriteSingleCoil {
				_, err = tx.ResponseWriteSingleCoil(mbap, address, value == 0xff00)
			} else if req.fc == FCWriteSingleRegister {
				_, err = tx.ResponseWriteSingleRegister(mbap, address, value)
			} else {
				panic("unhandled case") // unreachable.
			}

		default:
			err = fmt.Errorf("unhandled request function code %#X (%d)", req.fc, req.fc)
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
		FCWriteSingleCoil, FCWriteSingleRegister:
		n, err = io.ReadAtLeast(r, buf[:], 4)
		if err != nil {
			break
		} else if n != 4 {
			err = fmt.Errorf("expected 4 bytes data, got %d", n)
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

	case FCWriteMultipleCoils, FCWriteMultipleRegisters:
		// We must handle multiple writes in request handler to avoid allocating memory.
		_, err = io.ReadFull(r, buf[:5])
		if err != nil {
			break
		}
		startAddress := binary.BigEndian.Uint16(buf[:2])
		quantity := binary.BigEndian.Uint16(buf[2:4])
		byteCount := buf[4]
		_, err = io.ReadAtLeast(r, buf[:], int(byteCount))
		if err != nil {
			break
		}
		cs.data.Write(fc, startAddress, quantity, buf[:byteCount])
	default:
		fmt.Println("unhandled function code", fc)
	}
	return err
}
