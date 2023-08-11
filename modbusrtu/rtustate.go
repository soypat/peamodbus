package modbusrtu

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"io"
	"sync"
	"time"

	"github.com/soypat/peamodbus"
	"golang.org/x/exp/slog"
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
	pendingAddr uint8

	murx       sync.Mutex
	lastRxTime time.Time
	dataStart  int
	dataEnd    int
	log        *slog.Logger
	preproc    preprocessor
	rxbuf      [512]byte
	txbuf      [256 + 3]byte
}

// TryRx tries to read a complete packet from the connection. If the packet is
// not complete, it returns ErrMissingPacketData. The PDU is returned excluding the
// address byte and the CRC. In the case the CRC is wrong CRCError is returned
// along with the whole packet in it's Packet field.
func (cs *connState) TryRx(isResponse bool) (pdu []byte, address uint8, err error) {
	cs.murx.Lock()
	defer cs.murx.Unlock()
	var pdulen uint16
	defer func() {
		cs.debug("TryRx returns",
			slog.String("pdu", string(pdu)),
			slog.String("rxbuf", string(cs.rxBytes())),
			slog.Int("rxlen", cs.buffered()),
			slog.Uint64("expected_pdulen", uint64(pdulen)),
			slog.Any("err", err),
		)
	}()
	if cs.dataEnd > 256 || cs.buffered() == 0 {
		// Wrap around before overloading buffer.
		cs.resetRxBuf()
	}
	buf := cs.rxbuf[:]

	if buffered := cs.buffered(); buffered < 2 {
		// If we have read less than 2 bytes, we can't infer the length of the PDU.
		// We read to reach 2 bytes. We do this since some read ops are blocking.
		n, err := cs.read(2 - buffered)
		buffered += n
		if buffered < 2 {
			if err != nil {
				return nil, 0, err
			}
			return nil, 0, peamodbus.ErrMissingPacketData // Still missing data
		}
	}

	if isResponse {
		_, pdulen, err = peamodbus.InferResponsePacketLength(buf[cs.dataStart+1 : cs.dataEnd])
	} else {
		_, pdulen, err = peamodbus.InferRequestPacketLength(buf[cs.dataStart+1 : cs.dataEnd])
	}
	if errors.Is(err, peamodbus.ErrBadFunctionCode) {
		cs.error("desync")
		cs.closeErr = err // Close connection, probably desynced.
		return nil, 0, err
	}

	missingData := int(pdulen) + 3 - cs.buffered()
	cs.debug("read missing packet data", slog.Uint64("pdulen", uint64(pdulen)), slog.Int("buffered", cs.buffered()), slog.Int("missing", missingData))
	n, rxerr := cs.read(missingData)
	if rxerr != nil {
		return nil, 0, rxerr
	}
	if err != nil {
		return nil, 0, err
	}
	if missingData > n {
		cs.debug("missing data", slog.Int("missing", missingData-n))
		return nil, 0, peamodbus.ErrMissingPacketData // Still missing data
	}

	// Gotten to this point we have a complete Address+PDU+Checksum packet.
	address = buf[cs.dataStart]
	packet := buf[cs.dataStart : cs.dataStart+int(pdulen)+3]
	// Check CRC.
	crc := generateCRC(packet[:len(packet)-2])
	gotcrc := binary.LittleEndian.Uint16(packet[len(packet)-2:])
	cs.dataStart += len(packet)
	if gotcrc != crc {
		err = CRCError{Packet: packet}
	}
	pdu = packet[1 : len(packet)-2]
	return pdu, address, err
}

func (cs *connState) read(upTo int) (n int, err error) {
	if upTo > len(cs.rxbuf)-cs.dataEnd {
		return 0, errors.New("read overflow")
	} else if upTo == 0 {
		panic("read 0")
	}
	n, err = cs.port.Read(cs.rxbuf[cs.dataEnd : cs.dataEnd+upTo])
	if n != 0 {
		cs.lastRxTime = time.Now()
		cs.dataEnd += n
		if cs.preproc != nil {
			start, end := cs.preproc(cs.rxbuf[cs.dataStart:cs.dataEnd], n)
			cs.dataEnd = cs.dataStart + end
			cs.dataStart += start
		}
	}
	if err == nil && n != 0 {
		cs.debug("read byte(s)", slog.Int("n", n), slog.String("incoming", string(cs.rxbuf[cs.dataEnd-n:cs.dataEnd])))
	} else if err == nil {
		cs.error("read no bytes with nil error?")
	} else if err != nil {
		cs.error("read error", slog.String("err", err.Error()))
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

func (cs *connState) error(msg string, attrs ...slog.Attr) {
	if cs.log != nil {
		cs.log.LogAttrs(context.TODO(), slog.LevelError, msg, attrs...)
	}
}

func (cs *connState) info(msg string, attrs ...slog.Attr) {
	if cs.log != nil {
		cs.log.LogAttrs(context.TODO(), slog.LevelInfo, msg, attrs...)
	}
}

func (cs *connState) debug(msg string, attrs ...slog.Attr) {
	if cs.log != nil {
		cs.log.LogAttrs(context.TODO(), slog.LevelDebug, msg, attrs...)
	}
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
			if crc&1 != 0 {
				crc >>= 1
				crc ^= 0xA001
			} else {
				crc >>= 1
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
