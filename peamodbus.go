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
)

// DataModel is the core abstraction of the Modbus model.
// The definition of this interface allows for abstract
// representations of the Modbus data allowing for
// low memory representations of registers that are ideal for microcontrollers.
// An explicit representation of Modbus memory ([BlockedModel]) occupies a kilobyte,
// This could be reduced to just 2 bytes for a controller that just operates a 16bit sensor.
type DataModel interface {
	// Read writes data to dst as per specified by fc, the start address and
	// the length of dst buffer.
	Read(dst []byte, fc FunctionCode, startAddress, quantity uint16) error
	Write(fc FunctionCode, startAdress, quantity uint16, data []byte) error
}

// BlockedModel is a memory constrained DataModel implementation.
// Each data type (coil, registers) occupies its own block/segment of memory.
// A non-nil BlockedModel is ready for use after being declared.
type BlockedModel struct {
	HoldingRegisters [125]uint16
	InputRegisters   [125]uint16
	// There are 2000 coils which can be ON (1) or OFF (0).
	// We can then represent them as 2000/16=125 16 bit integers.
	coils [125]uint16
	// 2000 Discrete inputs, similar to coils.
	discreteInputs [125]uint16
}

func (dm *BlockedModel) Read(dst []byte, fc FunctionCode, startAddress, quantity uint16) error {
	bitSize := fc == FCReadCoils || fc == FCReadDiscreteInputs
	endAddress := startAddress + quantity
	switch {
	case !bitSize && fc != FCReadHoldingRegisters && fc != FCReadInputRegisters:
		return errors.New("unsupported read action")
	case !bitSize && len(dst)%2 != 0:
		return errors.New("uneven number of bytes in dst")
	case endAddress > 2000 || (!bitSize && endAddress >= 125):
		return errors.New("read request exceeds model's size")
	}

	for i := uint16(0); i < quantity; i++ {
		ireg := startAddress + i
		switch fc {
		case FCReadHoldingRegisters:
			binary.BigEndian.PutUint16(dst[2*i:], dm.HoldingRegisters[ireg])
		case FCReadInputRegisters:
			binary.BigEndian.PutUint16(dst[2*i:], dm.InputRegisters[ireg])
		case FCReadCoils:
			if dm.GetCoil(int(ireg)) {
				dst[i/8] |= (1 << (i % 8)) // Set bit.
			} else {
				dst[i/8] &^= (1 << (i % 8)) // Unset bit.
			}
		case FCReadDiscreteInputs:
			if dm.GetDiscreteInput(int(ireg)) {
				dst[i/8] |= (1 << (i % 8))
			} else {
				dst[i/8] &^= (1 << (i % 8))
			}
		}
		ireg++
	}
	return nil
}

func (dm *BlockedModel) Write(fc FunctionCode, startAddress, quantity uint16, data []byte) error {
	bitSize := fc == FCWriteSingleCoil || fc == FCWriteMultipleCoils
	endAddress := startAddress + quantity
	switch {
	case !bitSize && fc != FCWriteSingleRegister && fc != FCWriteMultipleRegisters:
		return errors.New("unsupported write action")
	case endAddress > 2000 || (!bitSize && endAddress >= 125):
		return errors.New("write request exceeds model's size")
	case quantity != 1 && (fc == FCWriteSingleCoil || fc == FCWriteSingleRegister):
		return errors.New("quantity must be 1 for reading single coil or single register")
	}

	for i := uint16(0); i < quantity; i++ {
		ireg := i + startAddress
		switch fc {
		case FCWriteMultipleRegisters, FCWriteSingleRegister:
			dm.HoldingRegisters[ireg] = binary.BigEndian.Uint16(data[i*2:])
		case FCWriteSingleCoil, FCWriteMultipleCoils:
			bit := data[i/8] & (1 << (i % 8))
			dm.SetCoil(int(ireg), bit != 0)
		}
	}
	return nil
}

// GetCoil returns 1 if the coil at position i is set and 0 if it is not.
// Expects a position of equal or under 2000.
func (sm *BlockedModel) GetCoil(i int) bool {
	if i > 2000 {
		panic("coil exceeds limit")
	}
	idx, bit := i/16, i%16
	return sm.coils[idx]&(1<<bit) != 0
}

func (sm *BlockedModel) SetCoil(i int, value bool) {
	if i > 2000 {
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
// Expects a position of equal or under 2000.
func (sm *BlockedModel) GetDiscreteInput(i int) bool {
	if i > 2000 {
		panic("discrete input exceeds limit")
	}
	idx, bit := i/16, i%16
	return sm.discreteInputs[idx]&(1<<bit) != 0
}

func (sm *BlockedModel) SetDiscreteInput(i int, value bool) {
	if i > 2000 {
		panic("discrete input exceeds limit")
	}
	idx, bit := i/16, i%16
	if value {
		sm.discreteInputs[idx] |= (1 << bit)
	} else {
		sm.discreteInputs[idx] &^= (1 << bit)
	}
}
