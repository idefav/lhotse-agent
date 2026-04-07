package http

import (
	"bufio"
	"net/textproto"
	"strings"
	"testing"
)

func TestReadRequestGETHostWithoutPort(t *testing.T) {
	raw := "GET / HTTP/1.1\r\nHost: example.com\r\n\r\n"
	req, err := ReadRequest(textproto.NewReader(bufio.NewReader(strings.NewReader(raw))))
	if err != nil {
		t.Fatalf("ReadRequest() error = %v", err)
	}

	if req.Host != "example.com" {
		t.Fatalf("Host = %q, want %q", req.Host, "example.com")
	}
	if req.Authority != "example.com" {
		t.Fatalf("Authority = %q, want %q", req.Authority, "example.com")
	}
	if req.Port != 80 {
		t.Fatalf("Port = %d, want 80", req.Port)
	}
	if req.TargetDomain() != "example.com" {
		t.Fatalf("TargetDomain() = %q, want %q", req.TargetDomain(), "example.com")
	}
}

func TestReadRequestCONNECTDefaultsTo443(t *testing.T) {
	raw := "CONNECT api.openai.com:443 HTTP/1.1\r\nHost: api.openai.com:443\r\n\r\n"
	req, err := ReadRequest(textproto.NewReader(bufio.NewReader(strings.NewReader(raw))))
	if err != nil {
		t.Fatalf("ReadRequest() error = %v", err)
	}

	if req.Authority != "api.openai.com:443" {
		t.Fatalf("Authority = %q, want %q", req.Authority, "api.openai.com:443")
	}
	if req.Port != 443 {
		t.Fatalf("Port = %d, want 443", req.Port)
	}
	if req.TargetDomain() != "api.openai.com" {
		t.Fatalf("TargetDomain() = %q, want %q", req.TargetDomain(), "api.openai.com")
	}
}

func TestReadRequestAbsoluteFormKeepsExplicitPort(t *testing.T) {
	raw := "GET http://example.com:8080/healthz HTTP/1.1\r\nHost: example.com:8080\r\n\r\n"
	req, err := ReadRequest(textproto.NewReader(bufio.NewReader(strings.NewReader(raw))))
	if err != nil {
		t.Fatalf("ReadRequest() error = %v", err)
	}

	if req.Port != 8080 {
		t.Fatalf("Port = %d, want 8080", req.Port)
	}
	if req.TargetDomain() != "example.com" {
		t.Fatalf("TargetDomain() = %q, want %q", req.TargetDomain(), "example.com")
	}
}

func TestReadRequestMissingContentLengthUsesUnknownLength(t *testing.T) {
	raw := "POST /upload HTTP/1.1\r\nHost: example.com\r\nTransfer-Encoding: chunked\r\n\r\n"
	req, err := ReadRequest(textproto.NewReader(bufio.NewReader(strings.NewReader(raw))))
	if err != nil {
		t.Fatalf("ReadRequest() error = %v", err)
	}

	if req.ContentLength != -1 {
		t.Fatalf("ContentLength = %d, want -1", req.ContentLength)
	}
}
