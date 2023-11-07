/*
	package peamodbus implements the modbus protocol for TCP networks.

# Glossary

  - MBAP: Modbus Application Protocol. Used to differentiate between Modbus RTU
    which is lower level in character.
  - ADU: Application Data Unit. Encapsulates the PDU packet of data and some additional
    fields that are introduced by nature of the underlying network used.
  - PDU: Protocol Data Unit. Refers to packet of data with function code and data
    associated with said function. This is data relevant to the Modbus system.
  - MBAP Header: This refers to the segment of data that precedes the PDU in a
    Modbus request or response over TCP/IP.

# MBAP Header

	2 bytes | Transaction Identifier
	2 bytes | Protocol Identifier. Is 0 for Modbus protocol
	2 bytes | Length. Number of following bytes in PDU
	1 byte  | Unit identifier: Identifies a remote slave connected on a serial line or other buses

# Modbus Data Model

	Data table type   | Structure  | Access     | Comments
	Discrete Inputs   | Single bit | Read-only  | Data provided by an IO system
	Coils             | Single bit | Read/Write | Alterable by application program
	Input Registers   | 16bit word | Read-only  | Data provided by an IO system
	Holding Registers | 16bit word | Read/Write | Alterable by application program
*/
package peamodbus

import (
	"encoding/binary"
	"math"
	"sync"
)

// DataModel is the core abstraction of the register data in the modbus protocol.
// The definition of this interface allows for abstract
// representations of the Modbus data allowing for
// low memory representations of registers that are ideal for microcontrollers.
// An explicit representation of Modbus memory ([BlockedModel]) occupies a kilobyte,
// This could be reduced to just 2 bytes for a controller that just operates a 16bit sensor.
type DataModel interface {
	// GetCoil checks if coil at position i is set.
	GetCoil(i int) (bool, Exception)
	// SetCoil sets the coil at position i to b.
	SetCoil(i int, b bool) Exception

	// GetDiscreteInput checks if discrete input at position i is set.
	GetDiscreteInput(i int) (bool, Exception)
	// SetDiscreteInput sets the discrete input at position i to b.
	SetDiscreteInput(i int, b bool) Exception

	// GetInputRegister returns the 16-bit value of the input register at position i.
	GetInputRegister(i int) (uint16, Exception)
	// SetInputRegister sets the 16-bit value of the input register at position i.
	SetInputRegister(i int, value uint16) Exception

	// GetHoldingRegister returns the 16-bit value of the holding register at position i.
	GetHoldingRegister(i int) (uint16, Exception)
	// SetHoldingRegister sets the 16-bit value of the holding register at position i.
	SetHoldingRegister(i int, value uint16) Exception
}

// readFromModel reads data from the model into dst as per specified by fc, the start address and
// the length of dst buffer. It is a low level primitive that receives raw modbus PDU data.
func readFromModel(dst []byte, model DataModel, fc FunctionCode, startAddress, quantity uint16) (exc Exception) {
	bitSize := fc == FCReadCoils || fc == FCReadDiscreteInputs
	endAddress := startAddress + quantity - 1
	switch {
	case quantity == 0:
		exc = ExceptionIllegalDataValue
	case !bitSize && fc != FCReadHoldingRegisters && fc != FCReadInputRegisters:
		exc = ExceptionIllegalFunction
	case !bitSize && len(dst)%2 != 0:
		exc = ExceptionIllegalDataValue
	case endAddress > 2000 || (!bitSize && endAddress >= 125):
		exc = ExceptionIllegalDataAddr
	}
	model, unlock := lockDataModel(model)
	defer unlock()

	var gotu16 uint16
	var gotb bool
	for i := uint16(0); exc == ExceptionNone && i < quantity; i++ {
		ireg := startAddress + i
		switch fc {
		case FCReadHoldingRegisters:
			gotu16, exc = model.GetHoldingRegister(int(ireg))
			if exc != ExceptionNone {
				break
			}
			binary.BigEndian.PutUint16(dst[2*i:], gotu16)
		case FCReadInputRegisters:
			gotu16, exc = model.GetInputRegister(int(ireg))
			if exc != ExceptionNone {
				break
			}
			binary.BigEndian.PutUint16(dst[2*i:], gotu16)
		case FCReadCoils:
			gotb, exc = model.GetCoil(int(ireg))
			if exc != ExceptionNone {
				break
			}
			if gotb {
				dst[i/8] |= (1 << (i % 8)) // Set bit.
			} else {
				dst[i/8] &^= (1 << (i % 8)) // Unset bit.
			}
		case FCReadDiscreteInputs:
			gotb, exc = model.GetDiscreteInput(int(ireg))
			if exc != ExceptionNone {
				break
			}
			if gotb {
				dst[i/8] |= (1 << (i % 8))
			} else {
				dst[i/8] &^= (1 << (i % 8))
			}
		}
	}
	return exc
}

