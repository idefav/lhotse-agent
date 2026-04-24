package proxy

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/net/http2"

	"lhotse-agent/cmd/proxy/config"
	"lhotse-agent/cmd/upgrade"
	"lhotse-agent/pkg/log"
	lhotseTLS "lhotse-agent/pkg/protocol/tls"
	"lhotse-agent/pkg/socket"
	"lhotse-agent/pkg/tls/mitm"
	"lhotse-agent/util"
)

func (o *OutboundServer) Startup() error {
	ln, err := upgrade.Upgrade.Listen("tcp", ":"+strconv.Itoa(int(o.Port)))
	if err != nil {
		return err
	}
	util.GO(func() {
		_ = o.proc(ln)
	})
	return nil
}

func (o *OutboundServer) proc(ln net.Listener) error {
	for {
		rawConn, err := ln.Accept()
		if err != nil {
			return err
		}

		util.GO(func() {
			conn := rawConn
			defer conn.Close()
			atomic.AddInt32(&o.NumOpen, 1)
			defer atomic.AddInt32(&o.NumOpen, -1)

			dstHost := ""
			if tcpConn, ok := conn.(*net.TCPConn); ok {
				if _, host, newConn, err := socket.GetOriginalDst(tcpConn); err == nil {
					dstHost = host
					if newConn != nil {
						conn = newConn
						defer newConn.Close()
					}
				}
			}

			for {
				conn.SetReadDeadline(time.Now().Add(o.IdleTimeOut))
				reader := bufio.NewReaderSize(conn, lhotseTLS.ClientHelloPeekBufferSize)
				peek, err := reader.Peek(5)
				if err != nil {
					return
				}
				header := string(peek)

				if isHTTPRequestPrefix(header) {
					if dstHost == "" {
						dstHost = "192.168.0.105:28080"
					}
					logTCPConnection("outbound", conn, dstHost)
					if err := o.HttpProc(conn, reader, dstHost); err != nil {
						return
					}
					continue
				}

				if dstHost == "" {
					dstHost = "192.168.0.105:28081"
				}

				serverName, isTLS, err := lhotseTLS.PeekClientHelloServerName(reader)
				if err == nil && isTLS && o.shouldMITM(serverName, dstHost) {
					if err := enforceDomainPolicy(o.Cfg, outboundDirection(), conn, normalizeHost(serverName), dstHost); err != nil {
						return
					}
					if err := o.mitmTLSProc(conn, reader, dstHost, serverName); err != nil {
						log.Warnf("mitm proxy failed, falling back to passthrough target=%s sni=%s: %v", dstHost, serverName, err)
						if fallbackErr := o.proxyTLSPassthrough(conn, reader, dstHost); fallbackErr != nil {
							log.Errorf("mitm fallback passthrough failed target=%s sni=%s: %v", dstHost, serverName, fallbackErr)
						}
					}
					return
				}

				if err := enforceRawDomainPolicy(o.Cfg, outboundDirection(), conn, reader, dstHost); err != nil {
					return
				}
				if err := o.proxyTLSPassthrough(conn, reader, dstHost); err != nil {
					return
				}
				return
			}
		})
	}
}

func (o *OutboundServer) shouldMITM(serverName, dstHost string) bool {
	if !o.Cfg.MITMEnabled || o.CertStore == nil || o.RuntimeConfig == nil {
		return false
	}
	host := normalizeHost(serverName)
	if host == "" {
		host = extractHost(dstHost)
	}
	return o.RuntimeConfig.MITMEnabledForHost(host)
}

type mitmProtocol int

const (
	mitmProtocolHTTP1 mitmProtocol = iota + 1
	mitmProtocolHTTP2
	mitmProtocolGenericTLS
)

const http2ClientPreface = "PRI * HTTP/2.0\r\n\r\nSM\r\n\r\n"

