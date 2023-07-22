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
