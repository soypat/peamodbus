package peamodbus

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

type Rx struct {
	LastRecievedMBAP ApplicationHeader
	trp              io.ReadCloser
	RxCallbacks      RxCallbacks
}

type RxCallbacks struct {
	// OnError executed when a decoding error is encountered after
	// consuming a non-zero amoung of bytes from the underlying transport.
	// If this callback is set then it becomes the responsability of the callback
	// to close the underlying transport.
	OnError     func(rx *Rx, err error)
	OnException func(rx *Rx, exceptCode uint8) error
	// Called on all data structure access or modification function codes. MAy be any of the following:
	//  FCReadCoils, FCReadDiscreteInputs, FCReadHoldingRegister, FCReadInputRegister,
	//  FCWriteSingleCoil, FCWriteSingleRegister, FCWriteMultipleRegisters, FCReadFileRecord,
	//  FCWriteFileRecord, FCMaskWriteRegister, FCReadWriteMultipledRegisters, FCReadFIFOQueue:
	OnDataAccess func(rx *Rx, fc FunctionCode, r io.Reader) error
	// Is called on unhandled function code packets received.
	OnUnhandled func(rx *Rx, fc FunctionCode, r io.Reader) error
}

// SetRxTransport sets the underlying transport of rx.
func (rx *Rx) SetRxTransport(rc io.ReadCloser) {
	rx.trp = rc
}

func (rx *Rx) ReadNext() (int, error) {
	var reader io.Reader = rx.trp
	mbap, n, err := DecodeMBAP(reader)
	if err != nil {
		if n > 0 || errors.Is(err, io.EOF) {
			// EOF means the connection is no longer usable, must close.
			rx.handleErr(err)
		}
		return n, err
	}

	rx.LastRecievedMBAP = mbap
	var buf [1]byte
	ngot, err := reader.Read(buf[:1])
	n += ngot
	if ngot == 0 || err != nil {
		return n, fmt.Errorf("unable to read function code: %w", err)
	}
	// PDU excludes the Unit field which is 1 byte wide.
	pduLength := mbap.Length - 1
	// What remains to be read is then the PDU excluding the 1-byte
	// function code we just read.
	remaining := int64(pduLength - 1)
	lr := &io.LimitedReader{
		R: reader,
		N: remaining,
	}
	// Consume remaining data in reader if callback not registered.
	consume := func() {
		var discard [256]byte
		for err == nil {
			_, err = lr.Read(discard[:])
		}
	}
	fc := FunctionCode(buf[0])
	switch fc {
	case FCReadCoils, FCReadDiscreteInputs, FCReadHoldingRegister, FCReadInputRegister,
		FCWriteSingleCoil, FCWriteSingleRegister, FCWriteMultipleRegisters, FCReadFileRecord,
		FCWriteFileRecord, FCMaskWriteRegister, FCReadWriteMultipledRegisters, FCReadFIFOQueue:
		if rx.RxCallbacks.OnDataAccess != nil {
			err = rx.RxCallbacks.OnDataAccess(rx, fc, lr)
		} else {
			consume()
		}
	case FCReadExceptionStatus:
		if rx.RxCallbacks.OnException != nil {
			// err = rx.RxCallbacks.OnException(rx, fc)
		} else {
			consume()
		}
	default:
		if rx.RxCallbacks.OnUnhandled != nil {
			err = rx.RxCallbacks.OnUnhandled(rx, fc, lr)
		} else {
			consume()
		}
	}
	if err == nil && lr.N != 0 {
		err = errors.New("callback did not consume all bytes in frame")
	}
	if err != nil {
		rx.handleErr(err)
	}
	return n + int(remaining-lr.N), err
}

// handleErr is a convenience wrapper for RxCallbacks.OnError. If RxCallbacks.OnError
// is not defined then handleErr just closes the connection.
func (rx *Rx) handleErr(err error) {
	if rx.RxCallbacks.OnError != nil {
		rx.RxCallbacks.OnError(rx, err)
		return
	}
	rx.trp.Close()
}

// TxBuffered handles the marshalling of frames over a underlying transport.
type TxBuffered struct {
	buf         bytes.Buffer
	trp         io.WriteCloser
	TxCallbacks TxCallbacks
}

// NewTxBuffered creates a new TxBuffered ready for use.
func NewTxBuffered(wc io.WriteCloser) *TxBuffered {
	return &TxBuffered{trp: wc}
}

// SetTxTransport sets the underlying TxBuffered transport writer.
func (tx *TxBuffered) SetTxTransport(wc io.WriteCloser) {
	tx.trp = wc
}

// TxCallbacks stores functions to be called on events during marshalling of websocket frames.
type TxCallbacks struct {
	// OnError is called when
	OnError func(tx *TxBuffered, err error)
}

func (tx *TxBuffered) handleErr(err error) {
	if tx.TxCallbacks.OnError != nil {
		tx.TxCallbacks.OnError(tx, err)
		return
	}
	tx.trp.Close()
}