func (o *OutboundServer) mitmTLSProc(conn net.Conn, reader *bufio.Reader, dstHost, fallbackHost string) error {
	preRead, err := reader.Peek(reader.Buffered())
	if err != nil && len(preRead) == 0 {
		return fmt.Errorf("capture peek buffer: %w", err)
	}

	serverName := normalizeHost(fallbackHost)
	var fallbackCert *tls.Certificate
	if serverName != "" {
		fallbackCert, err = o.CertStore.GetCertificate(&tls.ClientHelloInfo{ServerName: serverName})
		if err != nil {
			return fmt.Errorf("prepare mitm certificate: %w", err)
		}
	}

	bufConn := mitm.NewBufferedConn(conn, preRead)
	tlsConn := tls.Server(bufConn, &tls.Config{
		GetCertificate: func(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
			if normalizeHost(hello.ServerName) == "" && fallbackCert != nil {
				return fallbackCert, nil
			}
			return o.CertStore.GetCertificate(hello)
		},
		MinVersion: tls.VersionTLS12,
		NextProtos: []string{"h2", "http/1.1"},
	})
	if err := tlsConn.Handshake(); err != nil {
		return fmt.Errorf("mitm tls handshake: %w", err)
	}
	defer tlsConn.Close()

	serverName = normalizeHost(tlsConn.ConnectionState().ServerName)
	if serverName == "" {
		serverName = normalizeHost(fallbackHost)
	}

	plaintextReader := bufio.NewReaderSize(tlsConn, lhotseTLS.ClientHelloPeekBufferSize)
	protocol, err := detectMITMProtocol(tlsConn, plaintextReader, tlsConn.ConnectionState().NegotiatedProtocol)
	if err != nil {
		return fmt.Errorf("detect mitm application protocol: %w", err)
	}

	handler := o.newMITMHandler(dstHost, serverName)
	defer handler.CloseIdleConnections()

	switch protocol {
	case mitmProtocolHTTP2:
		preRead, err := plaintextReader.Peek(plaintextReader.Buffered())
		if err != nil && len(preRead) == 0 {
			return fmt.Errorf("buffer http2 pre-read bytes: %w", err)
		}
		plaintextConn := mitm.NewBufferedConn(tlsConn, preRead)
		var h2Server http2.Server
		h2Server.ServeConn(plaintextConn, &http2.ServeConnOpts{
			BaseConfig: &http.Server{
				Handler:           handler,
				ReadHeaderTimeout: o.IdleTimeOut,
				IdleTimeout:       o.IdleTimeOut,
			},
			Context: context.Background(),
			Handler: handler,
		})
		return nil
	case mitmProtocolHTTP1:
		preRead, err := plaintextReader.Peek(plaintextReader.Buffered())
		if err != nil && len(preRead) == 0 {
			return fmt.Errorf("buffer http1 pre-read bytes: %w", err)
		}
		plaintextConn := mitm.NewBufferedConn(tlsConn, preRead)
		httpServer := &http.Server{
			Handler:           handler,
			ReadHeaderTimeout: o.IdleTimeOut,
			IdleTimeout:       o.IdleTimeOut,
		}
		listener := &singleConnListener{conn: plaintextConn}
		err = httpServer.Serve(listener)
		if err != nil && !errors.Is(err, net.ErrClosed) && !strings.Contains(err.Error(), "closed network connection") {
			return fmt.Errorf("serve mitm http connection: %w", err)
		}
		return nil
	default:
		return o.proxyMITMTLSConnection(tlsConn, plaintextReader, dstHost, serverName)
	}
}

func (o *OutboundServer) proxyTLSPassthrough(conn net.Conn, reader *bufio.Reader, dstHost string) error {
	logTLSConnection("outbound", conn, dstHost, reader)
	destConn, err := net.Dial("tcp", dstHost)
	if err != nil {
		return err
	}
	defer destConn.Close()
	return proxyRawConnection(conn, destConn, reader)
}

func (o *OutboundServer) newMITMHandler(dstHost, serverName string) *mitmHandler {
	return &mitmHandler{
		outbound:   o,
		dstHost:    dstHost,
		serverName: serverName,
		transports: newMITMTransportCache(o.UpstreamRootCAs),
	}
}

type mitmHandler struct {
	outbound   *OutboundServer
	dstHost    string
	serverName string
	transports *mitmTransportCache
}

