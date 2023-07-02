package modbusrtu

import (
	"encoding/binary"
	"encoding/hex"
	"errors"
	"io"
	"sync"
	"time"

	"github.com/soypat/peamodbus"
)

var (
	errYetToConnect = errors.New("yet to connect")
)

// CRCError is returned when the CRC of a packet is wrong.
type CRCError struct {
	Packet []byte
}

func (e CRCError) Error() string {
	return "bad CRC:\n" + hex.Dump(e.Packet)
}

// connState stores the persisting state of a websocket server connection.
// Since this state is shared between frames it is protected by a mutex so that
// the Client implementation is concurrent-safe.
type connState struct {
	mu       sync.Mutex
	data     peamodbus.DataModel
	closeErr error
	port     io.ReadWriter

	// pendingRequest is the request that is currently being processed.
	// While set the server will wait for reply.
	pendingAddr    uint8
	pendingRequest peamodbus.Request

	murx       sync.Mutex
	lastRxTime time.Time
	dataStart  int
	dataEnd    int
	rxbuf      [512]byte
}

// TryRx tries to read a complete packet from the connection. If the packet is
// not complete, it returns ErrMissingPacketData. The PDU is returned excluding the
// address byte and the CRC. In the case the CRC is wrong CRCError is returned
// along with the whole packet in it's Packet field.
func (state *connState) TryRx(isResponse bool) (pdu []byte, address uint8, err error) {
	state.murx.Lock()
	defer state.murx.Unlock()
	if state.dataEnd > 256 || state.buffered() == 0 {
		// Wrap around before overloading buffer.
		state.resetRxBuf()
	}
	buf := state.rxbuf[:]

	if buffered := state.buffered(); buffered < 2 {
		// If we have read less than 2 bytes, we can't infer the length of the PDU.
		// We read to reach 2 bytes. We do this since some read ops are blocking.
		n, err := state.read(2 - buffered)
		buffered += n
		if buffered < 2 {
			if err != nil {
				return nil, 0, err
			}
			return nil, 0, peamodbus.ErrMissingPacketData // Still missing data
		}
	}
	var pdulen uint16
	if isResponse {
		_, pdulen, err = peamodbus.InferResponsePacketLength(buf[state.dataStart+1 : state.dataEnd])
	} else {
		_, pdulen, err = peamodbus.InferRequestPacketLength(buf[state.dataStart+1 : state.dataEnd])
	}
	if errors.Is(err, peamodbus.ErrBadFunctionCode) {
		state.closeErr = err // Close connection, probably desynced.
		return nil, 0, err
	}

	missingData := int(pdulen) + 3 - state.buffered()
	n, rxerr := state.read(missingData)
	if rxerr != nil {
		return nil, 0, rxerr
	}
	if err != nil {
		return nil, 0, err
	}
	if missingData > n {
		return nil, 0, peamodbus.ErrMissingPacketData // Still missing data
	}

	// Gotten to this point we have a complete Address+PDU+Checksum packet.
	address = buf[state.dataStart]
	packet := buf[state.dataStart : state.dataStart+int(pdulen)+3]
	// Check CRC.
	crc := generateCRC(packet[:len(packet)-2])
	gotcrc := binary.LittleEndian.Uint16(packet[len(packet)-2:])
	state.dataStart += len(packet)
	if gotcrc != crc {
		err = CRCError{Packet: packet}
	}
	return packet[1 : len(packet)-2], address, err
}

func (cs *connState) read(upTo int) (n int, err error) {
	if upTo > len(cs.rxbuf)-cs.dataEnd {
		return 0, errors.New("read overflow")
	}
	n, err = cs.port.Read(cs.rxbuf[cs.dataEnd : cs.dataEnd+upTo])
	if n != 0 {
		cs.lastRxTime = time.Now()
		cs.dataEnd += n
	}
	return n, err
}

func (cs *connState) buffered() int { return cs.dataEnd - cs.dataStart }

func (cs *connState) rxBytes() []byte {
	return cs.rxbuf[cs.dataStart:cs.dataEnd]
}

// Err returns the error responsible for a closed connection. The wrapped chain of errors
// will contain exceptions, io.EOF or a net.ErrClosed error.
//
// Err is safe to call concurrently.
func (cs *connState) Err() error {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	return cs.closeErr
}

