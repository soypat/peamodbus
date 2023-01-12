package peamodbus

import (
	"context"
	"errors"
	"net"
	"strings"
	"time"
)

const (
	defaultKeepalive = 2 * time.Hour
	defaultPort      = ":502"
)

type Server struct {
	address   net.Addr
	keepalive time.Duration
}

type ServerConfig struct {
	// Formatted as IP with port. i.e: "192.168.1.35:502"
	// By default client uses port 502.
	Address string
	// It is recommended to enable KeepAlive on both client and server connections
	// in order to poll whether either has crashed. By default is set to 2 hours
	// as specified by the Modbus TCP/IP Implementation Guidelines.
	KeepAlive time.Duration
}

func NewClient(cfg ServerConfig) (*Server, error) {
	if cfg.KeepAlive == 0 {
		cfg.KeepAlive = defaultKeepalive
	}
	colons := strings.Count(cfg.Address, ":")
	if colons > 1 {
		return nil, errors.New("too many colons in address, ipv6 not supported")
	} else if colons == 0 {
		cfg.Address += defaultPort //Add default port.
	}
	c := &Server{
		address:   &tcpaddr{address: cfg.Address},
		keepalive: cfg.KeepAlive,
	}
	return c, nil
}

type tcpaddr struct {
	address string
}

func (a *tcpaddr) Network() string { return "tcp" }
func (a *tcpaddr) String() string  { return a.address }

func (c *Server) Connect(ctx context.Context) error {
	dialer := net.Dialer{
		Timeout:   time.Second,
		KeepAlive: c.keepalive,
		LocalAddr: c.address,
	}
	conn, err := dialer.DialContext(ctx, c.address.Network(), c.address.String())
	if err != nil {
		return err
	}
	panic(conn)
	return nil
}