func (h *mitmHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	targetHost := normalizeHost(r.Host)
	if targetHost == "" {
		targetHost = h.serverName
	}
	if h.serverName != "" && targetHost != "" && targetHost != h.serverName {
		http.Error(w, "sni_host_mismatch", http.StatusMisdirectedRequest)
		return
	}

	userID, err := h.outbound.applyCredentialInjection(targetHost, r.Header.Get, r.Header.Set)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}

	proxy := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.Out.URL.Scheme = "https"
			pr.Out.URL.Host = h.dstHost
			pr.Out.Host = r.Host
			pr.Out = pr.Out.WithContext(withMITMServerName(pr.Out.Context(), targetHost))
			pr.SetXForwarded()
		},
		Transport: &mitmRoundTripper{
			targetAddr: h.dstHost,
			transports: h.transports,
		},
		ErrorHandler: func(rw http.ResponseWriter, req *http.Request, proxyErr error) {
			http.Error(rw, proxyErr.Error(), http.StatusBadGateway)
		},
	}
	log.Infof("mitm request host=%s target=%s user_id=%s proto=%s", targetHost, h.dstHost, userID, r.Proto)
	proxy.ServeHTTP(w, r)
}

func (h *mitmHandler) CloseIdleConnections() {
	h.transports.CloseIdleConnections()
}

type mitmServerNameContextKey struct{}

func withMITMServerName(ctx context.Context, serverName string) context.Context {
	return context.WithValue(ctx, mitmServerNameContextKey{}, serverName)
}

func mitmServerNameFromContext(ctx context.Context) string {
	serverName, _ := ctx.Value(mitmServerNameContextKey{}).(string)
	return serverName
}

type mitmTransportCache struct {
	rootCAs    *x509.CertPool
	mu         sync.Mutex
	transports map[string]*http.Transport
}

var newMITMHTTPTransport = func(serverName string, rootCAs *x509.CertPool) *http.Transport {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.ForceAttemptHTTP2 = true
	transport.TLSClientConfig = &tls.Config{
		MinVersion: tls.VersionTLS12,
		ServerName: serverName,
		RootCAs:    rootCAs,
	}
	return transport
}

func newMITMTransportCache(rootCAs *x509.CertPool) *mitmTransportCache {
	return &mitmTransportCache{
		rootCAs:    rootCAs,
		transports: make(map[string]*http.Transport),
	}
}

func (c *mitmTransportCache) Transport(serverName string) *http.Transport {
	c.mu.Lock()
	defer c.mu.Unlock()

	if transport, ok := c.transports[serverName]; ok {
		return transport
	}

	transport := newMITMHTTPTransport(serverName, c.rootCAs)
	c.transports[serverName] = transport
	return transport
}

func (c *mitmTransportCache) CloseIdleConnections() {
	c.mu.Lock()
	defer c.mu.Unlock()

	for _, transport := range c.transports {
		transport.CloseIdleConnections()
	}
}

func (o *OutboundServer) Shutdown() error {
	for o.NumOpen > 0 {
		time.Sleep(time.Second)
	}
	return nil
}

type singleConnListener struct {
	conn      net.Conn
	mu        sync.Mutex
	used      bool
	done      chan struct{}
	closeOnce sync.Once
}

func (l *singleConnListener) Accept() (net.Conn, error) {
	l.mu.Lock()
	if l.done == nil {
		l.done = make(chan struct{})
	}
	if l.used {
		done := l.done
		l.mu.Unlock()
		<-done
		return nil, net.ErrClosed
	}
	l.used = true
	closeFn := func() {
		l.closeOnce.Do(func() {
			close(l.done)
		})
	}
	if tlsConn, ok := l.conn.(*tls.Conn); ok {
		l.mu.Unlock()
		return &singleTLSConn{Conn: tlsConn, close: closeFn}, nil
	}
	conn := &singleConn{Conn: l.conn, close: closeFn}
	l.mu.Unlock()
	return conn, nil
}

func (l *singleConnListener) Close() error {
	l.mu.Lock()
	if l.done == nil {
		l.done = make(chan struct{})
	}
	l.closeOnce.Do(func() {
		close(l.done)
	})
	l.mu.Unlock()
	return nil
}

func (l *singleConnListener) Addr() net.Addr {
	return l.conn.LocalAddr()
}

type singleConn struct {
	net.Conn
	close func()
}

func (c *singleConn) Close() error {
	err := c.Conn.Close()
	c.close()
	return err
}

type singleTLSConn struct {
	*tls.Conn
	close func()
}

func (c *singleTLSConn) Close() error {
	err := c.Conn.Close()
	c.close()
	return err
}

