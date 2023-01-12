package peamodbus

type FunctionCode uint8

// Data access function codes.
const (
	FCReadCoils                   FunctionCode = 0x01
	FCReadDiscreteInputs          FunctionCode = 0x02
	FCReadHoldingRegister         FunctionCode = 0x03
	FCReadInputRegister           FunctionCode = 0x04
	FCWriteSingleCoil             FunctionCode = 0x05
	FCWriteSingleRegister         FunctionCode = 0x06
	FCWriteMultipleRegisters      FunctionCode = 0x10
	FCReadFileRecord              FunctionCode = 0x14
	FCWriteFileRecord             FunctionCode = 0x15
	FCMaskWriteRegister           FunctionCode = 0x16
	FCReadWriteMultipledRegisters FunctionCode = 0x17
	FCReadFIFOQueue               FunctionCode = 0x18
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