// writeToModel implements the low level API for modifying the Object Model's data
// using PDU data obtained directly from a modbus transaction.
func writeToModel(model DataModel, fc FunctionCode, startAddress, quantity uint16, data []byte) (exc Exception) {
	bitSize := fc == FCWriteSingleCoil || fc == FCWriteMultipleCoils
	endAddress := startAddress + quantity
	switch {
	case !bitSize && fc != FCWriteSingleRegister && fc != FCWriteMultipleRegisters:
		exc = ExceptionIllegalFunction
	case endAddress > 2000 || (!bitSize && endAddress >= 125):
		exc = ExceptionIllegalDataAddr
	case quantity != 1 && (fc == FCWriteSingleCoil || fc == FCWriteSingleRegister):
		exc = ExceptionIllegalDataValue
	}
	model, unlock := lockDataModel(model)
	defer unlock()

	for i := uint16(0); exc == ExceptionNone && i < quantity; i++ {
		ireg := i + startAddress
		switch fc { // TODO(soypat): Benchmark to see if using an `if bitsize` is faster.
		case FCWriteMultipleRegisters, FCWriteSingleRegister:
			exc = model.SetHoldingRegister(int(ireg), binary.BigEndian.Uint16(data[i*2:]))
		case FCWriteSingleCoil, FCWriteMultipleCoils:
			bit := data[i/8] & (1 << (i % 8))
			exc = model.SetCoil(int(ireg), bit != 0)
		}
	}
	return exc
}

// BlockedModel is a memory constrained DataModel implementation.
// Each data type (coil, registers) occupies its own block/segment of memory.
// A non-nil BlockedModel is ready for use after being declared.
type BlockedModel struct {
	// There are 2000 coils which can be ON (1) or OFF (0).
	// We can then represent them as 2000/16=125 16 bit integers.
	coils [125]uint16
	// 2000 Discrete inputs, similar to coils.
	discreteInputs   [125]uint16
	inputRegisters   [125]uint16
	holdingRegisters [125]uint16
}

var _ DataModel = &BlockedModel{}

func (dm *BlockedModel) GetHoldingRegister(i int) (uint16, Exception) {
	if i < 0 || i >= 125 {
		return 0, ExceptionIllegalDataAddr
	}
	return dm.holdingRegisters[i], ExceptionNone
}

func (dm *BlockedModel) SetHoldingRegister(i int, v uint16) Exception {
	if i < 0 || i >= 125 {
		return ExceptionIllegalDataAddr
	}
	dm.holdingRegisters[i] = v
	return ExceptionNone
}

func (dm *BlockedModel) GetInputRegister(i int) (uint16, Exception) {
	if i < 0 || i >= 125 {
		return 0, ExceptionIllegalDataAddr
	}
	return dm.inputRegisters[i], ExceptionNone
}

func (dm *BlockedModel) SetInputRegister(i int, v uint16) Exception {
	if i < 0 || i >= 125 {
		return ExceptionIllegalDataAddr
	}
	dm.inputRegisters[i] = v
	return ExceptionNone
}

// GetCoil returns 1 if the coil at position i is set and 0 if it is not.
// Expects coil index in range 0..1999.
func (sm *BlockedModel) GetCoil(i int) (bool, Exception) {
	if i < 0 || i >= 2000 {
		return false, ExceptionIllegalDataAddr
	}
	idx, bit := i/16, i%16
	return sm.coils[idx]&(1<<bit) != 0, ExceptionNone
}

