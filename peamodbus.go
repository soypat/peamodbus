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

type DataModel interface {
	// Read writes data to dst as per specified by fc, the start address and
	// the length of dst buffer. Coils and discrete inputs should be represented
	// as single bytes, either 1 or 0.
	Read(dst []byte, fc FunctionCode, startAddress uint16) error
	Write(fc FunctionCode, startAdress uint16, data []byte) error
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

func (dm *BlockedModel) Read(dst []byte, fc FunctionCode, startAddress uint16) error {
	shortSize := fc == FCReadHoldingRegisters || fc == FCReadInputRegisters
	switch {
	case !shortSize && fc != FCReadCoils && fc != FCReadDiscreteInputs:
		return errors.New("unsupported read action")
	case shortSize && len(dst)%2 != 0:
		return errors.New("uneven number of bytes in dst")
	case shortSize && startAddress+uint16(len(dst))/2 >= 125 || len(dst)+int(startAddress) > 2000:
		return errors.New("read request exceeds model's size")
	}

	ireg := startAddress
	stride := 1
	if shortSize {
		stride = 2
	}
	for i := 0; i < len(dst); i += stride {
		switch fc {
		case FCReadHoldingRegisters:
			binary.BigEndian.PutUint16(dst[i:], dm.HoldingRegisters[ireg])
		case FCReadInputRegisters:
			binary.BigEndian.PutUint16(dst[i:], dm.InputRegisters[ireg])
		case FCReadCoils:
			dst[i] = dm.GetCoil(int(ireg))
		case FCReadDiscreteInputs:
			dst[i] = dm.GetDiscreteInput(int(ireg))
		}
		ireg++
	}
	return nil
}

func (dm *BlockedModel) Write(fc FunctionCode, startAddress uint16, data []byte) error {
	shortSize := fc == FCWriteMultipleRegisters
	switch {
	case !shortSize && fc != FCWriteSingleCoil:
		return errors.New("unsupported write action")
	case shortSize && len(data)%2 != 0:
		return errors.New("uneven number of bytes in dst")
	case shortSize && startAddress+uint16(len(data))/2 >= 125 || len(data)+int(startAddress) > 2000:
		return errors.New("write request exceeds model's size")
	}
	stride := 1
	if shortSize {
		stride = 2
	}
	ireg := startAddress
	for i := 0; i < len(data); i += stride {
		switch fc {
		case FCWriteMultipleRegisters, FCWriteSingleRegister:
			dm.HoldingRegisters[ireg] = binary.BigEndian.Uint16(data[i:])
		case FCWriteSingleCoil:
			dm.SetCoil(int(ireg), data[ireg] != 0)
		}
		ireg++
		if fc == FCWriteSingleCoil || fc == FCWriteSingleRegister {
			break
		}
	}
	return nil
}

// GetCoil returns 1 if the coil at position i is set and 0 if it is not.
// Expects a position of equal or under 2000.
func (sm *BlockedModel) GetCoil(i int) byte {
	if i > 2000 {
		panic("coil exceeds limit")
	}
	idx, bit := i/16, i%16
	return b2u8(sm.coils[idx]&(1<<bit) != 0)
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
func (sm *BlockedModel) GetDiscreteInput(i int) byte {
	if i > 2000 {
		panic("discrete input exceeds limit")
	}
	idx, bit := i/16, i%16
	return b2u8(sm.discreteInputs[idx]&(1<<bit) != 0)
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
