package modbusrtu

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"sync"
	"time"

	"github.com/soypat/peamodbus"
	"golang.org/x/exp/slog"
)

var (
	errTimeout = errors.New("timeout waiting for rx")
)

type Client struct {
	state   connState
	muTx    sync.Mutex
	tx      peamodbus.Tx
	txbuf   [256]byte
	timeout time.Duration
}

type preprocessor = func(buf []byte, lastRead int) (start, end int)

type ClientConfig struct {
	// RxTimeout defines the maximum time to wait for a response to a request.
	RxTimeout time.Duration
	Logger    *slog.Logger
	// Preprocessor allows modifying received bytes before passing them to the
	// modbus parser. It should return the start and end of the modbus RTU packet.
	// The identity preprocessor returns the whole buffer: start=0, end=len(buf).
	Preprocessor preprocessor
}

// NewClient creates a new modbus RTU client.
func NewClient(cfg ClientConfig) *Client {
	// if cfg.RxTimeout <= 0 {
	// 	cfg.RxTimeout = 200 * time.Millisecond
	// }
	instr := &Client{
		state: connState{
			closeErr: errYetToConnect,
			log:      cfg.Logger,
			preproc:  cfg.Preprocessor,
		},
		timeout: cfg.RxTimeout,
	}
	return instr
}

// SetTransport sets the underlying communication port for the client.
func (c *Client) SetTransport(port io.ReadWriter) {
	if port == nil {
		panic("nil port")
	}
	c.muTx.Lock()
	defer c.muTx.Unlock()
	c.state.port = port
	c.state.closeErr = nil
}

// ReadHoldingRegisters reads a sequence of holding registers from a device.
func (c *Client) ReadHoldingRegisters(devAddr uint8, regAddr uint16, regs []uint16) error {
	return c.readRegisters(peamodbus.FCReadHoldingRegisters, devAddr, regAddr, regs)
}

// ReadHoldingRegisters reads a sequence of holding registers from a device.
func (c *Client) ReadInputRegisters(devAddr uint8, regAddr uint16, regs []uint16) error {
	return c.readRegisters(peamodbus.FCReadInputRegisters, devAddr, regAddr, regs)
}

func (c *Client) readRegisters(fc peamodbus.FunctionCode, devAddr uint8, regAddr uint16, regs []uint16) (err error) {
	c.muTx.Lock()
	defer c.muTx.Unlock()
	c.txbuf[0] = devAddr
	var n int
	switch fc {
	case peamodbus.FCReadHoldingRegisters:
		n, err = c.tx.RequestReadHoldingRegisters(c.txbuf[1:], regAddr, uint16(len(regs)))
	case peamodbus.FCReadInputRegisters:
		n, err = c.tx.RequestReadInputRegisters(c.txbuf[1:], regAddr, uint16(len(regs)))
	}
	if err != nil {
		return err
	}
	pdu, _, err := c.transaction(fc, devAddr, c.txbuf[:n+3])
	if err != nil {
		return err
	}
	pdu, err = peamodbus.ReceiveDataResponse(pdu)
	if err != nil {
		return err
	}
	if len(pdu) != len(regs)*2 {
		return errors.New("wrong number of registers")
	}
	for i := range regs {
		regs[i] = binary.BigEndian.Uint16(pdu[i*2:])
	}
	return nil
}

// WriteHoldingRegisters writes a sequence of holding registers to a device.
func (c *Client) WriteHoldingRegisters(devAddr uint8, regAddr uint16, regs []uint16) error {
	c.muTx.Lock()
	defer c.muTx.Unlock()
	c.txbuf[0] = devAddr
	n, err := c.tx.RequestWriteMultipleRegisters(c.txbuf[1:], regAddr, regs)
	if err != nil {
		return err
	}
	_, _, err = c.transaction(peamodbus.FCWriteMultipleRegisters, devAddr, c.txbuf[:n+3])
	if err != nil {
		return err
	}
	return err
}

// transaction performs a write and read transaction over the serial port.
// It receives a packet that is missing the CRC field but has the address field and PDU data.
// It will ignore packets that do not match
func (c *Client) transaction(rxFCFilter peamodbus.FunctionCode, addrFilter uint8, packetMissingCRC []byte) (pdu []byte, addr uint8, err error) {
	crc := generateCRC(packetMissingCRC[:len(packetMissingCRC)-2])
	binary.LittleEndian.PutUint16(packetMissingCRC[len(packetMissingCRC)-2:], crc)
	if c.state.log != nil && c.state.log.Handler().Enabled(context.TODO(), slog.LevelDebug) {
		c.state.debug("write outgoing packet",
			slog.Uint64("rtuaddr", uint64(packetMissingCRC[0])),
			slog.String("fc", peamodbus.FunctionCode(packetMissingCRC[1]).String()),
			slog.String("outgoing", string(packetMissingCRC)),
		)
	}

	_, err = c.state.port.Write(packetMissingCRC)
	if err != nil {
		return nil, 0, err
	}
	var deadline time.Time
	if c.timeout > 0 {
		deadline = time.Now().Add(c.timeout)
	}

	errcount := 0
	for (deadline.IsZero() || time.Until(deadline) > 0) && errcount < 5 {
		pdu, addr, err = c.state.TryRx(true)
		if len(pdu) > 1 && // All Modbus response PDUs are greater-equal than 2 bytes.
			(addrFilter == 0 || addrFilter == addr) && // Address filtering.
			(rxFCFilter == 0 || rxFCFilter == peamodbus.FunctionCode(pdu[0])) { // Function code filtering.
			break
		} else {
			pdu = nil // unset PDU.
		}
		if !errors.Is(peamodbus.ErrMissingPacketData, err) {
			errcount++
			time.Sleep(c.timeout / 16)
		}
	}
	switch {
	case err != nil:
		return pdu, 0, err // IO error or CRC fail.

	case pdu == nil:
		return nil, 0, errTimeout
	}
	return pdu, addr, nil
}