// SetCoil sets the coil at position i to 1 if value is true and to 0 if value is false.
// Expects coil index in range 0..1999.
func (sm *BlockedModel) SetCoil(i int, value bool) Exception {
	if i < 0 || i >= 2000 {
		return ExceptionIllegalDataAddr
	}
	idx, bit := i/16, i%16
	if value {
		sm.coils[idx] |= (1 << bit)
	} else {
		sm.coils[idx] &^= (1 << bit)
	}
	return ExceptionNone
}

// GetDiscreteInput returns 1 if the discrete input at position i is set and 0 if it is not.
// Expects discrete input index in range 0..1999.
func (sm *BlockedModel) GetDiscreteInput(i int) (bool, Exception) {
	if i < 0 || i >= 2000 {
		return false, ExceptionIllegalDataAddr
	}
	idx, bit := i/16, i%16
	return sm.discreteInputs[idx]&(1<<bit) != 0, ExceptionNone
}

// SetDiscreteInput sets the discrete input at position i to 1 if value is true and 0 if it is false.
// Expects discrete input index in range 0..1999.
func (sm *BlockedModel) SetDiscreteInput(i int, value bool) Exception {
	if i < 0 || i >= 2000 {
		return ExceptionIllegalDataAddr
	}
	idx, bit := i/16, i%16
	if value {
		sm.discreteInputs[idx] |= (1 << bit)
	} else {
		sm.discreteInputs[idx] &^= (1 << bit)
	}
	return ExceptionNone
}

// ConcurrencySafeDataModel returns a DataModel that is safe for concurrent use
// from a existing datamodel.
func ConcurrencySafeDataModel(dm DataModel) DataModel {
	if dm == nil {
		panic("nil DataModel")
	}
	return &lockedDataModel{
		dm: dm,
	}
}

type lockedDataModel struct {
	mu sync.Mutex
	dm DataModel
}

func (dm *lockedDataModel) GetHoldingRegister(i int) (uint16, Exception) {
	dm.mu.Lock()
	defer dm.mu.Unlock()
	return dm.dm.GetHoldingRegister(i)
}

func (dm *lockedDataModel) SetHoldingRegister(i int, v uint16) Exception {
	dm.mu.Lock()
	defer dm.mu.Unlock()
	return dm.dm.SetHoldingRegister(i, v)
}

func (dm *lockedDataModel) GetInputRegister(i int) (uint16, Exception) {
	dm.mu.Lock()
	defer dm.mu.Unlock()
	return dm.dm.GetInputRegister(i)
}

func (dm *lockedDataModel) SetInputRegister(i int, v uint16) Exception {
	dm.mu.Lock()
	defer dm.mu.Unlock()
	return dm.dm.SetInputRegister(i, v)
}

func (dm *lockedDataModel) GetCoil(i int) (bool, Exception) {
	dm.mu.Lock()
	defer dm.mu.Unlock()
	return dm.dm.GetCoil(i)
}

func (dm *lockedDataModel) SetCoil(i int, v bool) Exception {
	dm.mu.Lock()
	defer dm.mu.Unlock()
	return dm.dm.SetCoil(i, v)
}

func (dm *lockedDataModel) GetDiscreteInput(i int) (bool, Exception) {
	dm.mu.Lock()
	defer dm.mu.Unlock()
	return dm.dm.GetDiscreteInput(i)
}

func (dm *lockedDataModel) SetDiscreteInput(i int, v bool) Exception {
	dm.mu.Lock()
	defer dm.mu.Unlock()
	return dm.dm.SetDiscreteInput(i, v)
}

func NewDataInterpreter(cfg DataInterpreterConfig) (*DataInterpreter, error) {
	di := &DataInterpreter{
		badFloat: float32(cfg.BadFloat),
		mswFirst: cfg.MostSignificantWordFirst,
	}
	return di, nil
}

