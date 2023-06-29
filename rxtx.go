package peamodbus

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

var (
	ErrMissingPacketData = errors.New("missing packet data")
	ErrBadFunctionCode   = errors.New("bad function code")
)

// InferRequestPacketLength returns the expected length of a client (master) request PDU in bytes
// by looking at the function code as the first byte of the packet and the
// contained data in the packet.
//
// If there is not enough data in the packet to infer the length of the packet then
// InferResponsePacketLength returns ErrMissingPacketData and the number of bytes
// needed to be able to infer the packet length while guaranteeing no over-reads.
func InferRequestPacketLength(b []byte) (fc FunctionCode, n uint16, err error) {
	if len(b) < 1 {
		return 0, 1, ErrMissingPacketData
	}
	fc = FunctionCode(b[0])
	switch fc {
	case FCReadCoils, FCReadDiscreteInputs, FCReadHoldingRegisters, FCReadInputRegisters,
		FCWriteSingleCoil, FCWriteSingleRegister:
		n = 5
	case FCReadExceptionStatus, FCGetComEventCounter, FCGetComEventLog, FCReportServerID:
		n = 1
	case FCWriteMultipleCoils, FCWriteMultipleRegisters:
		if len(b) < 6 {
			return 0, uint16(6 - len(b)), ErrMissingPacketData
		}
		n = uint16(b[5])
		if fc == FCWriteMultipleCoils && n%8 != 0 {
			n++
		} else if fc == FCWriteMultipleRegisters {
			n *= 2
		}
		n += 6

	case FCDiagnostic:

		fallthrough
	default:
		err = ErrBadFunctionCode
	}

	return fc, n, err
}

// InferResponsePacketLength returns the expected length of a server (instrument) response PDU in bytes
// by looking at the function code as the first byte of the response packet and the
// contained data in the packet.
//
// If there is not enough data in the packet to infer the length of the packet then
// InferResponsePacketLength returns ErrMissingPacketData and the number of bytes
// needed to be able to infer the packet length.
func InferResponsePacketLength(b []byte) (fc FunctionCode, n uint16, err error) {
	if len(b) < 1 {
		return 0, 1, ErrMissingPacketData
	}
	fc = FunctionCode(b[0])
	switch fc {
	case FCReadCoils, FCReadDiscreteInputs:
		if len(b) < 2 {
			return 0, uint16(2 - len(b)), ErrMissingPacketData
		}
		n = uint16(b[1])
		if n%8 != 0 {
			n++
		}
		n += 2

	case FCReadHoldingRegisters, FCReadInputRegisters:
		if len(b) < 2 {
			return 0, uint16(2 - len(b)), ErrMissingPacketData
		}
		n = 2 + uint16(b[1])*2
	case FCWriteSingleCoil, FCWriteSingleRegister, FCGetComEventCounter, FCWriteMultipleCoils, FCWriteMultipleRegisters:
		n = 5
	case FCReadExceptionStatus:
		n = 2
	case FCDiagnostic:
		if len(b) < 2 {
			return 0, uint16(2 - len(b)), ErrMissingPacketData
		}
		n = 3 + uint16(b[1])
	case FCGetComEventLog:
		if len(b) < 2 {
			return 0, uint16(2 - len(b)), ErrMissingPacketData
		}
		n = 2 + uint16(b[1])

	default:
		err = ErrBadFunctionCode
	}

	return fc, n, err
}

// Request is a client (master) request meant for a server (instrument).
type Request struct {
	FC FunctionCode
	// First value usually contains Modbus address value.
	maybeAddr uint16
	// Second value usually contains Modbus quantity of addresses to read.
	maybeValueQuantity uint16
}

func (req Request) String() string {
	if req.FC.IsWrite() {
		return fmt.Sprintf("request to %s @ Addr: %d, Value: %d", req.FC, req.maybeAddr, req.maybeValueQuantity)
	}
	return fmt.Sprintf("request to %s @ Addr: %d, Quantity: %d", req.FC, req.maybeAddr, req.maybeValueQuantity)
}

