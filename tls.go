package main

import (
	"bytes"
	"errors"
	"io"
	"strconv"
)

type recordType uint8

type alert uint8

const (
	maxPlaintext    = 16384
	maxCiphertext   = 16384 + 2048
	recordHeaderLen = 5
	maxHandshake    = 65536

	statusTypeOCSP uint8 = 1

	recordTypeHandshake recordType = 22

	typeClientHello uint8 = 1

	alertUnexpectedMessage alert = 10
	alertRecordOverflow    alert = 22
	alertInternalError     alert = 80
)

var (
	extensionServerName      uint16 = 0
	extensionStatusRequest   uint16 = 5
	extensionSupportedCurves uint16 = 10
	extensionSupportedPoints uint16 = 11
	extensionSessionTicket   uint16 = 35
	extensionNextProtoNeg    uint16 = 13172 // not IANA assigned
)

var alertText = map[alert]string{
	alertUnexpectedMessage: "unexpected message",
	alertRecordOverflow:    "record overflow",
	alertInternalError:     "internal error",
}

func (e alert) String() string {
	s, ok := alertText[e]
	if ok {
		return s
	}
	return "alert(" + strconv.Itoa(int(e)) + ")"
}

func (e alert) Error() string {
	return e.String()
}
type ClientHelloMsg struct {
	Raw                []byte
	Vers               uint16
	Random             []byte
	SessionId          []byte
	CipherSuites       []uint16
	CompressionMethods []uint8
	NextProtoNeg       bool
	ServerName         string
	OcspStapling       bool
	SupportedCurves    []uint16
	SupportedPoints    []uint8
	TicketSupported    bool
	SessionTicket      []uint8
}

func (m *ClientHelloMsg) unmarshal(data []byte) bool {
	if len(data) < 42 {
		return false
	}
	m.Raw = data
	m.Vers = uint16(data[4])<<8 | uint16(data[5])
	m.Random = data[6:38]
	sessionIdLen := int(data[38])
	if sessionIdLen > 32 || len(data) < 39+sessionIdLen {
		return false
	}
	m.SessionId = data[39 : 39+sessionIdLen]
	data = data[39+sessionIdLen:]
	if len(data) < 2 {
		return false
	}
	// cipherSuiteLen is the number of bytes of cipher suite numbers. Since
	// they are uint16s, the number must be even.
	cipherSuiteLen := int(data[0])<<8 | int(data[1])
	if cipherSuiteLen%2 == 1 || len(data) < 2+cipherSuiteLen {
		return false
	}
	numCipherSuites := cipherSuiteLen / 2
	m.CipherSuites = make([]uint16, numCipherSuites)
	for i := 0; i < numCipherSuites; i++ {
		m.CipherSuites[i] = uint16(data[2+2*i])<<8 | uint16(data[3+2*i])
	}
	data = data[2+cipherSuiteLen:]
	if len(data) < 1 {
		return false
	}
	compressionMethodsLen := int(data[0])
	if len(data) < 1+compressionMethodsLen {
		return false
	}
	m.CompressionMethods = data[1 : 1+compressionMethodsLen]

	data = data[1+compressionMethodsLen:]

	m.NextProtoNeg = false
	m.ServerName = ""
	m.OcspStapling = false
	m.TicketSupported = false
	m.SessionTicket = nil

	if len(data) == 0 {
		// ClientHello is optionally followed by extension data
		return true
	}
	if len(data) < 2 {
		return false
	}

	extensionsLength := int(data[0])<<8 | int(data[1])
	data = data[2:]
	if extensionsLength != len(data) {
		return false
	}

	for len(data) != 0 {
		if len(data) < 4 {
			return false
		}
		extension := uint16(data[0])<<8 | uint16(data[1])
		length := int(data[2])<<8 | int(data[3])
		data = data[4:]
		if len(data) < length {
			return false
		}

		switch extension {
		case extensionServerName:
			if length < 2 {
				return false
			}
			numNames := int(data[0])<<8 | int(data[1])
			d := data[2:]
			for i := 0; i < numNames; i++ {
				if len(d) < 3 {
					return false
				}
				nameType := d[0]
				nameLen := int(d[1])<<8 | int(d[2])
				d = d[3:]
				if len(d) < nameLen {
					return false
				}
				if nameType == 0 {
					m.ServerName = string(d[0:nameLen])
					break
				}
				d = d[nameLen:]
			}
		case extensionNextProtoNeg:
			if length > 0 {
				return false
			}
			m.NextProtoNeg = true
		case extensionStatusRequest:
			m.OcspStapling = length > 0 && data[0] == statusTypeOCSP
		case extensionSupportedCurves:
			// http://tools.ietf.org/html/rfc4492#section-5.5.1
			if length < 2 {
				return false
			}
			l := int(data[0])<<8 | int(data[1])
			if l%2 == 1 || length != l+2 {
				return false
			}
			numCurves := l / 2
			m.SupportedCurves = make([]uint16, numCurves)
			d := data[2:]
			for i := 0; i < numCurves; i++ {
				m.SupportedCurves[i] = uint16(d[0])<<8 | uint16(d[1])
				d = d[2:]
			}
		case extensionSupportedPoints:
			// http://tools.ietf.org/html/rfc4492#section-5.5.2
			if length < 1 {
				return false
			}
			l := int(data[0])
			if length != l+1 {
				return false
			}
			m.SupportedPoints = make([]uint8, l)
			copy(m.SupportedPoints, data[1:])
		case extensionSessionTicket:
			// http://tools.ietf.org/html/rfc5077#section-3.2
			m.TicketSupported = true
			m.SessionTicket = data[:length]
		}
		data = data[length:]
	}

	return true
}