type DataInterpreterConfig struct {
	// BadFloat is the value to return when a float conversion fails.
	// Ideally the exception should be checked and the value ignored.
	BadFloat float64
	// MostSignificantWordFirst can be set to interpret contiguous modbus 16bit register data
	// as having the first 16bit register be the most significant word (MSW).
	// If not set the first 16bit register is the least significant word (LSW).
	MostSignificantWordFirst bool
}

// DataInterpreter provides primitive data type reading and writing from and to
// modbus [DataModel]s.
type DataInterpreter struct {
	badFloat float32
	mswFirst bool
}

// putUintReg is the basic building block for putting large integers into consecutive 16bit registers of a data model.
func (interpret *DataInterpreter) putUintReg(regSet func(int, uint16) Exception, dm DataModel, startIdx, sizeWords int, value uint64) (_ Exception) {
	if sizeWords < 2 || sizeWords > 4 || regSet == nil {
		panic("bad sizeWords/nil func") // Internal bug.
	}
	if interpret.mswFirst {
		for i := 0; i < sizeWords; i++ {
			shift := (sizeWords - i - 1) * 16
			exc := regSet(startIdx+i, uint16(value>>shift))
			if exc != 0 {
				return exc
			}
		}
	} else {
		for i := 0; i < sizeWords; i++ {
			shift := i * 16
			exc := regSet(startIdx+i, uint16(value>>shift))
			if exc != 0 {
				return exc
			}
		}
	}
	return ExceptionNone
}

// uintReg is the basic building block for interpreting consecutive 16bit registers.
func (interpret *DataInterpreter) uintReg(regGet func(int) (uint16, Exception), dm DataModel, startIdx, sizeWords int) (v uint64, _ Exception) {
	if sizeWords < 2 || sizeWords > 4 || regGet == nil {
		panic("bad sizeWords/nil func") // Internal bug.
	}
	if interpret.mswFirst {
		for i := 0; i < sizeWords; i++ {
			u16, exc := regGet(startIdx + i)
			if exc != ExceptionNone {
				return 0, exc
			}

			shift := (sizeWords - i - 1) * 16
			v |= uint64(u16) << shift
		}
	} else {
		for i := 0; i < sizeWords; i++ {
			u16, exc := regGet(startIdx + i)
			if exc != ExceptionNone {
				return 0, exc
			}

			shift := i * 16
			v |= uint64(u16) << shift
		}
	}
	return v, ExceptionNone
}

// Int16Holding interprets the holding register at i as a signed 16 bit integer.
func (interpret *DataInterpreter) Int16Holding(dm DataModel, i int) (int16, Exception) {
	i16, exc := dm.GetHoldingRegister(i)
	if exc != ExceptionNone {
		return 0, exc
	}
	return int16(i16), ExceptionNone
}

// Uint32Holding interprets consecutive 32bit holding register data at startIdx as a unsigned integer.
func (interpret *DataInterpreter) Uint32Holding(dm DataModel, startIdx int) (v uint32, _ Exception) {
	dm, unlock := lockDataModel(dm)
	defer unlock()
	v64, exc := interpret.uintReg(dm.GetHoldingRegister, dm, startIdx, 2)
	return uint32(v64), exc
}

// Uint64Holding interprets consecutive 64bit holding register data at startIdx as a unsigned integer.
func (interpret *DataInterpreter) Uint64Holding(dm DataModel, startIdx int) (v uint64, _ Exception) {
	dm, unlock := lockDataModel(dm)
	defer unlock()
	return interpret.uintReg(dm.GetHoldingRegister, dm, startIdx, 4)
}

// Int32Holding interprets consecutive 32bit holding register data at startIdx as a signed integer.
func (interpret *DataInterpreter) Int32Holding(dm DataModel, startIdx int) (int32, Exception) {
	u32, exc := interpret.Uint32Holding(dm, startIdx)
	return int32(u32), exc
}

// Float32Holding interprets consecutive 32bit holding register data at startIdx as a floating point number.
func (interpret *DataInterpreter) Float32Holding(dm DataModel, startIdx int) (float32, Exception) {
	f32, exc := interpret.Uint32Holding(dm, startIdx)
	if exc != ExceptionNone {
		return interpret.badFloat, exc
	}
	return math.Float32frombits(f32), ExceptionNone
}

