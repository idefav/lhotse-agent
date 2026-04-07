package http

import (
	"bufio"
	"net/textproto"
	"strings"
	"testing"
)

func TestReadResponseMarksUpgrade(t *testing.T) {
	raw := "HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: websocket\r\n\r\n"
	resp, err := ReadResponse(textproto.NewReader(bufio.NewReader(strings.NewReader(raw))), &Request{})
	if err != nil {
		t.Fatalf("ReadResponse() error = %v", err)
	}
	if !resp.Upgraded {
		t.Fatal("Upgraded = false, want true")
	}
	if resp.UpgradeType != "websocket" {
		t.Fatalf("UpgradeType = %q, want %q", resp.UpgradeType, "websocket")
	}
	if resp.Close {
		t.Fatal("Close = true, want false")
	}
}

func TestReadResponseCloseHeaderSetsClose(t *testing.T) {
	raw := "HTTP/1.1 200 OK\r\nConnection: close\r\nContent-Length: 0\r\n\r\n"
	resp, err := ReadResponse(textproto.NewReader(bufio.NewReader(strings.NewReader(raw))), &Request{})
	if err != nil {
		t.Fatalf("ReadResponse() error = %v", err)
	}
	if !resp.Close {
		t.Fatal("Close = false, want true")
	}
	if resp.Upgraded {
		t.Fatal("Upgraded = true, want false")
	}
}

func TestReadResponseMissingContentLengthUsesUnknownLength(t *testing.T) {
	raw := "HTTP/1.1 200 OK\r\nConnection: close\r\n\r\n"
	resp, err := ReadResponse(textproto.NewReader(bufio.NewReader(strings.NewReader(raw))), &Request{})
	if err != nil {
		t.Fatalf("ReadResponse() error = %v", err)
	}
	if resp.ContentLength != -1 {
		t.Fatalf("ContentLength = %d, want -1", resp.ContentLength)
	}
}