// PutResponse generates a response packet for the request.
func (req *Request) PutResponse(tx *Tx, model DataModel, dst, scratch []byte) (packet int, err error) {
	fc := req.FC
	isRead := fc.IsRead()
	shortSize := fc == FCReadHoldingRegisters || fc == FCReadInputRegisters
	quantityBytes := req.maybeValueQuantity
	if shortSize {
		quantityBytes *= 2
	}
	if isRead && int(quantityBytes) > len(scratch) {
		return 0, io.ErrShortBuffer
	}
	address := req.maybeAddr
	if fc == FCWriteSingleRegister || fc == FCWriteSingleCoil {
		binary.BigEndian.PutUint16(scratch[:2], quantityBytes)
		err = writeToModel(model, fc, address, 1, scratch[:2])
	} else {
		err = readFromModel(scratch[:quantityBytes], model, fc, address, req.maybeValueQuantity)
	}
	if err != nil {
		return 0, fmt.Errorf("handling fc=%q accessing data model: %w", fc, err)
	}

	switch fc {
	// Read functions:
	case FCReadHoldingRegisters:
		packet, err = tx.ResponseReadHoldingRegisters(dst, scratch[:quantityBytes])
	case FCReadCoils:
		packet, err = tx.ResponseReadCoils(dst, scratch[:quantityBytes])
	case FCReadInputRegisters:
		packet, err = tx.ResponseReadInputRegisters(dst, scratch[:quantityBytes])
	case FCReadDiscreteInputs:
		packet, err = tx.ResponseReadDiscreteInput(dst, scratch[:quantityBytes])

	// Write functions:
	case FCWriteSingleRegister:
		packet, err = tx.ResponseWriteSingleRegister(dst, address, quantityBytes)
	case FCWriteSingleCoil:
		packet, err = tx.ResponseWriteSingleCoil(dst, address, quantityBytes == 0xff00)
	default:
		panic("unhandled function code in request.Response()")
	}

	return packet, err
}

type Rx struct {
	LastPendingRequest Request
	RxCallbacks        RxCallbacks
}

type RxCallbacks struct {
	// OnError executed when a decoding error is encountered after
	// consuming a non-zero amoung of bytes from the underlying transport.
	// If this callback is set then it becomes the responsability of the callback
	// to close the underlying transport.
	OnError     func(rx *Rx, err error)
	OnException func(rx *Rx, except Exception) error

	// Called on PDUs with function codes coming from a client that have undefined byte length.
	// May be any of the following:
	//  FCWriteMultipleRegisters, FCReadFileRecord,
	//  FCWriteFileRecord, FCMaskWriteRegister, FCReadWriteMultipledRegisters, FCReadFIFOQueue:
	OnDataLong func(rx *Rx, fc FunctionCode, buf []byte) error

	// Called on PDUs with function codes coming from a client that have defined byte length.
	//  FCReadCoils, FCReadDiscreteInputs, FCReadHoldingRegister, FCReadInputRegister,
	//  FCWriteSingleCoil, FCWriteSingleRegister
	OnData func(rx *Rx, req Request) error

	// Is called on unhandled function code packets received.
	OnUnhandled func(rx *Rx, fc FunctionCode, buf []byte) error
}

// ReceiveSingleWriteResponse
func ReceiveSingleWriteResponse(pdu []byte) (addr, value uint16, err error) {
	if len(pdu) < 5 {
		return 0, 0, io.ErrShortBuffer
	}
	fc := FunctionCode(pdu[0])
	switch fc {
	case FCWriteSingleCoil, FCWriteSingleRegister:
		if len(pdu) < 5 {
			return 0, 0, io.ErrShortBuffer
		}
		addr = binary.BigEndian.Uint16(pdu[1:])
		value = binary.BigEndian.Uint16(pdu[3:])

	default:
		err = ErrBadFunctionCode
	}
	return addr, value, err
}

// ReceiveDataResponse decodes a response packet for any of the following packets:
//
//	FCReadCoils, FCReadDiscreteInputs, FCReadHoldingRegisters, FCReadInputRegisters
func ReceiveDataResponse(pdu []byte) (data []byte, err error) {
	if len(pdu) < 2 {
		return nil, io.ErrShortBuffer
	}
	fc := FunctionCode(pdu[0])
	switch fc {
	case FCReadCoils, FCReadDiscreteInputs, FCReadHoldingRegisters, FCReadInputRegisters:
		if len(pdu) < 2+int(pdu[1]) { // pdu[1] Always contains byte count.
			return nil, io.ErrShortBuffer
		}
		data = pdu[2:]
	default:
		err = ErrBadFunctionCode
	}
	return data, err
}

func (rx *Rx) ReceiveRequest(pdu []byte) (err error) {
	if len(pdu) < 2 {
		return io.ErrShortBuffer
	}
	fc := FunctionCode(pdu[0])
	switch fc {

	// Request to read/write received.
	case FCReadHoldingRegisters, FCReadInputRegisters, FCReadCoils, FCReadDiscreteInputs,
		FCWriteSingleCoil, FCWriteSingleRegister:
		if len(pdu) < 5 {
			return io.ErrShortBuffer
		}
		rx.LastPendingRequest.FC = fc
		rx.LastPendingRequest.maybeAddr = binary.BigEndian.Uint16(pdu[1:])
		rx.LastPendingRequest.maybeValueQuantity = binary.BigEndian.Uint16(pdu[3:])
		if rx.RxCallbacks.OnData != nil {
			err = rx.RxCallbacks.OnData(rx, rx.LastPendingRequest)
		}

	case FCWriteMultipleRegisters, FCReadFileRecord,
		FCWriteFileRecord, FCMaskWriteRegister, FCReadWriteMultipleRegisters, FCReadFIFOQueue:
		if rx.RxCallbacks.OnDataLong != nil {
			err = rx.RxCallbacks.OnDataLong(rx, fc, pdu[1:])
		}

	case FCReadExceptionStatus:
		if rx.RxCallbacks.OnException != nil {
			err = rx.RxCallbacks.OnException(rx, Exception(pdu[1]))
		}
	default:
		if rx.RxCallbacks.OnUnhandled != nil {
			err = rx.RxCallbacks.OnUnhandled(rx, fc, pdu[1:])
		}
	}
	if err != nil {
		rx.handleErr(err)
	}
	return err
}

