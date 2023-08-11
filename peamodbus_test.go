package peamodbus

import (
	"testing"
)

func TestReadHoldingRegisters_loopback(t *testing.T) {
	model := &BlockedModel{}
	model.SetHoldingRegister(0, 0)
	var reqBuf, responseBuf [256]byte
	tx := &Tx{}
	// Test request packet.
	const (
		startAddr = 0
		quantity  = 3
	)
	n, err := tx.RequestReadHoldingRegisters(reqBuf[:], startAddr, quantity)
	if err != nil {
		t.Fatal(err)
	}
	if n != 5 {
		t.Fatal("expected 5 bytes, got", n)
	}
	fc, n16, err := InferRequestPacketLength(reqBuf[:])
	if err != nil {
		t.Fatal(err)
	}
	if fc != FCReadHoldingRegisters || n16 != 5 {
		t.Fatal("expected function code 3, length 5, got", fc, n16)
	}
	req, dataoff, err := DecodeRequest(reqBuf[:n])
	if err != nil {
		t.Fatal(err)
	}
	respOffset, err := req.PutResponse(model, responseBuf[:], reqBuf[dataoff:])
	if err != nil {
		t.Fatal(err)
	}
	fc, n16, err = InferResponsePacketLength(responseBuf[:])
	if err != nil {
		t.Fatal(err)
	}
	expectedLen := uint16(2 + 2*quantity)
	if fc != FCReadHoldingRegisters || n16 != expectedLen || respOffset != int(expectedLen) {
		t.Fatal("expected function code 3, length 2+2*n", expectedLen, "got:", fc, n16)
	}
}

func TestInferTxResponseLength(t *testing.T) {
	t.Parallel()
	var tx Tx
	var buf, valueBuf [256]byte
	var values16 [125]uint16
	_ = values16
	// Discrete+Coils
	for startAddr := uint16(0); startAddr < 100; startAddr++ {
		// RESPONSE WRITE SINGLE COIL
		n, err := tx.ResponseWriteSingleCoil(buf[:], startAddr, true)
		if err != nil {
			t.Fatal(err)
		}
		fc, n16, err := InferResponsePacketLength(buf[:])
		if err != nil {
			t.Fatal(err)
		}
		if fc != FCWriteSingleCoil || int(n16) != n {
			t.Fatal("expected function code 5, length", n, "got", fc, n16)
		}

		for quantity := uint16(1); quantity < 125; quantity++ {
			// RESPONSE READ COILS
			n, err := tx.ResponseReadCoils(buf[:], valueBuf[:quantity])
			if err != nil {
				t.Fatal(err)
			}
			fc, n16, err := InferResponsePacketLength(buf[:])
			if err != nil {
				t.Fatal(err)
			}
			if fc != FCReadCoils || int(n16) != n {
				t.Fatal("expected function code 1, length", n, "got", fc, n16)
			}

			// RESPONSE READ DISCRETE INPUTS
			n, err = tx.ResponseReadDiscreteInputs(buf[:], valueBuf[:quantity])
			if err != nil {
				t.Fatal(err)
			}
			fc, n16, err = InferResponsePacketLength(buf[:])
			if err != nil {
				t.Fatal(err)
			}
			if fc != FCReadDiscreteInputs || int(n16) != n {
				t.Fatal("expected function code 2, length", n, "got", fc, n16)
			}

			// RESPONSE WRITE MULTIPLE COILS
			n, err = tx.ResponseWriteMultipleCoils(buf[:], startAddr, quantity)
			if err != nil {
				t.Fatal(err)
			}
			fc, n16, err = InferResponsePacketLength(buf[:])
			if err != nil {
				t.Fatal(err)
			}
			if fc != FCWriteMultipleCoils || int(n16) != n {
				t.Fatal("expected function code 15, length", n, "got", fc, n16)
			}
		}
	}

	for startAddr := uint16(0); startAddr < 100; startAddr++ {
		// RESPONSE WRITE SINGLE REGISTER
		n, err := tx.ResponseWriteSingleRegister(buf[:], startAddr, 0)
		if err != nil {
			t.Fatal(err)
		}
		fc, n16, err := InferResponsePacketLength(buf[:])
		if err != nil {
			t.Fatal(err)
		}
		if fc != FCWriteSingleRegister || int(n16) != n {
			t.Fatal("expected function code 6, length", n, "got", fc, n16)
		}
		for quantity := uint16(1); quantity < 123; quantity++ {
			// RESPONSE READ HOLDING REGISTERS
			n, err = tx.ResponseReadHoldingRegisters(buf[:], valueBuf[:quantity*2])
			if err != nil {
				t.Fatal(err)
			}
			fc, n16, err := InferResponsePacketLength(buf[:])
			if err != nil {
				t.Fatal(err)
			}
			if fc != FCReadHoldingRegisters || int(n16) != n {
				t.Fatal("expected function code 3, length", n, "got", fc, n16)
			}

			// RESPONSE READ INPUT REGISTERS
			n, err = tx.ResponseReadInputRegisters(buf[:], valueBuf[:quantity*2])
			if err != nil {
				t.Fatal(err)
			}
			fc, n16, err = InferResponsePacketLength(buf[:])
			if err != nil {
				t.Fatal(err)
			}
			if fc != FCReadInputRegisters || int(n16) != n {
				t.Fatal("expected function code 4, length", n, "got", fc, n16)
			}

			// RESPONSE WRITE MULTIPLE REGISTERS
			n, err = tx.ResponseWriteMultipleRegisters(buf[:], startAddr, quantity)
			if err != nil {
				t.Fatal(err)
			}
			fc, n16, err = InferResponsePacketLength(buf[:])
			if err != nil {
				t.Fatal(err)
			}
			if fc != FCWriteMultipleRegisters || int(n16) != n {
				t.Fatal("expected function code 16, length", n, "got", fc, n16)
			}
		}
	}
}