// Int64Holding interprets consecutive 64bit holding register data at startIdx as a signed integer.
func (interpret *DataInterpreter) Int64Holding(dm DataModel, startIdx int) (int64, Exception) {
	u64, exc := interpret.Uint64Holding(dm, startIdx)
	return int64(u64), exc
}

// Float64Holding interprets consecutive 64bit holding register data at startIdx as a floating point number.
func (interpret *DataInterpreter) Float64Holding(dm DataModel, startIdx int) (float64, Exception) {
	f64, exc := interpret.Uint64Holding(dm, startIdx)
	if exc != ExceptionNone {
		return float64(interpret.badFloat), exc
	}
	return math.Float64frombits(f64), ExceptionNone
}

//
// Begin Interpreter Input functions.
//

// Int16Input interprets the input register at i as a signed 16 bit integer.
func (interpret *DataInterpreter) Int16Input(dm DataModel, i int) (int16, Exception) {
	u16, exc := dm.GetInputRegister(i)
	if exc != ExceptionNone {
		return 0, exc
	}
	return int16(u16), ExceptionNone
}

// Uint32Input interprets consecutive 32bit input register data at startIdx as a unsigned integer.
func (interpret *DataInterpreter) Uint32Input(dm DataModel, startIdx int) (v uint32, _ Exception) {
	dm, unlock := lockDataModel(dm)
	defer unlock()
	v64, exc := interpret.uintReg(dm.GetInputRegister, dm, startIdx, 2)
	return uint32(v64), exc
}

// Uint64Input interprets consecutive 64bit input register data at startIdx as a unsigned integer.
func (interpret *DataInterpreter) Uint64Input(dm DataModel, startIdx int) (v uint64, _ Exception) {
	dm, unlock := lockDataModel(dm)
	defer unlock()
	return interpret.uintReg(dm.GetInputRegister, dm, startIdx, 4)
}

// Int32Input interprets consecutive 32bit input register data at startIdx as a signed integer.
func (interpret *DataInterpreter) Int32Input(dm DataModel, startIdx int) (int32, Exception) {
	i32, exc := interpret.Uint32Input(dm, startIdx)
	return int32(i32), exc
}

// Float32Input interprets consecutive 32bit input register data at startIdx as a floating point number.
func (interpret *DataInterpreter) Float32Input(dm DataModel, startIdx int) (float32, Exception) {
	f32, exc := interpret.Uint32Input(dm, startIdx)
	if exc != ExceptionNone {
		return interpret.badFloat, exc
	}
	return math.Float32frombits(f32), ExceptionNone
}

// Int64Input interprets consecutive 64bit input register data at startIdx as a signed integer.
func (interpret *DataInterpreter) Int64Input(dm DataModel, startIdx int) (int64, Exception) {
	u64, exc := interpret.Uint64Input(dm, startIdx)
	return int64(u64), exc
}

// Float64Input interprets consecutive 64bit input register data at startIdx as a floating point number.
func (interpret *DataInterpreter) Float64Input(dm DataModel, startIdx int) (float64, Exception) {
	f64, exc := interpret.Uint64Input(dm, startIdx)
	if exc != ExceptionNone {
		return float64(interpret.badFloat), exc
	}
	return math.Float64frombits(f64), ExceptionNone
}

//
// Begin Put/set functions.
//

// PutUint64Holding stores a 64bit unsigned integer into 4 holding registers at startIdx.
func (interpret *DataInterpreter) PutUint64Holding(dm DataModel, startIdx int, v uint64) Exception {
	dm, unlock := lockDataModel(dm)
	defer unlock()
	return interpret.putUintReg(dm.SetHoldingRegister, dm, startIdx, 4, v)
}

// PutUint32Holding stores a 32bit unsigned integer into 2 holding registers at startIdx.
func (interpret *DataInterpreter) PutUint32Holding(dm DataModel, startIdx int, v uint32) Exception {
	dm, unlock := lockDataModel(dm)
	defer unlock()
	return interpret.putUintReg(dm.SetHoldingRegister, dm, startIdx, 2, uint64(v))
}