func (cs *connState) ResetRxBuf() {
	cs.murx.Lock()
	cs.resetRxBuf()
	cs.murx.Unlock()
}

func (cs *connState) resetRxBuf() {
	if cs.dataStart != cs.dataEnd && cs.buffered() < 256 {
		cs.dataEnd = copy(cs.rxbuf[:], cs.rxbuf[cs.dataStart:cs.dataEnd])
	} else {
		// We either overflowed our buffer or dataEnd==dataStart.
		cs.dataEnd = 0
	}
	cs.dataStart = 0
}

// NewPendingRequest adds a request to the LIFO queue.
func (cs *connState) NewPendingRequest(r peamodbus.Request) {
	cs.mu.Lock()
	cs.pendingRequest = r
	cs.mu.Unlock()
}

func (cs *connState) HandlePendingRequests(tx *peamodbus.Tx) (err error) {
	const mbapsize = 1 + 2 // | address(uint8) | data... |  crc(uint16) |
	var scratch, txBuf [256 + mbapsize]byte
	cs.mu.Lock()
	defer cs.mu.Unlock()
	txBuf[0] = cs.pendingAddr
	req := cs.pendingRequest
	plen, err := req.PutResponse(tx, cs.data, txBuf[1:], scratch[:])
	if err != nil {
		return err
	}

	endOfData := 1 + plen
	crc := generateCRC(txBuf[0:endOfData])
	binary.LittleEndian.PutUint16(txBuf[endOfData:], crc)
	_, err = cs.port.Write(txBuf[:endOfData+2])
	return err
}

func (cs *connState) callbacks() (peamodbus.RxCallbacks, peamodbus.TxCallbacks) {
	return peamodbus.RxCallbacks{
			OnError: func(rx *peamodbus.Rx, err error) {
				println(err.Error())
				// cs.CloseConn(err)
			},
			OnException: func(rx *peamodbus.Rx, exceptCode peamodbus.Exception) error {
				return exceptCode
			},
			OnData: cs.dataAccessHandler,
			// See dataHandler method on cs.
			OnDataLong: cs.dataRequestHandler,
			OnUnhandled: func(rx *peamodbus.Rx, fc peamodbus.FunctionCode, buf []byte) error {
				println("got unhandled function code ", fc.String())
				return nil
			},
		}, peamodbus.TxCallbacks{
			OnError: nil,
		}
}

func (cs *connState) callbacksClient() (peamodbus.RxCallbacks, peamodbus.TxCallbacks) {
	return peamodbus.RxCallbacks{
			OnError: func(rx *peamodbus.Rx, err error) {
				println(err.Error())
			},
			OnException: func(rx *peamodbus.Rx, exceptCode peamodbus.Exception) error {
				return exceptCode
			},
			OnData: cs.dataAccessHandler,
			// See dataHandler method on cs.
			OnDataLong: func(rx *peamodbus.Rx, fc peamodbus.FunctionCode, buf []byte) error {
				println("unhandled function code", fc.String())
				return nil
			},
			OnUnhandled: func(rx *peamodbus.Rx, fc peamodbus.FunctionCode, buf []byte) error {
				println("unhandled function code", fc.String())
				return nil
			},
		}, peamodbus.TxCallbacks{
			OnError: nil,
		}
}

func (cs *connState) dataAccessHandler(rx *peamodbus.Rx, req peamodbus.Request) error {
	if cs.data == nil {
		println("got invalid data model request", req.String())
		return nil // ignore
	}
	// println("got data model request", req.String())
	cs.NewPendingRequest(req)
	return nil
}

func (cs *connState) dataRequestHandler(rx *peamodbus.Rx, fc peamodbus.FunctionCode, data []byte) (err error) {
	println("unhandled function code", fc.String())
	return nil
}

func generateCRC(b []byte) (crc uint16) {
	const (
		startCRC = 0xFFFF
		xorCRC   = 0xA001
	)
	crc = startCRC
	for i := 0; i < len(b); i++ {
		crc ^= uint16(b[i])
		for n := 0; n < 8; n++ {
			crc >>= 1
			if crc&1 != 0 {
				crc ^= xorCRC
			}
		}
	}
	return crc
}

func generateLRC(b []byte) (lrc uint8) {
	for i := 0; i < len(b); i++ {
		lrc += b[i]
	}
	return uint8(-int8(lrc)) // This is how you take two's complement in Go. equivalent to (0xff-lrc)+1
}
