package modbustcp

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/netip"
	"strings"
	"time"

	"github.com/soypat/peamodbus"
)

const (
	defaultKeepalive = 2*time.Hour - time.Minute
	defaultPort      = ":502"
	mbapSize         = 7
)

// Server is a Modbus TCP Server implementation. A Server listens on a network
// and awaits a client's request. Servers are typically sensors or actuators in an industrial setting.
// Server's methods are not all safe for concurrent use.
type Server struct {
	state      serverState
	tcpTimeout time.Duration
	address    net.TCPAddr
	txBuf      [256]byte
	rxbuf      [256]byte
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
	// sv.rx.RxCallbacks, sv.tx.TxCallbacks = sv.state.callbacks()
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
func (sv *Server) HandleNext() (err error) {
	if err := sv.Err(); err != nil {
		return errors.New("disconnected: " + err.Error())
	}
	// rcvBuf is buffer used to receive the request.
	rcvBuf := sv.rxbuf[:]
	_, err = io.ReadFull(sv.state.conn, rcvBuf[:mbapSize])
	if err != nil {
		if errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed) {
			sv.state.CloseConn(err)
		}
		return err
	}

	mbap, err := decodeMBAP(rcvBuf[:mbapSize])
	if err != nil {
		sv.state.CloseConn(err)
		return err
	}
	sv.state.mu.Lock()
	sv.state.lastMBAP = mbap
	sv.state.mu.Unlock()
	remaining := mbap.Length - 1
	if int(remaining) > len(rcvBuf) {
		err = fmt.Errorf("invalid MBAP length %d", mbap.Length)
		sv.state.CloseConn(err)
		return err
	}
	_, err = io.ReadFull(sv.state.conn, rcvBuf[:remaining])
	if err != nil {
		sv.state.CloseConn(err)
		return err
	}
	fc, inferred, err := peamodbus.InferRequestPacketLength(rcvBuf[:remaining])
	if err != nil || inferred != remaining {
		if exc, ok := err.(peamodbus.Exception); ok {
			return sv.handleException(exc, fc)
		}
		log.Println("error inferring packet length:", err, " inferred:", inferred, " actualRemaining:", remaining, " fc:", fc.String())
	}
	req, dataoffset, err := peamodbus.DecodeRequest(rcvBuf[:remaining])
	if err != nil {
		sv.state.CloseConn(err)
		return err
	}
	// sendbuf is the buffer used to send the response.
	sendbuf := sv.txBuf[:]
	plen, err := req.PutResponse(sv.state.data, sendbuf[mbapSize:], rcvBuf[dataoffset:])
	if err != nil {
		if exc, ok := err.(peamodbus.Exception); ok {
			return sv.handleException(exc, fc)
		}
		sv.state.CloseConn(err)
		return err
	}
	mbap.Length = uint16(plen + 1)
	mbap.Put(sendbuf[:mbapSize])
	_, err = sv.state.conn.Write(sendbuf[:plen+mbapSize])
	if err != nil {
		sv.state.CloseConn(err)
	}
	return err
}

// handleException writes the exception response to the network.
func (sv *Server) handleException(exc peamodbus.Exception, fc peamodbus.FunctionCode) error {
	sendbuf := sv.txBuf[:]
	sv.state.lastMBAP.Put(sendbuf[:mbapSize])
	exc.PutResponse(sendbuf[mbapSize:], fc)
	_, err := sv.state.conn.Write(sendbuf[:mbapSize+2])
	if err != nil {
		sv.state.CloseConn(err)
	}
	return err
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
func decodeMBAP(buf []byte) (mbap applicationHeader, err error) {
	if len(buf) < mbapSize {
		return mbap, io.ErrShortBuffer
	}
	mbap.Protocol = binary.BigEndian.Uint16(buf[2:4])
	if mbap.Protocol != 0 {
		return applicationHeader{}, fmt.Errorf("MBAP header got %d protocol, expected 0", mbap.Protocol)
	}
	mbap.Transaction = binary.BigEndian.Uint16(buf[:2])
	mbap.Length = binary.BigEndian.Uint16(buf[4:6])
	mbap.Unit = buf[6]
	return mbap, nil
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
