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
	"errors"
	"sync"
)

// DataModel is the core abstraction of the register data in the modbus protocol.
// The definition of this interface allows for abstract
// representations of the Modbus data allowing for
// low memory representations of registers that are ideal for microcontrollers.
// An explicit representation of Modbus memory ([BlockedModel]) occupies a kilobyte,
// This could be reduced to just 2 bytes for a controller that just operates a 16bit sensor.
type DataModel interface {
	GetCoil(i int) bool
	SetCoil(i int, b bool)
	GetDiscreteInput(i int) bool
	SetDiscreteInput(i int, b bool)
	GetInputRegister(i int) uint16
	SetInputRegister(i int, value uint16)
	GetHoldingRegister(i int) uint16
	SetHoldingRegister(i int, value uint16)
}

// readFromModel reads data from the model into dst as per specified by fc, the start address and
// the length of dst buffer. It is a low level primitive that receives raw modbus PDU data.
func readFromModel(dst []byte, model DataModel, fc FunctionCode, startAddress, quantity uint16) error {
	bitSize := fc == FCReadCoils || fc == FCReadDiscreteInputs
	endAddress := startAddress + quantity - 1
	switch {
	case quantity == 0:
		return errors.New("quantity must be greater than 0")
	case !bitSize && fc != FCReadHoldingRegisters && fc != FCReadInputRegisters:
		return errors.New("unsupported ReadFromModel function code action")
	case !bitSize && len(dst)%2 != 0:
		return errors.New("uneven number of bytes in dst")
	case endAddress > 2000 || (!bitSize && endAddress >= 125):
		return errors.New("read request exceeds model's size")
	}
	for i := uint16(0); i < quantity; i++ {
		ireg := startAddress + i
		switch fc {
		case FCReadHoldingRegisters:
			binary.BigEndian.PutUint16(dst[2*i:], model.GetHoldingRegister(int(ireg)))
		case FCReadInputRegisters:
			binary.BigEndian.PutUint16(dst[2*i:], model.GetInputRegister(int(ireg)))
		case FCReadCoils:
			if model.GetCoil(int(ireg)) {
				dst[i/8] |= (1 << (i % 8)) // Set bit.
			} else {
				dst[i/8] &^= (1 << (i % 8)) // Unset bit.
			}
		case FCReadDiscreteInputs:
			if model.GetDiscreteInput(int(ireg)) {
				dst[i/8] |= (1 << (i % 8))
			} else {
				dst[i/8] &^= (1 << (i % 8))
			}
		}
		ireg++
	}
	return nil
}

// writeToModel implements the low level API for modifying the Object Model's data
// using PDU data obtained directly from a modbus transaction.
func writeToModel(model DataModel, fc FunctionCode, startAddress, quantity uint16, data []byte) error {
	bitSize := fc == FCWriteSingleCoil || fc == FCWriteMultipleCoils
	endAddress := startAddress + quantity
	switch {
	case !bitSize && fc != FCWriteSingleRegister && fc != FCWriteMultipleRegisters:
		return errors.New("unsupported WriteToModel function code action")
	case endAddress > 2000 || (!bitSize && endAddress >= 125):
		return errors.New("write request exceeds model's size")
	case quantity != 1 && (fc == FCWriteSingleCoil || fc == FCWriteSingleRegister):
		return errors.New("quantity must be 1 for reading single coil or single register")
	}

	for i := uint16(0); i < quantity; i++ {
		ireg := i + startAddress
		switch fc { // TODO(soypat): Benchmark to see if using an `if bitsize` is faster.
		case FCWriteMultipleRegisters, FCWriteSingleRegister:
			model.SetHoldingRegister(int(ireg), binary.BigEndian.Uint16(data[i*2:]))
		case FCWriteSingleCoil, FCWriteMultipleCoils:
			bit := data[i/8] & (1 << (i % 8))
			model.SetCoil(int(ireg), bit != 0)
		}
	}
	return nil
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

func (dm *BlockedModel) GetHoldingRegister(i int) uint16 {
	return dm.holdingRegisters[i]
}

func (dm *BlockedModel) SetHoldingRegister(i int, v uint16) {
	dm.holdingRegisters[i] = v
}

func (dm *BlockedModel) GetInputRegister(i int) uint16 {
	return dm.inputRegisters[i]
}

func (dm *BlockedModel) SetInputRegister(i int, v uint16) {
	dm.inputRegisters[i] = v
}

// GetCoil returns 1 if the coil at position i is set and 0 if it is not.
// Expects coil index in range 0..2000.
func (sm *BlockedModel) GetCoil(i int) bool {
	if i >= 2000 {
		panic("coil exceeds limit")
	}
	idx, bit := i/16, i%16
	return sm.coils[idx]&(1<<bit) != 0
}

// SetCoil sets the coil at position i to 1 if value is true and to 0 if value is false.
// Expects coil index in range 0..2000.
func (sm *BlockedModel) SetCoil(i int, value bool) {
	if i >= 2000 {
		panic("coil exceeds limit")
	}
	idx, bit := i/16, i%16
	if value {
		sm.coils[idx] |= (1 << bit)
	} else {
		sm.coils[idx] &^= (1 << bit)
	}
}

// GetDiscreteInput returns 1 if the discrete input at position i is set and 0 if it is not.
// Expects discrete input index in range 0..2000.
func (sm *BlockedModel) GetDiscreteInput(i int) bool {
	if i >= 2000 {
		panic("discrete input exceeds limit")
	}
	idx, bit := i/16, i%16
	return sm.discreteInputs[idx]&(1<<bit) != 0
}

// SetDiscreteInput sets the discrete input at position i to 1 if value is true and 0 if it is false.
// Expects discrete input index in range 0..2000.
func (sm *BlockedModel) SetDiscreteInput(i int, value bool) {
	if i >= 2000 {
		panic("discrete input exceeds limit")
	}
	idx, bit := i/16, i%16
	if value {
		sm.discreteInputs[idx] |= (1 << bit)
	} else {
		sm.discreteInputs[idx] &^= (1 << bit)
	}
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

func (dm *lockedDataModel) GetHoldingRegister(i int) uint16 {
	dm.mu.Lock()
	defer dm.mu.Unlock()
	return dm.dm.GetHoldingRegister(i)
}

func (dm *lockedDataModel) SetHoldingRegister(i int, v uint16) {
	dm.mu.Lock()
	defer dm.mu.Unlock()
	dm.dm.SetHoldingRegister(i, v)
}

func (dm *lockedDataModel) GetInputRegister(i int) uint16 {
	dm.mu.Lock()
	defer dm.mu.Unlock()
	return dm.dm.GetInputRegister(i)
}

func (dm *lockedDataModel) SetInputRegister(i int, v uint16) {
	dm.mu.Lock()
	defer dm.mu.Unlock()
	dm.dm.SetInputRegister(i, v)
}

func (dm *lockedDataModel) GetCoil(i int) bool {
	dm.mu.Lock()
	defer dm.mu.Unlock()
	return dm.dm.GetCoil(i)
}

func (dm *lockedDataModel) SetCoil(i int, v bool) {
	dm.mu.Lock()
	defer dm.mu.Unlock()
	dm.dm.SetCoil(i, v)
}

func (dm *lockedDataModel) GetDiscreteInput(i int) bool {
	dm.mu.Lock()
	defer dm.mu.Unlock()
	return dm.dm.GetDiscreteInput(i)
}

func (dm *lockedDataModel) SetDiscreteInput(i int, v bool) {
	dm.mu.Lock()
	defer dm.mu.Unlock()
	dm.dm.SetDiscreteInput(i, v)
}
