package peamodbus

import (
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
		if lr.N == 0 && errors.Is(err, io.EOF) {
			err = nil
		}
	}
	fc := FunctionCode(buf[0])
	switch fc {
	case FCReadCoils, FCReadDiscreteInputs, FCReadHoldingRegisters, FCReadInputRegisters,
		FCWriteSingleCoil, FCWriteSingleRegister, FCWriteMultipleRegisters, FCReadFileRecord,
		FCWriteFileRecord, FCMaskWriteRegister, FCReadWriteMultipleRegisters, FCReadFIFOQueue:
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
	if lr.N != 0 && err == nil {
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

const txBufLen = 256

// Tx handles the marshalling of frames over a underlying transport.
type Tx struct {
	trp         io.WriteCloser
	TxCallbacks TxCallbacks
	buf         [txBufLen]byte
}

// TxCallbacks stores functions to be called on events during marshalling of websocket frames.
type TxCallbacks struct {
	// OnError is called when
	OnError func(tx *Tx, err error)
	// OnDataAccess func(tx *Tx, fc FunctionCode, startAddr uint16, dst []byte) error
}

// NewTxBuffered creates a new TxBuffered ready for use.
func NewTxBuffered(wc io.WriteCloser) *Tx {
	return &Tx{trp: wc}
}

// SetTxTransport sets the underlying TxBuffered transport writer.
func (tx *Tx) SetTxTransport(wc io.WriteCloser) {
	tx.trp = wc
}

func (tx *Tx) handleErr(err error) {
	if tx.TxCallbacks.OnError != nil {
		tx.TxCallbacks.OnError(tx, err)
		return
	}
	tx.trp.Close()
}

func (tx *Tx) RequestReadHoldingRegisters(mbap ApplicationHeader, startAddr, numberOfRegisters uint16) (int, error) {
	mbap.Length = 6
	var buf [4]byte
	binary.BigEndian.PutUint16(buf[:2], startAddr)
	binary.BigEndian.PutUint16(buf[2:4], numberOfRegisters)
	return tx.writeSimple(mbap, FCReadHoldingRegisters, buf[:4])
}

func (tx *Tx) ResponseReadInputRegisters(mbap ApplicationHeader, registerData []byte) (int, error) {
	if len(registerData)%2 != 0 || len(registerData) > 255 {
		return 0, errors.New("register data length must be multiple of 2 and less than 255 in length")
	}
	ln := byte(len(registerData))
	mbap.Length = 3 + uint16(ln)
	return tx.writeSimpleU8(mbap, FCReadInputRegisters, ln, registerData)
}

func (tx *Tx) ResponseReadHoldingRegisters(mbap ApplicationHeader, registerData []byte) (int, error) {
	if len(registerData)%2 != 0 || len(registerData) > 255 {
		return 0, errors.New("register data length must be multiple of 2 and less than 255 in length")
	}
	ln := byte(len(registerData))
	mbap.Length = 3 + uint16(ln)
	return tx.writeSimpleU8(mbap, FCReadHoldingRegisters, ln, registerData)
}

func (tx *Tx) ResponseWriteSingleRegister(mbap ApplicationHeader, address, writtenValue uint16) (int, error) {
	return tx.writeSimple2U16(mbap, FCWriteSingleCoil, address, writtenValue, nil)
}

func (tx *Tx) ResponseWriteSingleCoil(mbap ApplicationHeader, address uint16, writtenValue bool) (int, error) {
	var outputValue uint16
	if writtenValue {
		outputValue = 0xff00
	}
	return tx.writeSimple2U16(mbap, FCWriteSingleCoil, address, outputValue, nil)
}

func (tx *Tx) ResponseReadCoils(mbap ApplicationHeader, registerData []byte) (int, error) {
	if len(registerData) > 255 {
		return 0, errors.New("register data length must be less than 255 in length")
	}
	ln := byte(len(registerData))
	return tx.writeSimpleU8(mbap, FCReadCoils, ln, registerData)
}

func (tx *Tx) ResponseReadDiscreteInput(mbap ApplicationHeader, registerData []byte) (int, error) {
	if len(registerData) > 255 {
		return 0, errors.New("register data length must be less than 255 in length")
	}
	ln := byte(len(registerData))
	return tx.writeSimpleU8(mbap, FCReadDiscreteInputs, ln, registerData)
}

var errResponseTooLargeTx = errors.New("response data too large for tx buffer")

func (tx *Tx) writeSimple(mbap ApplicationHeader, fc FunctionCode, responseData []byte) (int, error) {
	if len(responseData) > txBufLen-8 {
		return 0, errResponseTooLargeTx
	}
	mbap.Length = 2 + uint16(len(responseData))
	mbap.Put(tx.buf[:])
	tx.buf[7] = byte(fc)
	n := copy(tx.buf[8:], responseData)
	return writeFull(tx.trp, tx.buf[:8+n])
}

func (tx *Tx) writeSimpleU8(mbap ApplicationHeader, fc FunctionCode, v1 uint8, responseData []byte) (int, error) {
	if len(responseData) > txBufLen-9 {
		return 0, errResponseTooLargeTx
	}
	mbap.Length = 3 + uint16(len(responseData))
	mbap.Put(tx.buf[:])
	tx.buf[7] = byte(fc)
	tx.buf[8] = v1
	n := copy(tx.buf[9:], responseData)
	return writeFull(tx.trp, tx.buf[:9+n])
}

func (tx *Tx) writeSimpleU16(mbap ApplicationHeader, fc FunctionCode, v1 uint16, responseData []byte) (int, error) {
	if len(responseData) > txBufLen-10 {
		return 0, errResponseTooLargeTx
	}
	mbap.Length = 4 + uint16(len(responseData))
	mbap.Put(tx.buf[:])
	tx.buf[7] = byte(fc)
	binary.BigEndian.PutUint16(tx.buf[8:10], v1)
	n := copy(tx.buf[10:], responseData)
	return writeFull(tx.trp, tx.buf[:10+n])
}

func (tx *Tx) writeSimple2U16(mbap ApplicationHeader, fc FunctionCode, v1, v2 uint16, responseData []byte) (int, error) {
	if len(responseData) > txBufLen-12 {
		return 0, errResponseTooLargeTx
	}
	mbap.Length = 6 + uint16(len(responseData))
	mbap.Put(tx.buf[:])
	tx.buf[7] = byte(fc)
	binary.BigEndian.PutUint16(tx.buf[8:10], v1)
	binary.BigEndian.PutUint16(tx.buf[10:12], v2)
	n := copy(tx.buf[12:], responseData)
	return writeFull(tx.trp, tx.buf[:12+n])
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
	ap.Put(buf[:])
	return writeFull(w, buf[:])
}

// Put puts the MBAP Header's 7 bytes in buf.
func (ap *ApplicationHeader) Put(buf []byte) error {
	if len(buf) < 7 {
		return io.ErrShortBuffer
	}
	binary.BigEndian.PutUint16(buf[:2], ap.Transaction)
	binary.BigEndian.PutUint16(buf[2:4], ap.Protocol)
	binary.BigEndian.PutUint16(buf[4:6], ap.Length)
	buf[6] = ap.Unit
	return nil
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