type mitmRoundTripper struct {
	targetAddr string
	transports *mitmTransportCache
}

func (rt *mitmRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	upstreamReq := req.Clone(req.Context())
	upstreamReq.URL.Scheme = "https"
	upstreamReq.URL.Host = rt.targetAddr

	serverName := mitmServerNameFromContext(req.Context())
	transport := rt.transports.Transport(serverName)
	return transport.RoundTrip(upstreamReq)
}

func isHTTPRequestPrefix(header string) bool {
	return strings.HasPrefix(header, "GET") ||
		strings.HasPrefix(header, "POST") ||
		strings.HasPrefix(header, "HEAD") ||
		strings.HasPrefix(header, "CONNECT") ||
		strings.HasPrefix(header, "OPTIONS") ||
		strings.HasPrefix(header, "PUT") ||
		strings.HasPrefix(header, "DELETE") ||
		strings.HasPrefix(header, "PATCH")
}

func detectMITMProtocol(conn net.Conn, reader *bufio.Reader, negotiatedProtocol string) (mitmProtocol, error) {
	switch negotiatedProtocol {
	case "h2":
		return mitmProtocolHTTP2, nil
	case "http/1.1":
		return mitmProtocolHTTP1, nil
	}

	const sniffTimeout = 50 * time.Millisecond

	clearReadDeadline(conn)
	_ = conn.SetReadDeadline(time.Now().Add(sniffTimeout))
	peek, err := reader.Peek(len(http2ClientPreface))
	clearReadDeadline(conn)
	if err != nil {
		var netErr net.Error
		switch {
		case errors.As(err, &netErr) && netErr.Timeout():
			return mitmProtocolGenericTLS, nil
		case errors.Is(err, bufio.ErrBufferFull), errors.Is(err, io.EOF):
			// Partial application data is still useful for best-effort protocol detection.
		default:
			return 0, err
		}
	}
	if len(peek) == 0 {
		return mitmProtocolGenericTLS, nil
	}
	if len(peek) >= len(http2ClientPreface) && string(peek[:len(http2ClientPreface)]) == http2ClientPreface {
		return mitmProtocolHTTP2, nil
	}
	if probeLen := min(len(peek), 5); probeLen > 0 && isHTTPRequestPrefix(string(peek[:probeLen])) {
		return mitmProtocolHTTP1, nil
	}

	return mitmProtocolGenericTLS, nil
}

func (o *OutboundServer) proxyMITMTLSConnection(downstream net.Conn, reader *bufio.Reader, dstHost, serverName string) error {
	clearReadDeadline(downstream)

	rawUpstreamConn, err := net.Dial("tcp", dstHost)
	if err != nil {
		return err
	}
	upstreamTLSConn := tls.Client(rawUpstreamConn, &tls.Config{
		MinVersion: tls.VersionTLS12,
		RootCAs:    o.UpstreamRootCAs,
		ServerName: serverName,
	})
	if err := upstreamTLSConn.Handshake(); err != nil {
		_ = rawUpstreamConn.Close()
		return err
	}
	defer upstreamTLSConn.Close()

	return proxyRawConnection(downstream, upstreamTLSConn, reader)
}

func loadMITMUpstreamRootCAs(cfg *config.Config) (*x509.CertPool, error) {
	if cfg == nil {
		return nil, nil
	}
	caPath := strings.TrimSpace(cfg.UpstreamCAFile)
	if caPath == "" {
		caPath = strings.TrimSpace(os.Getenv("SSL_CERT_FILE"))
	}
	if caPath == "" {
		return nil, nil
	}

	extraPEM, err := os.ReadFile(caPath)
	if err != nil {
		return nil, fmt.Errorf("read upstream CA file %q: %w", caPath, err)
	}
	if len(strings.TrimSpace(string(extraPEM))) == 0 {
		return nil, fmt.Errorf("upstream CA file %q is empty", caPath)
	}

	pool, err := x509.SystemCertPool()
	if err != nil {
		return nil, fmt.Errorf("load system cert pool: %w", err)
	}
	if pool == nil {
		pool = x509.NewCertPool()
	}
	if !pool.AppendCertsFromPEM(extraPEM) {
		return nil, fmt.Errorf("parse upstream CA file %q: no certificates found", caPath)
	}
	return pool, nil
}
