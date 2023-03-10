package peamodbus

type FunctionCode uint8

// Data access function codes.
const (
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
