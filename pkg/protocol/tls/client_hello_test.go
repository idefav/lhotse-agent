package tls

import (
	"bufio"
	"bytes"
	"crypto/tls"
	"encoding/binary"
	"errors"
	"net"
	"testing"
	"time"
)

func TestParseClientHelloServerName(t *testing.T) {
	record := captureClientHelloRecord(t, "www.example.com")

	serverName, err := ParseClientHelloServerName(record)
	if err != nil {
		t.Fatalf("ParseClientHelloServerName() error = %v", err)
	}
	if serverName != "www.example.com" {
		t.Fatalf("serverName = %q, want %q", serverName, "www.example.com")
	}
}

func TestPeekClientHelloServerName(t *testing.T) {
	record := captureClientHelloRecord(t, "api.openai.com")
	reader := bufio.NewReaderSize(bytes.NewReader(record), len(record))

	serverName, isTLS, err := PeekClientHelloServerName(reader)
	if err != nil {
		t.Fatalf("PeekClientHelloServerName() error = %v", err)
	}
	if !isTLS {
		t.Fatal("PeekClientHelloServerName() isTLS = false, want true")
	}
	if serverName != "api.openai.com" {
		t.Fatalf("serverName = %q, want %q", serverName, "api.openai.com")
	}
}

func TestPeekClientHelloServerNameNonTLS(t *testing.T) {
	reader := bufio.NewReader(bytes.NewReader([]byte("GET / HTTP/1.1\r\n\r\n")))

	serverName, isTLS, err := PeekClientHelloServerName(reader)
	if err != nil {
		t.Fatalf("PeekClientHelloServerName() error = %v", err)
	}
	if isTLS {
		t.Fatal("PeekClientHelloServerName() isTLS = true, want false")
	}
	if serverName != "" {
		t.Fatalf("serverName = %q, want empty", serverName)
	}
}

func TestPeekClientHelloServerNameLargeRecord(t *testing.T) {
	record := padClientHelloRecord(t, captureClientHelloRecord(t, "large.example.com"), 5000)

	defaultReader := bufio.NewReader(bytes.NewReader(record))
	_, isTLS, err := PeekClientHelloServerName(defaultReader)
	if !isTLS {
		t.Fatal("PeekClientHelloServerName() isTLS = false, want true")
	}
	if !errors.Is(err, errClientHelloTruncated) {
		t.Fatalf("PeekClientHelloServerName() error = %v, want %v", err, errClientHelloTruncated)
	}

	reader := bufio.NewReaderSize(bytes.NewReader(record), ClientHelloPeekBufferSize)
	serverName, isTLS, err := PeekClientHelloServerName(reader)
	if err != nil {
		t.Fatalf("PeekClientHelloServerName() error = %v", err)
	}
	if !isTLS {
		t.Fatal("PeekClientHelloServerName() isTLS = false, want true")
	}
	if serverName != "large.example.com" {
		t.Fatalf("serverName = %q, want %q", serverName, "large.example.com")
	}
}

func TestParseClientHelloServerNameTruncated(t *testing.T) {
	record := captureClientHelloRecord(t, "www.example.com")
	_, err := ParseClientHelloServerName(record[:10])
	if !errors.Is(err, errClientHelloTruncated) {
		t.Fatalf("ParseClientHelloServerName() error = %v, want %v", err, errClientHelloTruncated)
	}
}

func captureClientHelloRecord(t *testing.T, serverName string) []byte {
	t.Helper()

	clientConn, serverConn := net.Pipe()
	defer serverConn.Close()

	done := make(chan struct{})
	go func() {
		defer close(done)
		defer clientConn.Close()
		conn := tls.Client(clientConn, &tls.Config{
			ServerName:         serverName,
			InsecureSkipVerify: true,
		})
		_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
		_ = conn.Handshake()
	}()

	_ = serverConn.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 4096)
	n, err := serverConn.Read(buf)
	if err != nil {
		t.Fatalf("serverConn.Read() error = %v", err)
	}
	<-done
	return append([]byte(nil), buf[:n]...)
}

func padClientHelloRecord(t *testing.T, record []byte, targetLen int) []byte {
	t.Helper()

	if len(record) >= targetLen {
		return record
	}
	if len(record) < 9 || record[0] != recordTypeHandshake {
		t.Fatalf("record is not a TLS handshake record")
	}

	recordLen := int(binary.BigEndian.Uint16(record[3:5]))
	if len(record) < 5+recordLen {
		t.Fatalf("record length = %d, buffer length = %d", recordLen, len(record))
	}

	body := record[5 : 5+recordLen]
	helloLen := int(body[1])<<16 | int(body[2])<<8 | int(body[3])
	hello := body[4 : 4+helloLen]

	offset := 34
	sessionIDLen := int(hello[offset])
	offset += 1 + sessionIDLen
	cipherSuiteLen := int(binary.BigEndian.Uint16(hello[offset : offset+2]))
	offset += 2 + cipherSuiteLen
	compressionMethodsLen := int(hello[offset])
	offset += 1 + compressionMethodsLen
	extensionsLenOffset := 5 + 4 + offset
	extensionsLen := int(binary.BigEndian.Uint16(record[extensionsLenOffset : extensionsLenOffset+2]))
	extensionsEnd := extensionsLenOffset + 2 + extensionsLen

	paddingLen := targetLen - len(record) - 4
	if paddingLen < 0 {
		paddingLen = 0
	}
	padding := make([]byte, 4+paddingLen)
	binary.BigEndian.PutUint16(padding[0:2], 21)
	binary.BigEndian.PutUint16(padding[2:4], uint16(paddingLen))

	padded := make([]byte, 0, len(record)+len(padding))
	padded = append(padded, record[:extensionsEnd]...)
	padded = append(padded, padding...)
	padded = append(padded, record[extensionsEnd:]...)

	added := len(padding)
	binary.BigEndian.PutUint16(padded[3:5], uint16(recordLen+added))
	newHelloLen := helloLen + added
	padded[6] = byte(newHelloLen >> 16)
	padded[7] = byte(newHelloLen >> 8)
	padded[8] = byte(newHelloLen)
	binary.BigEndian.PutUint16(padded[extensionsLenOffset:extensionsLenOffset+2], uint16(extensionsLen+added))

	return padded
}