type block struct {
	data []byte
	off  int
}

func (b *block) resize(n int) {
	if n > cap(b.data) {
		b.reserve(n)
	}
	b.data = b.data[0:n]
}

func (b *block) reserve(n int) {
	if cap(b.data) >= n {
		return
	}
	m := cap(b.data)
	if m == 0 {
		m = 1024
	}
	for m < n {
		m *= 2
	}
	data := make([]byte, len(b.data), m)
	copy(data, b.data)
	b.data = data
}

func (b *block) readFromUntil(r io.Reader, n int) error {
	if len(b.data) >= n {
		return nil
	}

	b.reserve(n)
	for {
		m, err := r.Read(b.data[len(b.data):cap(b.data)])
		b.data = b.data[0 : len(b.data)+m]
		if len(b.data) >= n {
			break
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func (b *block) Read(p []byte) (n int, err error) {
	n = copy(p, b.data[b.off:])
	b.off += n
	return
}

func splitBlock(b *block, n int) (*block, *block) {
	if len(b.data) <= n {
		return b, nil
	}
	bb := newBlock()
	bb.resize(len(b.data) - n)
	copy(bb.data, b.data[n:])
	b.data = b.data[0:n]
	return b, bb
}

func newBlock() *block {
	return new(block)
}

func readClientHello(rd io.Reader) (*ClientHelloMsg, error) {
	var nextBlock *block  // raw input, right off the wire
	var hand bytes.Buffer // handshake data waiting to be read

	// readRecord reads the next TLS record from the connection
	// and updates the record layer state.
	readRecord := func() error {
		// Caller must be in sync with connection:
		// handshake data if handshake not yet completed,
		// else application data.  (We don't support renegotiation.)
		if nextBlock == nil {
			nextBlock = newBlock()
		}
		b := nextBlock

		// Read header, payload.
		if err := b.readFromUntil(rd, recordHeaderLen); err != nil {
			return err
		}
		typ := recordType(b.data[0])

		// No valid TLS record has a type of 0x80, however SSLv2 handshakes
		// start with a uint16 length where the MSB is set and the first record
		// is always < 256 bytes long. Therefore typ == 0x80 strongly suggests
		// an SSLv2 client.
		if typ == 0x80 {
			return errors.New("tls: unsupported SSLv2 handshake received")
		}

		vers := uint16(b.data[1])<<8 | uint16(b.data[2])
		n := int(b.data[3])<<8 | int(b.data[4])
		if n > maxCiphertext {
			return alertRecordOverflow
		}

		// First message, be extra suspicious:
		// this might not be a TLS client.
		// Bail out before reading a full 'body', if possible.
		// The current max version is 3.1.
		// If the version is >= 16.0, it's probably not real.
		// Similarly, a clientHello message encodes in
		// well under a kilobyte.  If the length is >= 12 kB,
		// it's probably not real.
		if (typ != recordTypeHandshake) || vers >= 0x1000 || n >= 0x3000 {
			return alertUnexpectedMessage
		}

		if err := b.readFromUntil(rd, recordHeaderLen+n); err != nil {
			return err
		}

		// Process message.
		b, nextBlock = splitBlock(b, recordHeaderLen+n)
		b.off = recordHeaderLen
		data := b.data[b.off:]
		if len(data) > maxPlaintext {
			return alertRecordOverflow
		}

		hand.Write(data)

		return nil
	}

	if err := readRecord(); err != nil {
		return nil, err
	}

	data := hand.Bytes()
	n := int(data[1])<<16 | int(data[2])<<8 | int(data[3])
	if n > maxHandshake {
		return nil, alertInternalError
	}
	for hand.Len() < 4+n {
		if err := readRecord(); err != nil {
			return nil, err
		}
	}

	data = hand.Next(4 + n)
	if data[0] != typeClientHello {
		return nil, alertUnexpectedMessage
	}

	msg := new(ClientHelloMsg)
	if !msg.unmarshal(data) {
		return nil, alertUnexpectedMessage
	}

	return msg, nil
}