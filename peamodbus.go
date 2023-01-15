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

// StaticModel is a memory constrained DataModel implementation.
// A non-nil StaticModel is ready for use after being declared.
type StaticModel struct {
	HoldingRegisters [125]uint16
	InputRegisters   [125]uint16
	// There are 2000 coils which can be ON (1) or OFF (0).
	// We can then represent them as 2000/16=125 16 bit integers.
	coils [125]uint16
	// 2000 Discrete inputs, similar to coils.
	discreteInputs [125]uint16
}

func (dm *StaticModel) Read(dst []byte, fc FunctionCode, startAddress uint16) error {
	if fc != FCReadHoldingRegisters {
		return errors.New("unsupported read action")
	}
	if len(dst)%2 != 0 {
		return errors.New("uneven number of bytes in dst")
	}
	if startAddress+uint16(len(dst))/2 >= 125 {
		return errors.New("read request exceeds model's size")
	}
	ireg := startAddress
	for i := 0; i < len(dst); i += 2 {
		binary.BigEndian.PutUint16(dst[i:], dm.HoldingRegisters[ireg])
		ireg++
	}
	return nil
}

func (dm *StaticModel) Write(fc FunctionCode, startAddress uint16, data []byte) error {
	if fc != FCWriteMultipleRegisters {
		return errors.New("unsupported write action")
	}
	if len(data)%2 != 0 {
		return errors.New("uneven number of bytes in dst")
	}
	if startAddress+uint16(len(data))/2 >= 125 {
		return errors.New("read request exceeds model's size")
	}
	ireg := startAddress
	for i := 0; i < len(data); i += 2 {
		dm.HoldingRegisters[ireg] = binary.BigEndian.Uint16(data[i:])
		ireg++
	}
	return nil
}