// handleErr is a convenience wrapper for RxCallbacks.OnError. If RxCallbacks.OnError
// is not defined then handleErr just closes the connection.
func (rx *Rx) handleErr(err error) {
	if rx.RxCallbacks.OnError != nil {
		rx.RxCallbacks.OnError(rx, err)
		return
	}
}

// Tx handles the marshalling of frames over a underlying transport.
type Tx struct {
	TxCallbacks TxCallbacks
}

// TxCallbacks stores functions to be called on events during marshalling of websocket frames.
type TxCallbacks struct {
	// OnError is called when
	OnError func(tx *Tx, err error)
	// OnDataAccess func(tx *Tx, fc FunctionCode, startAddr uint16, dst []byte) error
}

// NewTx creates a new Tx ready for use.
func NewTx() *Tx {
	tx := Tx{}
	return &tx
}

func (tx *Tx) RequestReadHoldingRegisters(dst []byte, startAddr, numberOfRegisters uint16) (int, error) {
	var buf [4]byte
	binary.BigEndian.PutUint16(buf[:2], startAddr)
	binary.BigEndian.PutUint16(buf[2:4], numberOfRegisters)
	return tx.writeSimple(dst, FCReadHoldingRegisters, buf[:4])
}

var errDataLengthMustBeMultipleOf2 = errors.New("data length must be multiple of 2")

func (tx *Tx) ResponseReadInputRegisters(dst, registerData []byte) (int, error) {
	if len(registerData)%2 != 0 {
		return 0, errDataLengthMustBeMultipleOf2
	}
	ln := byte(len(registerData)) / 2
	return tx.writeSimpleU8(dst, FCReadInputRegisters, ln, registerData)
}

func (tx *Tx) ResponseReadHoldingRegisters(dst, registerData []byte) (int, error) {
	if len(registerData)%2 != 0 {
		return 0, errDataLengthMustBeMultipleOf2
	}
	ln := byte(len(registerData)) / 2
	return tx.writeSimpleU8(dst, FCReadHoldingRegisters, ln, registerData)
}

func (tx *Tx) ResponseWriteSingleRegister(dst []byte, address, writtenValue uint16) (int, error) {
	return tx.writeSimple2U16(dst, FCWriteSingleCoil, address, writtenValue, nil)
}

func (tx *Tx) ResponseWriteSingleCoil(dst []byte, address uint16, writtenValue bool) (int, error) {
	var outputValue uint16
	if writtenValue {
		outputValue = 0xff00
	}
	return tx.writeSimple2U16(dst, FCWriteSingleCoil, address, outputValue, nil)
}

func (tx *Tx) ResponseReadCoils(dst, registerData []byte) (int, error) {
	ln := byte(len(registerData))
	return tx.writeSimpleU8(dst, FCReadCoils, ln, registerData)
}

func (tx *Tx) ResponseReadDiscreteInput(dst, registerData []byte) (int, error) {
	ln := byte(len(registerData))
	return tx.writeSimpleU8(dst, FCReadDiscreteInputs, ln, registerData)
}

var errResponseTooLargeTx = errors.New("response data too large for tx buffer")

func (tx *Tx) writeSimple(dst []byte, fc FunctionCode, responseData []byte) (int, error) {
	if len(responseData) > len(dst)-1 {
		return 0, errResponseTooLargeTx
	}
	dst[0] = byte(fc)
	n := copy(dst[1:], responseData)
	return n + 1, nil
}

func (tx *Tx) writeSimpleU8(dst []byte, fc FunctionCode, v1 uint8, responseData []byte) (int, error) {
	if len(responseData) > len(dst)-(1+1) {
		return 0, errResponseTooLargeTx
	}
	dst[0] = byte(fc)
	dst[1] = v1
	n := copy(dst[2:], responseData)
	return 2 + n, nil
}

func (tx *Tx) writeSimple2U16(dst []byte, fc FunctionCode, v1, v2 uint16, responseData []byte) (int, error) {
	if len(responseData) > len(dst)-(1+4) {
		return 0, errResponseTooLargeTx
	}
	dst[0] = byte(fc)
	binary.BigEndian.PutUint16(dst[1:3], v1)
	binary.BigEndian.PutUint16(dst[3:5], v2)
	n := copy(dst[5:], responseData)
	return 5 + n, nil
}