// PutFloat32Holding stores a 32bit floating point number into 2 holding registers at startIdx.
func (interpret *DataInterpreter) PutFloat32Holding(dm DataModel, startIdx int, v float32) Exception {
	return interpret.PutUint32Holding(dm, startIdx, math.Float32bits(v))
}

// PutFloat64Holding stores a 64bit floating point number into 4 holding registers at startIdx.
func (interpret *DataInterpreter) PutFloat64Holding(dm DataModel, startIdx int, v float64) Exception {
	return interpret.PutUint64Holding(dm, startIdx, math.Float64bits(v))
}

// PutUint64Input stores a 64bit unsigned integer into 4 input registers at startIdx.
func (interpret *DataInterpreter) PutUint64Input(dm DataModel, startIdx int, v uint64) Exception {
	dm, unlock := lockDataModel(dm)
	defer unlock()
	return interpret.putUintReg(dm.SetInputRegister, dm, startIdx, 4, v)
}

// PutUint32Input stores a 32bit unsigned integer into 2 input registers at startIdx.
func (interpret *DataInterpreter) PutUint32Input(dm DataModel, startIdx int, v uint32) Exception {
	dm, unlock := lockDataModel(dm)
	defer unlock()
	return interpret.putUintReg(dm.SetInputRegister, dm, startIdx, 2, uint64(v))
}

// PutFloat32Input stores a 32bit floating point number into 2 input registers at startIdx.
func (interpret *DataInterpreter) PutFloat32Input(dm DataModel, startIdx int, v float32) Exception {
	return interpret.PutUint32Input(dm, startIdx, math.Float32bits(v))
}

// PutFloat64Input stores a 64bit floating point number into 4 input registers at startIdx.
func (interpret *DataInterpreter) PutFloat64Input(dm DataModel, startIdx int, v float64) Exception {
	dm, unlock := lockDataModel(dm)
	defer unlock()
	return interpret.PutUint64Input(dm, startIdx, math.Float64bits(v))
}

// ReadBytesInput reads data from the input registers of the data model into dst starting at startIdx.
func (interpret *DataInterpreter) ReadBytesInput(dm DataModel, dst []byte, startIdx int) (int, Exception) {
	dm, unlock := lockDataModel(dm)
	defer unlock()
	return interpret.readBytes(dst, startIdx, dm.GetInputRegister)
}

// ReadBytesHolding reads data from the holding registers of the data model into dst starting at startIdx.
func (interpret *DataInterpreter) ReadBytesHolding(dm DataModel, dst []byte, startIdx int) (int, Exception) {
	dm, unlock := lockDataModel(dm)
	defer unlock()
	return interpret.readBytes(dst, startIdx, dm.GetHoldingRegister)
}

// WriteBytesInput writes the data byte slice into the input registers of the data model starting at startIdx.
func (interpret *DataInterpreter) WriteBytesInput(dm DataModel, data []byte, startIdx int) (int, Exception) {
	dm, unlock := lockDataModel(dm)
	defer unlock()
	return interpret.writeBytes(data, startIdx, dm.SetInputRegister, dm.GetInputRegister)
}

// WriteBytesHolding writes the data byte slice into the holding registers of the data model starting at startIdx.
func (interpret *DataInterpreter) WriteBytesHolding(dm DataModel, data []byte, startIdx int) (int, Exception) {
	dm, unlock := lockDataModel(dm)
	defer unlock()
	return interpret.writeBytes(data, startIdx, dm.SetHoldingRegister, dm.GetHoldingRegister)
}

func (interpret *DataInterpreter) readBytes(dst []byte, startIdx int, get16 func(int) (uint16, Exception)) (int, Exception) {
	n := len(dst) / 2
	for i := 0; i < n; i++ {
		u16, exc := get16(i + startIdx)
		if exc != ExceptionNone {
			return i, exc
		}
		binary.BigEndian.PutUint16(dst[i*2:], u16)
	}
	if len(dst)%2 != 0 {
		u16, exc := get16(n + startIdx)
		if exc != ExceptionNone {
			return n, exc
		}
		dst[len(dst)-1] = byte(u16 >> 8) // Big endian.
	}
	return len(dst), ExceptionNone
}

