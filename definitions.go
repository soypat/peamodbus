package peamodbus

import "strconv"

type FunctionCode uint8

// Data access function codes.
const (
	// FCReadCoils a.k.a ReadDiscreteOutputs.
	// Request:
	//  0    1        2           3         4         5
	//  | FC | StartAddr (uint16) | Quantity (uint16) |
	// Response:
	//  0    1             2            2+n+X   where X=n%8 == 0 ? 0 : 1
	//  | FC | ByteCount=n | Coil status |
	FCReadCoils                  FunctionCode = 0x01
	FCReadDiscreteInputs         FunctionCode = 0x02
	FCReadHoldingRegisters       FunctionCode = 0x03
	FCReadInputRegisters         FunctionCode = 0x04
	FCWriteSingleCoil            FunctionCode = 0x05
	FCWriteSingleRegister        FunctionCode = 0x06 // Holding register.
	FCWriteMultipleRegisters     FunctionCode = 0x10 // Holding registers.
	FCReadFileRecord             FunctionCode = 0x14
	FCWriteFileRecord            FunctionCode = 0x15
	FCMaskWriteRegister          FunctionCode = 0x16
	FCReadWriteMultipleRegisters FunctionCode = 0x17
	FCReadFIFOQueue              FunctionCode = 0x18
	FCWriteMultipleCoils         FunctionCode = 0x0F
)

// Diagnostic function codes.
const (
	FCReadExceptionStatus      FunctionCode = 0x07
	FCDiagnostic               FunctionCode = 0x08
	FCGetComEventCounter       FunctionCode = 0x0B
	FCGetComEventLog           FunctionCode = 0x0C
	FCReportServerID           FunctionCode = 0x11
	FCReadDeviceIdentification FunctionCode = 0x2B
)

// IsWrite returns true if fc is an exclusively write operation. Returns false if fc is a read/write operation.
func (fc FunctionCode) IsWrite() bool {
	return fc == FCWriteSingleCoil || fc == FCWriteSingleRegister ||
		fc == FCWriteMultipleRegisters || fc == FCWriteFileRecord ||
		fc == FCMaskWriteRegister
}

// IsRead returns true if fc is an exclusively read operation. Returns false if fc is a read/write operation.
func (fc FunctionCode) IsRead() bool {
	return fc == FCReadCoils || fc == FCReadDiscreteInputs ||
		fc == FCReadHoldingRegisters || fc == FCReadInputRegisters ||
		fc == FCReadFIFOQueue || fc == FCReadFileRecord
}

// String returns a human readable string representation of the function code.
func (fc FunctionCode) String() (s string) {
	switch fc {
	case FCReadCoils:
		s = "read coils"
	case FCReadDiscreteInputs:
		s = "read discrete inputs"
	case FCReadHoldingRegisters:
		s = "read holding registers"
	case FCReadInputRegisters:
		s = "read input registers"
	case FCWriteSingleCoil:
		s = "write single coil"
	case FCWriteSingleRegister:
		s = "write single register"
	case FCWriteMultipleRegisters:
		s = "write multiple registers"
	case FCReadFileRecord:
		s = "read file record"
	case FCWriteFileRecord:
		s = "write file record"
	case FCMaskWriteRegister:
		s = "mask write register"
	case FCReadWriteMultipleRegisters:
		s = "read/write multiple registers"
	case FCReadFIFOQueue:
		s = "read FIFO queue"
	case FCWriteMultipleCoils:
		s = "write multiple coils"
	case FCReadExceptionStatus:
		s = "read exception status"
	case FCDiagnostic:
		s = "diagnostic"
	case FCGetComEventCounter:
		s = "get com event counter"
	case FCGetComEventLog:
		s = "get com event log"
	case FCReportServerID:
		s = "report server ID"
	case FCReadDeviceIdentification:
		s = "read device identification"
	default:
		if fc7bits := fc &^ 0x80; fc7bits != fc && (fc7bits.IsRead() || fc7bits.IsWrite()) {
			s = "exception!<" + fc7bits.String() + ">"
		} else {
			s = "unknown function code (" + strconv.Itoa(int(fc)) + ")"
		}
	}
	return s
}

// Exception represents a modbus Exception Code.
// Appears as section 6.7 in the modbus specification.
// A Value of 0 is equivalent to no exception.
type Exception uint8

const (
	// ExceptionNone indicates no exception has ocurred.
	ExceptionNone Exception = iota
	// The function code received in the query is not an allowable action for the slave.
	// If a Poll Program Complete command was issued, this code indicates that no
	// program function preceded it.
	ExceptionIllegalFunction
	//  The data address received in the query is not an allowable address for the slave.
	ExceptionIllegalDataAddr
	// A value contained in the query data field is not an allowable value for the slave.
	ExceptionIllegalDataValue
	// An unrecoverable error occurred while the slave was attempting to perform the requested action.
	ExceptionDeviceFailure
	// The slave has accepted the request and is processing it, but a long duration
	// of time will be required to do so. This response is returned to prevent a timeout
	// error from occurring in the master. The master can next issue a Poll Program
	// Complete message to determine if processing is completed.
	ExceptionAcknowledge
	// The slave is engaged in processing a long–duration program command.
	// The master should retransmit the message later when the slave is free.
	ExceptionDeviceBusy
	// The slave cannot perform the program function received in the query.
	// This code is returned for an unsuccessful programming request using function
	// code 13 or 14 decimal. The master should request diagnostic or error information from the slave
	ExceptionNegativeAcknowledge
	// The slave detected a parity error in the memory. The master can retry the request,
	// but service may be required on the slave device.
	ExceptionMemoryParityError
)

func (e Exception) Error() (s string) {
	switch e {
	case ExceptionIllegalFunction:
		s = "illegal function"
	case ExceptionIllegalDataAddr:
		s = "illegal data address"
	case ExceptionIllegalDataValue:
		s = "illegal data value"
	case ExceptionDeviceFailure:
		s = "device failure"
	case ExceptionAcknowledge:
		s = "acknowledge"
	case ExceptionDeviceBusy:
		s = "device busy"
	case ExceptionNegativeAcknowledge:
		s = "negative acknowledge"
	case ExceptionMemoryParityError:
		s = "memory parity error"
	case ExceptionNone:
		s = "none"
	default:
		s = "unknown modbus exception " + strconv.Itoa(int(e))
	}
	return s
}