func TestInferTxRequestLength(t *testing.T) {
	t.Parallel()
	var tx Tx
	var buf, valueBuf [256]byte
	var values16 [125]uint16
	// Discrete+Coils
	for startAddr := uint16(0); startAddr < 100; startAddr++ {
		// REQUEST WRITE SINGLE COIL
		n, err := tx.RequestWriteSingleCoil(buf[:], startAddr, true)
		if err != nil {
			t.Fatal(err)
		}
		fc, n16, err := InferRequestPacketLength(buf[:])
		if err != nil {
			t.Fatal(err)
		}
		if fc != FCWriteSingleCoil || int(n16) != n {
			t.Fatal("expected function code 5, length", n, "got", fc, n16)
		}

		for quantity := uint16(1); quantity < 125; quantity++ {
			// REQUEST READ COILS
			n, err := tx.RequestReadCoils(buf[:], startAddr, quantity)
			if err != nil {
				t.Fatal(err)
			}
			fc, n16, err := InferRequestPacketLength(buf[:])
			if err != nil {
				t.Fatal(err)
			}
			if fc != FCReadCoils || int(n16) != n {
				t.Fatal("expected function code 1, length", n, "got", fc, n16)
			}

			// REQUEST READ DISCRETE INPUTS
			n, err = tx.RequestReadDiscreteInputs(buf[:], startAddr, quantity)
			if err != nil {
				t.Fatal(err)
			}
			fc, n16, err = InferRequestPacketLength(buf[:])
			if err != nil {
				t.Fatal(err)
			}
			if fc != FCReadDiscreteInputs || int(n16) != n {
				t.Fatal("expected function code 2, length", n, "got", fc, n16)
			}

			// REQUEST WRITE MULTIPLE COILS
			packedLen := quantity / 8
			if quantity%8 != 0 {
				packedLen++
			}
			n, err = tx.RequestWriteMultipleCoils(buf[:], startAddr, quantity, valueBuf[:packedLen])
			if err != nil {
				t.Fatal(err)
			}
			fc, n16, err = InferRequestPacketLength(buf[:])
			if err != nil {
				t.Fatal(err)
			}
			if fc != FCWriteMultipleCoils || int(n16) != n {
				t.Fatal("expected function code 15, length", n, "got", fc, n16)
			}
		}
	}

	for startAddr := uint16(0); startAddr < 100; startAddr++ {
		// REQUEST WRITE SINGLE REGISTER
		n, err := tx.RequestWriteSingleRegister(buf[:], startAddr, 0)
		if err != nil {
			t.Fatal(err)
		}
		fc, n16, err := InferRequestPacketLength(buf[:])
		if err != nil {
			t.Fatal(err)
		}
		if fc != FCWriteSingleRegister || int(n16) != n {
			t.Fatal("expected function code 6, length", n, "got", fc, n16)
		}

		for quantity := uint16(1); quantity < 123; quantity++ {
			// REQUEST READ HOLDING REGISTERS
			n, err = tx.RequestReadHoldingRegisters(buf[:], startAddr, quantity)
			if err != nil {
				t.Fatal(err)
			}
			fc, n16, err := InferRequestPacketLength(buf[:])
			if err != nil {
				t.Fatal(err)
			}
			if fc != FCReadHoldingRegisters || int(n16) != n {
				t.Fatal("expected function code 3, length", n, "got", fc, n16)
			}

			// REQUEST READ INPUT REGISTERS
			n, err = tx.RequestReadInputRegisters(buf[:], startAddr, quantity)
			if err != nil {
				t.Fatal(err)
			}
			fc, n16, err = InferRequestPacketLength(buf[:])
			if err != nil {
				t.Fatal(err)
			}
			if fc != FCReadInputRegisters || int(n16) != n {
				t.Fatal("expected function code 4, length", n, "got", fc, n16)
			}

			// REQUEST WRITE MULTIPLE REGISTERS
			n, err = tx.RequestWriteMultipleRegisters(buf[:], startAddr, values16[:quantity])
			if err != nil {
				t.Fatal(err)
			}
			fc, n16, err = InferRequestPacketLength(buf[:])
			if err != nil {
				t.Fatal(err)
			}
			if fc != FCWriteMultipleRegisters || int(n16) != n {
				t.Fatal("expected function code 16, length", n, "got", fc, n16)
			}
		}
	}
}
