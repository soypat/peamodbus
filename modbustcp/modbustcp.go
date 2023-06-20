package modbustcp

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"strings"
	"time"

	"github.com/soypat/peamodbus"
)

const (
	defaultKeepalive = 2*time.Hour - time.Minute
	defaultPort      = ":502"
)

// Server is a Modbus TCP Server implementation. A Server listens on a network
// and awaits a client's request. Servers are typically sensors or actuators in an industrial setting.
// Server's methods are not all safe for concurrent use.
type Server struct {
	state      serverState
	tcpTimeout time.Duration
	rx         peamodbus.Rx
	tx         peamodbus.Tx
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
	DataModel peamodbus.DataModel
}

// NewServer returns a Server ready for use.
// `localhost` in a server address is replaced with `127.0.0.1`
func NewServer(cfg ServerConfig) (*Server, error) {
	if cfg.DataModel == nil {
		cfg.DataModel = &peamodbus.BlockedModel{}
	}
	cfg.Address = strings.Replace(cfg.Address, "localhost", "127.0.0.1", 1)
	address, err := netip.ParseAddrPort(cfg.Address)
	if err != nil {
		return nil, err
	}

	sv := &Server{
		state:      serverState{closeErr: errors.New("not yet connected"), data: cfg.DataModel},
		tcpTimeout: cfg.ConnectTimeout,
		// keepalive:  cfg.KeepAlive,
		address: *net.TCPAddrFromAddrPort(address),
	}
	sv.rx.RxCallbacks, sv.tx.TxCallbacks = sv.state.callbacks()
	return sv, nil
}

// DataModel returns the active handle the the modbus data model. Changes to the
// data model are reflected in the server's behavior. There is no concurrent protection
// for the data model.
func (sv *Server) DataModel() peamodbus.DataModel {
	return sv.state.data
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
	sv.state.conn = conn
	sv.state.mu.Unlock()
	return nil
}

// HandleNext reads the next message on the network and handles it automatically.
// This call is blocking.
func (sv *Server) HandleNext() error {
	if err := sv.Err(); err != nil {
		return err
	}
	mbap, n, err := decodeMBAP(sv.state.conn)
	if err != nil {
		if n != 0 {
			sv.state.CloseConn(err)
		}
		return err
	}
	sv.state.mu.Lock()
	sv.state.lastMBAP = mbap
	sv.state.mu.Unlock()
	var buf [256]byte
	remaining := mbap.Length - 1
	_, err = io.ReadFull(sv.state.conn, buf[:remaining])
	if err != nil {
		return err
	}
	err = sv.rx.Receive(buf[:remaining])
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

// applicationHeader is a compact representation of the MBAP header as described
// by Modbus TCP Implementation Guide. This header precedes all Modbus TCP packets.
type applicationHeader struct {
	Transaction uint16
	Protocol    uint16
	Length      uint16
	Unit        uint8
}

// decodeMBAP reads the MBAP header present in all TCP Modbus packets.
//
// The amount of bytes that are expected to remain in the reader after
// a succesful call to this function is mbap.Length-1.
func decodeMBAP(r io.Reader) (mbap applicationHeader, n int, err error) {
	// We perform two passes to fail fast in case the protocol is unexpected.
	// We are not so much doing this because this is "faster", but because
	// we do not want to read more than we need from the reader in case the reader
	// is buffered and blocks when reaching end of buffer.
	var buf [4]byte
	n, err = io.ReadFull(r, buf[:])
	if err != nil {
		return applicationHeader{}, n, err
	}
	mbap.Transaction = binary.BigEndian.Uint16(buf[:2])
	mbap.Protocol = binary.BigEndian.Uint16(buf[2:4])
	if mbap.Protocol != 0 {
		return applicationHeader{}, n, fmt.Errorf("MBAP header got %d protocol, expected 0", mbap.Protocol)
	}
	ngot, err := io.ReadFull(r, buf[:3])
	n += ngot
	if err != nil {
		return applicationHeader{}, n, err
	}
	mbap.Length = binary.BigEndian.Uint16(buf[:2])
	mbap.Unit = buf[2]
	if mbap.Length < 2 {
		return applicationHeader{}, n, errors.New("MBAP header has Length field set to value under 2 or over 256")
	}
	return mbap, n, nil
}

func (ap *applicationHeader) Encode(w io.Writer) (int, error) {
	if ap.Length < 2 || ap.Length > 256 {
		return 0, errors.New("invalid MBAP length")
	}
	if ap.Protocol != 0 {
		return 0, errors.New("invalid protocol, must be 0")
	}
	var buf [7]byte
	ap.Put(buf[:])
	return w.Write(buf[:])
}

// Put puts the MBAP Header's 7 bytes in buf.
func (ap *applicationHeader) Put(buf []byte) error {
	if len(buf) < 7 {
		return io.ErrShortBuffer
	}
	binary.BigEndian.PutUint16(buf[:2], ap.Transaction)
	binary.BigEndian.PutUint16(buf[2:4], ap.Protocol)
	binary.BigEndian.PutUint16(buf[4:6], ap.Length)
	buf[6] = ap.Unit
	return nil
}