func (tx *TxBuffered) RequestReadHoldingRegisters(mbap ApplicationHeader, startAddr, numberOfRegisters uint16) (int, error) {
	mbap.Length = 6
	tx.buf.Reset()
	if _, err := mbap.Encode(&tx.buf); err != nil {
		return 0, err
	}

	var buf [4]byte
	binary.BigEndian.PutUint16(buf[:2], startAddr)
	binary.BigEndian.PutUint16(buf[2:4], numberOfRegisters)
	_, err := writeFunctionCode(&tx.buf, FCReadHoldingRegister, buf[:4])
	if err != nil {
		return 0, err
	}
	n, err := tx.buf.WriteTo(tx.trp)
	if n != 0 {
		tx.handleErr(err)
		return int(n), err
	}
	return int(n), nil
}

func (tx *TxBuffered) ResponseReadHoldingRegisters(mbap ApplicationHeader, registerData []byte) (int, error) {
	var buf [256]byte
	if len(registerData)%2 != 0 || len(registerData) > 255 {
		return 0, errors.New("register data length must be multiple of 2 and less than 255 in length")
	}
	buf[0] = byte(len(registerData)) // Amount of bytes to read. Modbus spec defines N = Quantity of registers.
	mbap.Length = 3 + uint16(buf[0])
	tx.buf.Reset()
	if _, err := mbap.Encode(&tx.buf); err != nil {
		return 0, err
	}
	copy(buf[1:], registerData)
	_, err := writeFunctionCode(&tx.buf, FCReadHoldingRegister, buf[:len(registerData)])
	if err != nil {
		return 0, err
	}
	n, err := tx.buf.WriteTo(tx.trp)
	if err != nil {
		tx.handleErr(err)
		return int(n), err
	}
	return int(n), nil
}

func writeFunctionCode(w io.Writer, fc FunctionCode, data []byte) (int, error) {
	err := encodeByte(w, byte(fc))
	if err != nil {
		return 0, err
	}
	n, err := writeFull(w, data)
	return n + 1, err
}

// ApplicationHeader is a compact representation of the MBAP header as described
// by Modbus TCP Implementation Guide. This header precedes all Modbus TCP packets.
type ApplicationHeader struct {
	Transaction uint16
	Protocol    uint16
	Length      uint16
	Unit        uint8
}

func DecodeMBAP(r io.Reader) (mbap ApplicationHeader, n int, err error) {
	// We perform two passes to fail fast in case the protocol is unexpected.
	// We are not so much doing this because this is "faster", but because
	// we do not want to read more than we need from the reader in case the reader
	// is buffered and blocks when reaching end of buffer.
	var buf [4]byte
	n, err = io.ReadFull(r, buf[:])
	if err != nil {
		return ApplicationHeader{}, n, err
	}
	mbap.Transaction = binary.BigEndian.Uint16(buf[:2])
	mbap.Protocol = binary.BigEndian.Uint16(buf[2:4])
	if mbap.Protocol != 0 {
		return ApplicationHeader{}, n, fmt.Errorf("MBAP header got %d protocol, expected 0", mbap.Protocol)
	}
	ngot, err := io.ReadFull(r, buf[:3])
	n += ngot
	if err != nil {
		return ApplicationHeader{}, n, err
	}
	mbap.Length = binary.BigEndian.Uint16(buf[:2])
	mbap.Unit = buf[2]
	if mbap.Length < 2 {
		return ApplicationHeader{}, n, errors.New("MBAP header has Length field set to value under 2 or over 256")
	}
	return mbap, n, nil
}

func (ap *ApplicationHeader) Encode(w io.Writer) (int, error) {
	if ap.Length < 2 || ap.Length > 256 {
		return 0, errors.New("invalid MBAP length")
	}
	if ap.Protocol != 0 {
		return 0, errors.New("invalid protocol, must be 0")
	}
	var buf [7]byte
	binary.BigEndian.PutUint16(buf[:2], ap.Transaction)
	binary.BigEndian.PutUint16(buf[2:4], ap.Protocol)
	binary.BigEndian.PutUint16(buf[4:6], ap.Length)
	buf[6] = ap.Unit
	return writeFull(w, buf[:])
}

// b2u8 converts a bool to an uint8. true==1, false==0.
func b2u8(b bool) uint8 {
	if b {
		return 1
	}
	return 0
}

func encodeByte(w io.Writer, b byte) error {
	buf := [1]byte{b}
	n, err := w.Write(buf[:1])
	if err == nil && n == 0 {
		return errors.New("unexpected 0 bytes written to buffer and no error returned")
	}
	return err
}

// writeFull is a convenience function to perform dst.Write and detect
// bad writer implementations while at it.
func writeFull(dst io.Writer, src []byte) (int, error) {
	n, err := dst.Write(src)
	if n != len(src) && err == nil {
		err = fmt.Errorf("bad writer implementation %T", dst)
	}
	return n, err
}
