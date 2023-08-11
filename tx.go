package peamodbus

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"strconv"
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
//
// May return a peamodbus exception code if the function code is not implemented.
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
		n += 6

	case FCDiagnostic, FCMaskWriteRegister, FCReadDeviceIdentification,
		FCReadFIFOQueue, FCReadFileRecord, FCReadWriteMultipleRegisters,
		FCWriteFileRecord:
		err = ExceptionIllegalFunction // Not implemented yet.

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
	if fc&0x80 != 0 {
		// Is exception code so only contains an error code byte and exception code byte.
		return fc &^ 0x80, 2, nil
	}
	switch fc {
	case FCReadCoils, FCReadDiscreteInputs:
		if len(b) < 2 {
			return 0, uint16(2 - len(b)), ErrMissingPacketData
		}
		n = uint16(b[1]) + 2

	case FCReadHoldingRegisters, FCReadInputRegisters:
		if len(b) < 2 {
			return 0, uint16(2 - len(b)), ErrMissingPacketData
		}
		n = 2 + uint16(b[1])
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
	FC             FunctionCode
	maybeByteCount uint8
	// First value usually contains Modbus address value.
	maybeAddr uint16
	// Second value usually contains Modbus quantity of addresses to read.
	maybeValueQuantity uint16
}

func (req Request) String() string {
	var quantityOrValue string = ", Quantity "
	if req.FC.IsWrite() {
		quantityOrValue = ", Value "
	}
	return "request to " + req.FC.String() + " @ Addr: " + strconv.Itoa(int(req.maybeAddr)) + quantityOrValue + strconv.Itoa(int(req.maybeValueQuantity))
}

