package modbusrtu

import (
	"encoding/binary"
	"errors"
	"io"
	"sync"
	"time"

	"github.com/soypat/peamodbus"
)

var (
	errWrongAddress = errors.New("wrong address")
)

type Client struct {
	state   connState
	rx      peamodbus.Rx
	muTx    sync.Mutex
	tx      peamodbus.Tx
	txbuf   [256]byte
	timeout time.Duration
}

func NewClient(port io.ReadWriter, timeout time.Duration) *Client {
	instr := &Client{
		state: connState{
			closeErr: errYetToConnect,
			port:     port,
		},
		timeout: timeout,
	}
	instr.rx.RxCallbacks, instr.tx.TxCallbacks = instr.state.callbacks()
	return instr
}

func (c *Client) ReadHoldingRegisters(devAddr uint8, regAddr uint16, regs []uint16) error {
	if len(regs) > 125 {
		return errors.New("too many registers")
	}
	c.muTx.Lock()
	defer c.muTx.Unlock()
	n, err := c.tx.RequestReadHoldingRegisters(c.txbuf[1:], regAddr, uint16(len(regs)))
	if err != nil {
		return err
	}
	c.txbuf[0] = devAddr
	crc := generateCRC(c.txbuf[:n+1])
	binary.BigEndian.PutUint16(c.txbuf[n+1:], crc)
	_, err = c.state.port.Write(c.txbuf[:n+3])
	if err != nil {
		return err
	}
	deadline := time.Now().Add(c.timeout)
	errcount := 0
	var pdu []byte
	var addr uint8
	for time.Until(deadline) > 0 && errcount < 5 {
		pdu, addr, err = c.state.TryRx()
		if err == nil && devAddr == addr {
			break
		}
		if !errors.Is(peamodbus.ErrMissingPacketData, err) {
			errcount++
			time.Sleep(time.Millisecond)
		}
	}
	if err != nil {
		return err
	}
	if devAddr != addr {
		return errWrongAddress
	}
	err = c.rx.Receive(pdu)
	if err != nil {
		return err
	}
	return nil
}
