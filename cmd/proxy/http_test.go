package proxy

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"lhotse-agent/cmd/proxy/config"
	"lhotse-agent/cmd/proxy/domainpolicy"
	lhotseHttp "lhotse-agent/pkg/protocol/http"
	"net"
	"strings"
	"testing"
	"time"
)

func TestProxyRawConnectionClearsDeadlineAndPreservesHalfClose(t *testing.T) {
	clientConn, proxyDown := newTCPPair(t)
	defer clientConn.Close()
	defer proxyDown.Close()

	upstreamProxy, upstreamServer := newTCPPair(t)
	defer upstreamProxy.Close()
	defer upstreamServer.Close()

	if err := proxyDown.SetReadDeadline(time.Now().Add(25 * time.Millisecond)); err != nil {
		t.Fatalf("SetReadDeadline() error = %v", err)
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- proxyRawConnection(proxyDown, upstreamProxy, bufio.NewReader(proxyDown))
	}()

	time.Sleep(60 * time.Millisecond)

	if _, err := clientConn.Write([]byte("hello")); err != nil {
		t.Fatalf("client write error = %v", err)
	}
	serverBuf := make([]byte, 5)
	if _, err := io.ReadFull(upstreamServer, serverBuf); err != nil {
		t.Fatalf("server read error = %v", err)
	}
	if got := string(serverBuf); got != "hello" {
		t.Fatalf("server saw %q, want %q", got, "hello")
	}

	if err := clientConn.CloseWrite(); err != nil {
		t.Fatalf("client CloseWrite() error = %v", err)
	}

	if _, err := upstreamServer.Write([]byte("world")); err != nil {
		t.Fatalf("server write error = %v", err)
	}
	clientBuf := make([]byte, 5)
	if _, err := io.ReadFull(clientConn, clientBuf); err != nil {
		t.Fatalf("client read error = %v", err)
	}
	if got := string(clientBuf); got != "world" {
		t.Fatalf("client saw %q, want %q", got, "world")
	}

	if err := upstreamServer.CloseWrite(); err != nil {
		t.Fatalf("server CloseWrite() error = %v", err)
	}

	select {
	case err := <-errCh:
		if !errors.Is(err, errProxyConnectionDone) {
			t.Fatalf("proxyRawConnection() error = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("proxyRawConnection() did not finish after both sides half-closed")
	}
}

func TestSingleConnListenerWaitsForAcceptedConnClose(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()

	listener := &singleConnListener{conn: server}
	accepted, err := listener.Accept()
	if err != nil {
		t.Fatalf("first Accept() error = %v", err)
	}

	errCh := make(chan error, 1)
	go func() {
		_, acceptErr := listener.Accept()
		errCh <- acceptErr
	}()

	select {
	case err := <-errCh:
		t.Fatalf("second Accept() returned before conn close: %v", err)
	case <-time.After(50 * time.Millisecond):
	}

	if err := accepted.Close(); err != nil {
		t.Fatalf("accepted Close() error = %v", err)
	}

	select {
	case err := <-errCh:
		if !errors.Is(err, net.ErrClosed) {
			t.Fatalf("second Accept() error = %v, want %v", err, net.ErrClosed)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("second Accept() did not return after conn close")
	}
}

func TestMITMTransportCacheReusesTransportPerServerName(t *testing.T) {
	cache := newMITMTransportCache(nil)

	first := cache.Transport("api.example.com")
	second := cache.Transport("api.example.com")
	other := cache.Transport("other.example.com")

	if first != second {
		t.Fatal("Transport() did not reuse transport for the same server name")
	}
	if first == other {
		t.Fatal("Transport() reused transport for a different server name")
	}
	if first.TLSClientConfig == nil {
		t.Fatal("first transport TLSClientConfig is nil")
	}
	if first.TLSClientConfig.ServerName != "api.example.com" {
		t.Fatalf("first transport ServerName = %q, want api.example.com", first.TLSClientConfig.ServerName)
	}
	cache.CloseIdleConnections()
}

func TestEnforceDomainPolicyWritesHTTPForbidden(t *testing.T) {
	policy, err := domainpolicy.Compile(domainpolicy.ConfigDocument{Rules: []domainpolicy.RuleDocument{
		{
			Direction: domainpolicy.DirectionOutbound,
			Mode:      domainpolicy.ModeDefaultDeny,
			AllowList: []string{"api.example.com"},
		},
	}})
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}

	client, proxyDown := net.Pipe()
	defer client.Close()
	defer proxyDown.Close()

	cfg := &config.Config{
		DomainPolicy: domainpolicy.NewStaticManager(policy, domainpolicy.ScopeOutbound),
	}
	done := make(chan error, 1)
	go func() {
		done <- enforceDomainPolicy(cfg, domainpolicy.DirectionOutbound, proxyDown, "blocked.example.com", "203.0.113.10:443")
	}()

	buf := make([]byte, 256)
	n, err := client.Read(buf)
	if err != nil {
		t.Fatalf("client Read() error = %v", err)
	}
	if got := string(buf[:n]); !strings.Contains(got, "403 Forbidden") {
		t.Fatalf("response = %q, want 403 Forbidden", got)
	}
	if err := <-done; !errors.Is(err, errProxyConnectionDone) {
		t.Fatalf("enforceDomainPolicy() error = %v", err)
	}
}

func TestProxyHTTPRequestConnectTunnelsTraffic(t *testing.T) {
	clientConn, proxyDown := newTCPPair(t)
	defer clientConn.Close()
	defer proxyDown.Close()

	targetLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	defer targetLn.Close()

	acceptedCh := make(chan *net.TCPConn, 1)
	errCh := make(chan error, 1)
	go func() {
		conn, acceptErr := targetLn.Accept()
		if acceptErr != nil {
			errCh <- acceptErr
			return
		}
		acceptedCh <- conn.(*net.TCPConn)
	}()

	proxyDone := make(chan error, 1)
	go func() {
		req := &lhotseHttp.Request{
			Method:    "CONNECT",
			Proto:     "HTTP/1.1",
			Authority: targetLn.Addr().String(),
			Header:    lhotseHttp.Header{},
		}
		proxyDone <- proxyHTTPRequest("outbound", proxyDown, bufio.NewReader(proxyDown), req, targetLn.Addr().String(), targetLn.Addr().String(), "lhotse-agent")
	}()

	targetConn := <-acceptedCh
	defer targetConn.Close()

	headers := make([]byte, len("HTTP/1.1 200 Connection Established\r\nServer: lhotse-agent\r\n\r\n"))
	if _, err := io.ReadFull(clientConn, headers); err != nil {
		t.Fatalf("read CONNECT response error = %v", err)
	}
	if got := string(headers); !strings.Contains(got, "200 Connection Established") {
		t.Fatalf("CONNECT response = %q, want 200 established", got)
	}

	if _, err := clientConn.Write([]byte("abc")); err != nil {
		t.Fatalf("client write error = %v", err)
	}
	buf := make([]byte, 3)
	if _, err := io.ReadFull(targetConn, buf); err != nil {
		t.Fatalf("target read error = %v", err)
	}
	if got := string(buf); got != "abc" {
		t.Fatalf("target saw %q, want %q", got, "abc")
	}

	if _, err := targetConn.Write([]byte("xyz")); err != nil {
		t.Fatalf("target write error = %v", err)
	}
	if _, err := io.ReadFull(clientConn, buf); err != nil {
		t.Fatalf("client read error = %v", err)
	}
	if got := string(buf); got != "xyz" {
		t.Fatalf("client saw %q, want %q", got, "xyz")
	}

	if err := clientConn.CloseWrite(); err != nil {
		t.Fatalf("client CloseWrite() error = %v", err)
	}
	if err := targetConn.CloseWrite(); err != nil {
		t.Fatalf("target CloseWrite() error = %v", err)
	}

	select {
	case err := <-proxyDone:
		if !errors.Is(err, errProxyConnectionDone) {
			t.Fatalf("proxyHTTPRequest() error = %v", err)
		}
	case err := <-errCh:
		t.Fatalf("accept goroutine error = %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("CONNECT tunnel did not finish")
	}
}

func TestProxyHTTPBodyStreamsConnectionCloseDelimitedResponse(t *testing.T) {
	response := &lhotseHttp.Response{
		StatusCode:    200,
		ContentLength: -1,
		Close:         true,
	}
	request := &lhotseHttp.Request{Method: "GET"}
	reader := bufio.NewReader(strings.NewReader("stream-body"))
	var dst bytes.Buffer

	if err := proxyHTTPBody(&dst, reader, request, response); err != nil {
		t.Fatalf("proxyHTTPBody() error = %v", err)
	}
	if got := dst.String(); got != "stream-body" {
		t.Fatalf("body = %q, want %q", got, "stream-body")
	}
}

func TestWriteHTTPRequestCopiesChunkedBody(t *testing.T) {
	upstream, peer := net.Pipe()
	defer upstream.Close()
	defer peer.Close()

	request := &lhotseHttp.Request{
		Method:           "POST",
		RequestURI:       "/stream",
		Proto:            "HTTP/1.1",
		Header:           lhotseHttp.Header{"Transfer-Encoding": []string{"chunked"}},
		TransferEncoding: []string{"chunked"},
		ContentLength:    -1,
	}
	reader := bufio.NewReader(strings.NewReader("4\r\ntest\r\n0\r\n\r\n"))

	done := make(chan error, 1)
	go func() {
		done <- writeHTTPRequest(upstream, reader, request)
	}()

	want := "POST /stream HTTP/1.1\r\nTransfer-Encoding: chunked\r\n\r\n4\r\ntest\r\n0\r\n\r\n"
	got := make([]byte, len(want))
	if _, err := io.ReadFull(peer, got); err != nil {
		t.Fatalf("peer ReadFull() error = %v", err)
	}
	if string(got) != want {
		t.Fatalf("forwarded request = %q, want %q", string(got), want)
	}
	if err := <-done; err != nil {
		t.Fatalf("writeHTTPRequest() error = %v", err)
	}
}

func newTCPPair(t *testing.T) (*net.TCPConn, *net.TCPConn) {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	defer ln.Close()

	acceptCh := make(chan *net.TCPConn, 1)
	errCh := make(chan error, 1)
	go func() {
		conn, acceptErr := ln.Accept()
		if acceptErr != nil {
			errCh <- acceptErr
			return
		}
		acceptCh <- conn.(*net.TCPConn)
	}()

	clientRaw, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}

	select {
	case acceptErr := <-errCh:
		t.Fatalf("Accept() error = %v", acceptErr)
	case serverConn := <-acceptCh:
		return clientRaw.(*net.TCPConn), serverConn
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for accept")
	}
	return nil, nil
}