// PutResponse writes the response to the receiver Request into dst.
//
// Returns a Exception returned by DataModel or if function code is not implemented.
func (req *Request) PutResponse(model DataModel, dst, data []byte) (packetLenWritten int, err error) {
	fc := req.FC
	shortSize := fc == FCReadHoldingRegisters || fc == FCReadInputRegisters
	quantityBytes := req.maybeValueQuantity
	if shortSize {
		if req.maybeByteCount != 0 {
			quantityBytes = uint16(req.maybeByteCount) // TODO(soypat): refactor this...
		} else {
			quantityBytes *= 2
		}
	}

	// Read Data Offset.
	const rdo = 2
	readData := data[rdo : rdo+quantityBytes]
	address := req.maybeAddr
	var exc Exception
	switch fc {
	case FCWriteSingleRegister, FCWriteSingleCoil:
		var scratch [2]byte
		binary.BigEndian.PutUint16(scratch[:2], quantityBytes)
		exc = writeToModel(model, fc, address, 1, scratch[:2])

	case FCWriteMultipleCoils, FCWriteMultipleRegisters:
		exc = writeToModel(model, fc, address, req.maybeValueQuantity, data[:req.maybeByteCount])

	case FCReadCoils, FCReadDiscreteInputs,
		FCReadHoldingRegisters, FCReadInputRegisters:
		exc = readFromModel(readData, model, fc, address, req.maybeValueQuantity)

	default:
		exc = ExceptionIllegalFunction
	}
	if exc != ExceptionNone {
		if exc > ExceptionMemoryParityError {
			return 0, fmt.Errorf("unknown exception code returned by DataModel (%d)", exc)
		}
		exc.PutResponse(dst, fc)
		return 2, exc
	}
	var tx Tx
	switch fc {
	// Read functions:
	case FCReadHoldingRegisters:
		packetLenWritten, err = tx.ResponseReadHoldingRegisters(dst, readData)
	case FCReadCoils:
		packetLenWritten, err = tx.ResponseReadCoils(dst, readData)
	case FCReadInputRegisters:
		packetLenWritten, err = tx.ResponseReadInputRegisters(dst, readData)
	case FCReadDiscreteInputs:
		packetLenWritten, err = tx.ResponseReadDiscreteInputs(dst, readData)

	// Write functions:
	case FCWriteSingleRegister:
		packetLenWritten, err = tx.ResponseWriteSingleRegister(dst, address, quantityBytes)
	case FCWriteSingleCoil:
		packetLenWritten, err = tx.ResponseWriteSingleCoil(dst, address, quantityBytes == 0xff00)
	case FCWriteMultipleRegisters:
		packetLenWritten, err = tx.ResponseWriteMultipleRegisters(dst, address, quantityBytes)
	default:
		panic("unhandled function code in request.Response()")
	}

	return packetLenWritten, err
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

// DecodeRequest parses a request packet and returns the request and the offset
// into the packet where the data starts, if there is any.
func DecodeRequest(pdu []byte) (req Request, dataoffset int, err error) {
	if len(pdu) < 2 {
		return req, 0, io.ErrShortBuffer
	}
	fc := FunctionCode(pdu[0])
	switch fc {
	// Request to read/write received.
	case FCReadHoldingRegisters, FCReadInputRegisters, FCReadCoils, FCReadDiscreteInputs,
		FCWriteSingleCoil, FCWriteSingleRegister:
		if len(pdu) < 5 {
			return req, 0, ErrMissingPacketData
		}
		req.FC = fc
		req.maybeAddr = binary.BigEndian.Uint16(pdu[1:])
		req.maybeValueQuantity = binary.BigEndian.Uint16(pdu[3:])

	case FCWriteMultipleCoils, FCWriteMultipleRegisters:
		if len(pdu) < 7 || len(pdu) < 6+int(pdu[5]) {
			return req, 0, ErrMissingPacketData
		}
		req.FC = fc
		req.maybeAddr = binary.BigEndian.Uint16(pdu[1:])
		req.maybeValueQuantity = binary.BigEndian.Uint16(pdu[3:])
		req.maybeByteCount = pdu[5]
		dataoffset = 6

	case FCReadFileRecord, FCWriteFileRecord, FCMaskWriteRegister, FCReadWriteMultipleRegisters, FCReadFIFOQueue:
		err = ExceptionIllegalFunction // Not implemented yet.

	case FCReadExceptionStatus:
		req.FC = fc

	default:
		err = fmt.Errorf("unhandled function code: %s", fc.String())
	}
	return req, dataoffset, err
}

// Tx provides the low level functions that marshal modbus packets onto byte slices.
//
// If implementing a modbus server it is very likely one will not interact
// with Tx directly but rather use the higher level PutResponse method of Request.
type Tx struct{}

var (
	errDiscreteOOB = errors.New("discrete address inputs/coils out of bounds (0..0xffff) or too many (1..2000)")
	errRegisterOOB = errors.New("register address out of bounds (0..0xffff) or too many (1..125 for read, 1..123 for write)")
	errCoilPacking = errors.New("RequestWriteMultipleCoils: packed coil data length does not match argument quantityOfOutputs")
)

// RequestReadCoils writes packet to dst used to read from 1 to 2000 contiguous status of coils in a remote device.
// startAddr must be in 0x0000..0xFFFF. quantityOfCoils must be in 1..2000.
func (tx *Tx) RequestReadCoils(dst []byte, startAddr, quantityOfCoils uint16) (int, error) {
	if quantityOfCoils > 2000 || quantityOfCoils == 0 {
		return 0, errDiscreteOOB
	}
	return tx.writeSimple2U16(dst, FCReadCoils, startAddr, quantityOfCoils, nil)
}

func (tx *Tx) RequestReadDiscreteInputs(dst []byte, startAddr, quantityOfInputs uint16) (int, error) {
	if quantityOfInputs > 2000 || quantityOfInputs == 0 {
		return 0, errDiscreteOOB
	}
	return tx.writeSimple2U16(dst, FCReadDiscreteInputs, startAddr, quantityOfInputs, nil)
}

// RequestReadDiscreteInputs writes packet to dst used to read from 1 to 125 contiguous holding registers.
func (tx *Tx) RequestReadHoldingRegisters(dst []byte, startAddr, numberOfRegisters uint16) (int, error) {
	if numberOfRegisters > 125 || numberOfRegisters == 0 {
		return 0, errRegisterOOB
	}
	return tx.writeSimple2U16(dst, FCReadHoldingRegisters, startAddr, numberOfRegisters, nil)
}

// RequestReadInputRegisters writes packet to dst used to read from 1 to 125 contiguous input registers in a remote device.
func (tx *Tx) RequestReadInputRegisters(dst []byte, startAddr, numberOfRegisters uint16) (int, error) {
	if numberOfRegisters > 125 || numberOfRegisters == 0 {
		return 0, errRegisterOOB
	}
	return tx.writeSimple2U16(dst, FCReadInputRegisters, startAddr, numberOfRegisters, nil)
}

// RequestWriteSingleCoil writes packet to dst used to write a single coil to either ON or OFF in a remote device.
func (tx *Tx) RequestWriteSingleCoil(dst []byte, addr uint16, value bool) (int, error) {
	var v uint16
	if value {
		v = 0xff00
	}
	return tx.writeSimple2U16(dst, FCWriteSingleCoil, addr, v, nil)
}

// RequestWriteSingleRegister writes packet to dst used to write a single holding register in a remote device.
func (tx *Tx) RequestWriteSingleRegister(dst []byte, addr, value uint16) (int, error) {
	return tx.writeSimple2U16(dst, FCWriteSingleRegister, addr, value, nil)
}

// RequestWriteMultipleCoils writes packet to dst used to force contiguous coils to either ON or OFF in a remote device.
// The data argument contains packed coil values.
func (tx *Tx) RequestWriteMultipleCoils(dst []byte, startAddr, quantityOfOutputs uint16, data []byte) (int, error) {
	ln := len(data)
	if len(data) < 1 || len(data) > 250 || quantityOfOutputs > 2000 || quantityOfOutputs == 0 || quantityOfOutputs > uint16(ln*8) {
		return 0, errDiscreteOOB
	}
	dataLenOK := (quantityOfOutputs/8 == uint16(ln)) || (quantityOfOutputs%8 != 0 && quantityOfOutputs/8+1 == uint16(ln))
	if !dataLenOK {
		return 0, errCoilPacking
	}
	if len(dst) < 6+ln {
		return 0, errResponseTooLargeTx
	}
	dst[0] = byte(FCWriteMultipleCoils)
	binary.BigEndian.PutUint16(dst[1:], startAddr)
	binary.BigEndian.PutUint16(dst[3:], quantityOfOutputs)
	dst[5] = byte(ln)
	copy(dst[6:], data)
	return 6 + ln, nil
}

// RequestWriteMultipleRegisters writes packet to dst used to write contiguous block of holding registers (1 to 123 registers) in a remote device.
func (tx *Tx) RequestWriteMultipleRegisters(dst []byte, startAddr uint16, registers []uint16) (int, error) {
	if len(registers) < 1 || len(registers) > 123 {
		return 0, errRegisterOOB
	}
	if len(dst) < 6+len(registers)*2 {
		return 0, errResponseTooLargeTx
	}
	dst[0] = byte(FCWriteMultipleRegisters)
	binary.BigEndian.PutUint16(dst[1:], startAddr)
	binary.BigEndian.PutUint16(dst[3:], uint16(len(registers)))
	NBytes := len(registers) * 2
	dst[5] = byte(NBytes)
	for i, r := range registers {
		binary.BigEndian.PutUint16(dst[6+i*2:], r)
	}
	return 6 + NBytes, nil
}

var errDataLengthMustBeMultipleOf2 = errors.New("data length must be multiple of 2")

func (tx *Tx) ResponseReadInputRegisters(dst, registerData []byte) (int, error) {
	if len(registerData)%2 != 0 {
		return 0, errDataLengthMustBeMultipleOf2
	}
	ln := byte(len(registerData))
	return tx.writeSimpleU8(dst, FCReadInputRegisters, ln, registerData)
}

func (tx *Tx) ResponseReadHoldingRegisters(dst, registerData []byte) (int, error) {
	if len(registerData)%2 != 0 {
		return 0, errDataLengthMustBeMultipleOf2
	}
	ln := byte(len(registerData))
	return tx.writeSimpleU8(dst, FCReadHoldingRegisters, ln, registerData)
}

func (tx *Tx) ResponseWriteSingleRegister(dst []byte, address, writtenValue uint16) (int, error) {
	return tx.writeSimple2U16(dst, FCWriteSingleRegister, address, writtenValue, nil)
}

func (tx *Tx) ResponseWriteMultipleRegisters(dst []byte, address, quantityOfOutputs uint16) (int, error) {
	return tx.writeSimple2U16(dst, FCWriteMultipleRegisters, address, quantityOfOutputs, nil)
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

func (tx *Tx) ResponseReadDiscreteInputs(dst, registerData []byte) (int, error) {
	ln := byte(len(registerData))
	return tx.writeSimpleU8(dst, FCReadDiscreteInputs, ln, registerData)
}

func (tx *Tx) ResponseWriteMultipleCoils(dst []byte, address, quantityOfOutputs uint16) (int, error) {
	return tx.writeSimple2U16(dst, FCWriteMultipleCoils, address, quantityOfOutputs, nil)
}

var errResponseTooLargeTx = errors.New("response/request data too large for tx buffer")

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
