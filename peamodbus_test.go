package peamodbus

import (
	"errors"
	"testing"
)

func TestReadHoldingRegisters_loopback(t *testing.T) {
	model := &BlockedModel{}
	model.SetHoldingRegister(0, 0)
	var txbuf, rxbuf [256]byte
	rx, tx := newRxTx(t, model, rxbuf[:])
	// Test request packet.
	const (
		startAddr = 0
		quantity  = 3
	)
	n, err := tx.RequestReadHoldingRegisters(txbuf[:], startAddr, quantity)
	if err != nil {
		t.Fatal(err)
	}
	if n != 5 {
		t.Fatal("expected 5 bytes, got", n)
	}
	fc, n16, err := InferRequestPacketLength(txbuf[:])
	if err != nil {
		t.Fatal(err)
	}
	if fc != FCReadHoldingRegisters || n16 != 5 {
		t.Fatal("expected function code 3, length 5, got", fc, n16)
	}
	// Now test response packet.
	err = rx.ReceiveRequest(txbuf[:n])
	if err != nil {
		t.Fatal(err)
	}
	fc, n16, err = InferResponsePacketLength(rxbuf[:])
	if err != nil {
		t.Fatal(err)
	}
	expectedLen := uint16(2 + 2*quantity)
	if fc != FCReadHoldingRegisters || n16 != expectedLen {
		t.Fatal("expected function code 3, length 2+2*n", expectedLen, "got:", fc, n16)
	}
	_ = rx
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

func newRxTx(t *testing.T, model DataModel, rxbuf []byte) (*Rx, *Tx) {
	tx := &Tx{}
	scratch := make([]byte, 256)
	return &Rx{
		RxCallbacks: RxCallbacks{
			OnDataLong: func(rx *Rx, fc FunctionCode, data []byte) error {
				return errors.New("unhandled function code " + fc.String())
				var exc Exception
				rxbuf[0] = byte(fc)
				if fc.IsWrite() {
					exc = writeToModel(model, fc, rx.LastPendingRequest.maybeAddr, rx.LastPendingRequest.maybeValueQuantity, data)
				} else if fc.IsRead() {
					exc = readFromModel(rxbuf[1:], model, fc, rx.LastPendingRequest.maybeAddr, rx.LastPendingRequest.maybeValueQuantity)
				}
				if exc != 0 {
					return exc
				}
				return nil
			},
			OnError: func(rx *Rx, err error) {
				t.Error(err)
			},
			OnData: func(rx *Rx, req Request) error {
				_, err := req.PutResponse(tx, model, rxbuf[:], scratch)
				return err
			},
		},
	}, tx
}
