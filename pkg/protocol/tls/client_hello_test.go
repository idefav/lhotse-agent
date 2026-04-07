package tls

import (
	"bufio"
	"bytes"
	"crypto/tls"
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