func (interpret *DataInterpreter) writeBytes(data []byte, startIdx int, set16 func(idx int, v uint16) Exception, get16 func(int) (uint16, Exception)) (int, Exception) {
	n := len(data) / 2
	for i := 0; i < n; i++ {
		v := binary.BigEndian.Uint16(data[i*2:])
		exc := set16(i+startIdx, v)
		if exc != ExceptionNone {
			return i, exc
		}
	}
	if len(data)%2 != 0 {
		u16, exc := get16(n + startIdx)
		if exc != ExceptionNone {
			return n, exc
		}
		u16 = u16&0xff | uint16(data[len(data)-1])<<8
		exc = set16(n+startIdx, u16)
		if exc != ExceptionNone {
			return n, exc
		}
	}
	return len(data), ExceptionNone
}

// ReadUint16Input reads data from the input registers of the data model into dst starting at startIdx.
func (interpret *DataInterpreter) ReadUint16Input(dm DataModel, dst []uint16, startIdx int) (int, Exception) {
	dm, unlock := lockDataModel(dm)
	defer unlock()
	return interpret.read16(dst, startIdx, dm.GetInputRegister)
}

// ReadUint16Holding reads data from the holding registers of the data model into dst starting at startIdx.
func (interpret *DataInterpreter) ReadUint16Holding(dm DataModel, dst []uint16, startIdx int) (int, Exception) {
	dm, unlock := lockDataModel(dm)
	defer unlock()
	return interpret.read16(dst, startIdx, dm.GetHoldingRegister)
}

// WriteUint16Input writes the 16bit data slice into the input registers of the data model starting at startIdx.
func (interpret *DataInterpreter) WriteUint16Input(dm DataModel, data []uint16, startIdx int) (int, Exception) {
	dm, unlock := lockDataModel(dm)
	defer unlock()
	return interpret.write16(data, startIdx, dm.SetInputRegister)
}

// WriteUint16Holding writes the 16bit data slice into the holding registers of the data model starting at startIdx.
func (interpret *DataInterpreter) WriteUint16Holding(dm DataModel, data []uint16, startIdx int) (int, Exception) {
	dm, unlock := lockDataModel(dm)
	defer unlock()
	return interpret.write16(data, startIdx, dm.SetHoldingRegister)
}

func (interpret *DataInterpreter) read16(dst []uint16, startIdx int, get16 func(int) (uint16, Exception)) (int, Exception) {
	for i := 0; i < len(dst); i++ {
		u16, exc := get16(i + startIdx)
		if exc != ExceptionNone {
			return i, exc
		}
		dst[i] = u16
	}
	return len(dst), ExceptionNone
}

func (interpret *DataInterpreter) write16(data []uint16, startIdx int, set16 func(idx int, v uint16) Exception) (int, Exception) {
	for i := 0; i < len(data); i++ {
		exc := set16(i+startIdx, data[i])
		if exc != ExceptionNone {
			return i, exc
		}
	}
	return len(data), ExceptionNone
}

// lockDataModel is a convenience function for when working with a DataModel interface
// that is of underlying type *lockedDataModel. It returns the underlying DataModel
// in said case and locks the data model returning the unlock function to be called
// when done.
//
// If the DataModel is not of type *lockedDataModel, it returns the DataModel as is
// and a no-op unlock function. Below is a safe usage example:
//
//	model, unlock := lockDataModel(model)
//	defer unlock()
//	// Do stuff with model within confines of function.
func lockDataModel(dm DataModel) (unlocked DataModel, unlock func()) {
	if ldm, ok := dm.(*lockedDataModel); ok {
		ldm.mu.Lock()
		return ldm.dm, ldm.mu.Unlock
	}
	return dm, func() {} // Unknown data model, just use as is and unlock is no-op.
}
