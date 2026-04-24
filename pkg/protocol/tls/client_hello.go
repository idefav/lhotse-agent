package tls

import (
	"bufio"
	"encoding/binary"
	"errors"
	"io"
)

const (
	recordTypeHandshake      = 22
	handshakeTypeClientHello = 1
	maxTLSRecordLen          = 16384 + 2048

	// ClientHelloPeekBufferSize is large enough to buffer the TLS record header
	// and the largest ClientHello record this parser accepts.
	ClientHelloPeekBufferSize = 5 + maxTLSRecordLen
)

var errClientHelloTruncated = errors.New("truncated tls client hello")

func PeekClientHelloServerName(reader *bufio.Reader) (string, bool, error) {
	header, err := reader.Peek(5)
	if err != nil {
		return "", false, err
	}

	recordLen := int(binary.BigEndian.Uint16(header[3:5]))
	if header[0] != recordTypeHandshake || header[1] != 3 || recordLen <= 0 || recordLen > maxTLSRecordLen {
		return "", false, nil
	}

	record, err := reader.Peek(5 + recordLen)
	if err != nil {
		if errors.Is(err, bufio.ErrBufferFull) || errors.Is(err, io.EOF) {
			return "", true, errClientHelloTruncated
		}
		return "", true, err
	}

	serverName, err := ParseClientHelloServerName(record)
	if err != nil {
		return "", true, err
	}
	return serverName, true, nil
}

func ParseClientHelloServerName(record []byte) (string, error) {
	if len(record) < 5 {
		return "", errClientHelloTruncated
	}
	if record[0] != recordTypeHandshake {
		return "", nil
	}

	recordLen := int(binary.BigEndian.Uint16(record[3:5]))
	if len(record) < 5+recordLen {
		return "", errClientHelloTruncated
	}

	body := record[5 : 5+recordLen]
	if len(body) < 4 || body[0] != handshakeTypeClientHello {
		return "", nil
	}

	helloLen := int(body[1])<<16 | int(body[2])<<8 | int(body[3])
	if len(body) < 4+helloLen {
		return "", errClientHelloTruncated
	}

	hello := body[4 : 4+helloLen]
	if len(hello) < 34 {
		return "", errClientHelloTruncated
	}

	offset := 34
	if offset >= len(hello) {
		return "", errClientHelloTruncated
	}

	sessionIDLen := int(hello[offset])
	offset++
	if offset+sessionIDLen > len(hello) {
		return "", errClientHelloTruncated
	}
	offset += sessionIDLen

	if offset+2 > len(hello) {
		return "", errClientHelloTruncated
	}
	cipherSuiteLen := int(binary.BigEndian.Uint16(hello[offset : offset+2]))
	offset += 2
	if offset+cipherSuiteLen > len(hello) {
		return "", errClientHelloTruncated
	}
	offset += cipherSuiteLen

	if offset >= len(hello) {
		return "", errClientHelloTruncated
	}
	compressionMethodsLen := int(hello[offset])
	offset++
	if offset+compressionMethodsLen > len(hello) {
		return "", errClientHelloTruncated
	}
	offset += compressionMethodsLen

	if offset == len(hello) {
		return "", nil
	}
	if offset+2 > len(hello) {
		return "", errClientHelloTruncated
	}

	extensionsLen := int(binary.BigEndian.Uint16(hello[offset : offset+2]))
	offset += 2
	if offset+extensionsLen > len(hello) {
		return "", errClientHelloTruncated
	}

	extensions := hello[offset : offset+extensionsLen]
	for len(extensions) >= 4 {
		extensionType := binary.BigEndian.Uint16(extensions[:2])
		extensionLen := int(binary.BigEndian.Uint16(extensions[2:4]))
		extensions = extensions[4:]
		if extensionLen > len(extensions) {
			return "", errClientHelloTruncated
		}

		extensionData := extensions[:extensionLen]
		extensions = extensions[extensionLen:]
		if extensionType != 0 {
			continue
		}

		serverName, err := parseServerNameExtension(extensionData)
		if err != nil {
			return "", err
		}
		return serverName, nil
	}

	return "", nil
}

func parseServerNameExtension(data []byte) (string, error) {
	if len(data) < 2 {
		return "", errClientHelloTruncated
	}

	listLen := int(binary.BigEndian.Uint16(data[:2]))
	if len(data) < 2+listLen {
		return "", errClientHelloTruncated
	}

	names := data[2 : 2+listLen]
	for len(names) >= 3 {
		nameType := names[0]
		nameLen := int(binary.BigEndian.Uint16(names[1:3]))
		names = names[3:]
		if nameLen > len(names) {
			return "", errClientHelloTruncated
		}
		serverName := names[:nameLen]
		names = names[nameLen:]
		if nameType == 0 {
			return string(serverName), nil
		}
	}

	if len(names) > 0 {
		return "", errClientHelloTruncated
	}
	return "", nil
}
