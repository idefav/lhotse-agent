package proxy

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"lhotse-agent/cmd/proxy/config"
	"lhotse-agent/pkg/credential"
	lhotseTLS "lhotse-agent/pkg/protocol/tls"
	"lhotse-agent/pkg/tls/mitm"
)

func TestMITMTLSProcInjectsCredentialsOverHTTP11(t *testing.T) {
	store, roots := newMITMStore(t, "mitm-root")
	vault := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/internal/credentials/resolve":
			_, _ = w.Write([]byte(`{"credential":{"header_name":"Authorization","header_prefix":"Bearer ","value":"secret"}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer vault.Close()

	var (
		mu            sync.Mutex
		authHeader    string
		upstreamProto string
	)
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		authHeader = r.Header.Get("Authorization")
		upstreamProto = r.Proto
		mu.Unlock()
		_, _ = w.Write([]byte("mitm-http1"))
	}))
	defer upstream.Close()

	restore := overrideMITMTransportFactory(func(serverName string, rootCAs *x509.CertPool) *http.Transport {
		transport := http.DefaultTransport.(*http.Transport).Clone()
		transport.ForceAttemptHTTP2 = true
		transport.TLSClientConfig = &tls.Config{
			MinVersion:         tls.VersionTLS12,
			ServerName:         serverName,
			InsecureSkipVerify: true,
		}
		return transport
	})
	defer restore()

	outbound := &OutboundServer{
		IdleTimeOut:  2 * time.Second,
		Cfg:          &config.Config{AgentID: "agent-1"},
		CertStore:    store,
		CredResolver: credential.NewResolver(vault.URL),
	}
	proxyAddr, wait := startMITMServer(t, func(conn net.Conn) error {
		return outbound.mitmTLSProc(conn, bufio.NewReaderSize(conn, lhotseTLS.ClientHelloPeekBufferSize), upstream.Listener.Addr().String(), "api.example.com")
	})
	defer wait()

	var issuer string
	client := &http.Client{
		Transport: &http.Transport{
			ForceAttemptHTTP2: false,
			DialTLSContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				conn, err := tls.Dial(network, proxyAddr, &tls.Config{
					MinVersion: tls.VersionTLS12,
					RootCAs:    roots,
					ServerName: "api.example.com",
					NextProtos: []string{"http/1.1"},
				})
				if err != nil {
					return nil, err
				}
				issuer = conn.ConnectionState().PeerCertificates[0].Issuer.CommonName
				return conn, nil
			},
		},
	}
	defer client.CloseIdleConnections()

	resp, err := client.Get("https://api.example.com/test")
	if err != nil {
		t.Fatalf("client.Get() error = %v", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("ReadAll() error = %v", err)
	}
	if string(body) != "mitm-http1" {
		t.Fatalf("body = %q, want %q", string(body), "mitm-http1")
	}
	if resp.ProtoMajor != 1 {
		t.Fatalf("resp.ProtoMajor = %d, want 1", resp.ProtoMajor)
	}
	if issuer != "mitm-root" {
		t.Fatalf("peer issuer = %q, want %q", issuer, "mitm-root")
	}

	mu.Lock()
	defer mu.Unlock()
	if authHeader != "Bearer secret" {
		t.Fatalf("Authorization = %q, want %q", authHeader, "Bearer secret")
	}
	if upstreamProto == "" {
		t.Fatal("upstream request proto was not recorded")
	}
}

func TestMITMTLSProcSupportsHTTP2(t *testing.T) {
	store, roots := newMITMStore(t, "mitm-root")

	upstream := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(r.Proto))
	}))
	upstream.EnableHTTP2 = true
	upstream.StartTLS()
	defer upstream.Close()

	restore := overrideMITMTransportFactory(func(serverName string, rootCAs *x509.CertPool) *http.Transport {
		transport := http.DefaultTransport.(*http.Transport).Clone()
		transport.ForceAttemptHTTP2 = true
		transport.TLSClientConfig = &tls.Config{
			MinVersion:         tls.VersionTLS12,
			ServerName:         serverName,
			InsecureSkipVerify: true,
		}
		return transport
	})
	defer restore()

	outbound := &OutboundServer{
		IdleTimeOut: 2 * time.Second,
		Cfg:         &config.Config{AgentID: "agent-1"},
		CertStore:   store,
	}
	proxyAddr, wait := startMITMServer(t, func(conn net.Conn) error {
		return outbound.mitmTLSProc(conn, bufio.NewReaderSize(conn, lhotseTLS.ClientHelloPeekBufferSize), upstream.Listener.Addr().String(), "api.example.com")
	})
	defer wait()

	client := &http.Client{
		Transport: &http.Transport{
			ForceAttemptHTTP2: true,
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, network, proxyAddr)
			},
			TLSClientConfig: &tls.Config{
				MinVersion: tls.VersionTLS12,
				RootCAs:    roots,
				NextProtos: []string{"h2", "http/1.1"},
			},
		},
	}
	defer client.CloseIdleConnections()

	resp, err := client.Get("https://api.example.com/http2")
	if err != nil {
		t.Fatalf("client.Get() error = %v", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("ReadAll() error = %v", err)
	}
	if resp.ProtoMajor != 2 {
		t.Fatalf("resp.ProtoMajor = %d, want 2", resp.ProtoMajor)
	}
	if string(body) == "" {
		t.Fatal("body is empty")
	}
	client.CloseIdleConnections()
}

func TestMITMTLSFailureFallsBackToPassthrough(t *testing.T) {
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("fallback-ok"))
	}))
	defer upstream.Close()

	outbound := &OutboundServer{
		IdleTimeOut: 2 * time.Second,
		CertStore:   mitm.NewCertStore(""),
	}
	proxyAddr, wait := startMITMServer(t, func(conn net.Conn) error {
		reader := bufio.NewReaderSize(conn, lhotseTLS.ClientHelloPeekBufferSize)
		if err := outbound.mitmTLSProc(conn, reader, upstream.Listener.Addr().String(), "api.example.com"); err == nil {
			t.Fatal("mitmTLSProc() error = nil, want non-nil")
		}
		return outbound.proxyTLSPassthrough(conn, reader, upstream.Listener.Addr().String())
	})
	defer wait()

	client := &http.Client{
		Transport: &http.Transport{
			DialTLSContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				return tls.Dial(network, proxyAddr, &tls.Config{
					MinVersion:         tls.VersionTLS12,
					ServerName:         "api.example.com",
					InsecureSkipVerify: true,
				})
			},
		},
	}
	defer client.CloseIdleConnections()

	resp, err := client.Get("https://api.example.com/fallback")
	if err != nil {
		t.Fatalf("client.Get() error = %v", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("ReadAll() error = %v", err)
	}
	if string(body) != "fallback-ok" {
		t.Fatalf("body = %q, want %q", string(body), "fallback-ok")
	}
}

func TestLoadMITMUpstreamRootCAsUsesConfiguredFile(t *testing.T) {
	_, caPEM := newTestCA(t, "upstream-root")
	caPath := writeTempPEMFile(t, "upstream-ca.pem", caPEM)

	pool, err := loadMITMUpstreamRootCAs(&config.Config{UpstreamCAFile: caPath})
	if err != nil {
		t.Fatalf("loadMITMUpstreamRootCAs() error = %v", err)
	}
	if pool == nil {
		t.Fatal("loadMITMUpstreamRootCAs() returned nil pool")
	}
}

func TestLoadMITMUpstreamRootCAsUsesSSLCertFileFallback(t *testing.T) {
	_, caPEM := newTestCA(t, "env-upstream-root")
	caPath := writeTempPEMFile(t, "env-upstream-ca.pem", caPEM)

	original, hadOriginal := os.LookupEnv("SSL_CERT_FILE")
	if err := os.Setenv("SSL_CERT_FILE", caPath); err != nil {
		t.Fatalf("Setenv() error = %v", err)
	}
	t.Cleanup(func() {
		if hadOriginal {
			_ = os.Setenv("SSL_CERT_FILE", original)
			return
		}
		_ = os.Unsetenv("SSL_CERT_FILE")
	})

	pool, err := loadMITMUpstreamRootCAs(&config.Config{})
	if err != nil {
		t.Fatalf("loadMITMUpstreamRootCAs() error = %v", err)
	}
	if pool == nil {
		t.Fatal("loadMITMUpstreamRootCAs() returned nil pool")
	}
}

func TestLoadMITMUpstreamRootCAsRejectsInvalidPEM(t *testing.T) {
	caPath := writeTempPEMFile(t, "invalid-upstream-ca.pem", []byte("not-a-cert"))

	_, err := loadMITMUpstreamRootCAs(&config.Config{UpstreamCAFile: caPath})
	if err == nil {
		t.Fatal("loadMITMUpstreamRootCAs() error = nil, want non-nil")
	}
}

func TestMITMTLSProcUsesConfiguredUpstreamCAFile(t *testing.T) {
	store, roots := newMITMStore(t, "mitm-root")
	upstreamCA, upstreamCAPEM := newTestCA(t, "private-upstream-root")
	upstreamTLS := newTLSServerWithCA(t, upstreamCA, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("private-upstream-ok"))
	}))
	defer upstreamTLS.Close()
	upstreamCAPath := writeTempPEMFile(t, "private-upstream-root.pem", upstreamCAPEM)

	outbound := &OutboundServer{
		IdleTimeOut: 2 * time.Second,
		Cfg: &config.Config{
			AgentID:        "agent-1",
			UpstreamCAFile: upstreamCAPath,
		},
		CertStore:       store,
		UpstreamRootCAs: mustLoadUpstreamRootCAs(t, &config.Config{UpstreamCAFile: upstreamCAPath}),
	}

	proxyAddr, wait := startMITMServer(t, func(conn net.Conn) error {
		return outbound.mitmTLSProc(conn, bufio.NewReaderSize(conn, lhotseTLS.ClientHelloPeekBufferSize), upstreamTLS.Listener.Addr().String(), "api.example.com")
	})
	defer wait()

	client := &http.Client{
		Transport: &http.Transport{
			ForceAttemptHTTP2: false,
			DialTLSContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				return tls.Dial(network, proxyAddr, &tls.Config{
					MinVersion: tls.VersionTLS12,
					RootCAs:    roots,
					ServerName: "api.example.com",
					NextProtos: []string{"http/1.1"},
				})
			},
		},
	}
	defer client.CloseIdleConnections()

	resp, err := client.Get("https://api.example.com/private")
	if err != nil {
		t.Fatalf("client.Get() error = %v", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("ReadAll() error = %v", err)
	}
	if string(body) != "private-upstream-ok" {
		t.Fatalf("body = %q, want %q", string(body), "private-upstream-ok")
	}
}

func TestMITMTLSProcUsesIPSANForIPTargets(t *testing.T) {
	store, roots := newMITMStore(t, "mitm-root")
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("mitm-ip"))
	}))
	defer upstream.Close()

	restore := overrideMITMTransportFactory(func(serverName string, rootCAs *x509.CertPool) *http.Transport {
		transport := http.DefaultTransport.(*http.Transport).Clone()
		transport.ForceAttemptHTTP2 = true
		transport.TLSClientConfig = &tls.Config{
			MinVersion:         tls.VersionTLS12,
			ServerName:         serverName,
			InsecureSkipVerify: true,
		}
		return transport
	})
	defer restore()

	outbound := &OutboundServer{
		IdleTimeOut: 2 * time.Second,
		Cfg:         &config.Config{AgentID: "agent-1"},
		CertStore:   store,
	}
	proxyAddr, wait := startMITMServer(t, func(conn net.Conn) error {
		return outbound.mitmTLSProc(conn, bufio.NewReaderSize(conn, lhotseTLS.ClientHelloPeekBufferSize), upstream.Listener.Addr().String(), "203.0.113.10")
	})
	defer wait()

	client := &http.Client{
		Transport: &http.Transport{
			ForceAttemptHTTP2: false,
			DialTLSContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				return tls.Dial(network, proxyAddr, &tls.Config{
					MinVersion: tls.VersionTLS12,
					RootCAs:    roots,
					ServerName: "203.0.113.10",
					NextProtos: []string{"http/1.1"},
				})
			},
		},
	}
	defer client.CloseIdleConnections()

	resp, err := client.Get("https://203.0.113.10/test")
	if err != nil {
		t.Fatalf("client.Get() error = %v", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("ReadAll() error = %v", err)
	}
	if string(body) != "mitm-ip" {
		t.Fatalf("body = %q, want %q", string(body), "mitm-ip")
	}
}

func TestMITMTLSProcBridgesNonHTTPTLS(t *testing.T) {
	store, roots := newMITMStore(t, "mitm-root")
	upstreamCA, upstreamCAPEM := newTestCA(t, "generic-upstream-root")
	upstreamCAPath := writeTempPEMFile(t, "generic-upstream-root.pem", upstreamCAPEM)
	echoAddr, waitEcho := startTLSEchoServer(t, upstreamCA, "api.example.com")
	defer waitEcho()

	outbound := &OutboundServer{
		IdleTimeOut: 2 * time.Second,
		Cfg: &config.Config{
			AgentID:        "agent-1",
			UpstreamCAFile: upstreamCAPath,
		},
		CertStore:       store,
		UpstreamRootCAs: mustLoadUpstreamRootCAs(t, &config.Config{UpstreamCAFile: upstreamCAPath}),
	}
	proxyAddr, waitProxy := startMITMServer(t, func(conn net.Conn) error {
		return outbound.mitmTLSProc(conn, bufio.NewReaderSize(conn, lhotseTLS.ClientHelloPeekBufferSize), echoAddr, "api.example.com")
	})
	defer waitProxy()

	clientConn, err := tls.Dial("tcp", proxyAddr, &tls.Config{
		MinVersion: tls.VersionTLS12,
		RootCAs:    roots,
		ServerName: "api.example.com",
	})
	if err != nil {
		t.Fatalf("tls.Dial() error = %v", err)
	}
	defer clientConn.Close()

	if _, err := clientConn.Write([]byte("ping")); err != nil {
		t.Fatalf("client Write() error = %v", err)
	}
	reply := make([]byte, 4)
	if _, err := io.ReadFull(clientConn, reply); err != nil {
		t.Fatalf("client ReadFull() error = %v", err)
	}
	if got := string(reply); got != "ping" {
		t.Fatalf("reply = %q, want %q", got, "ping")
	}
}

func newMITMStore(t *testing.T, commonName string) (*mitm.CertStore, *x509.CertPool) {
	t.Helper()

	caCert, caPEM := newTestCA(t, commonName)
	store := mitm.NewCertStore("")
	store.Reload([]*tls.Certificate{caCert})

	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(caPEM) {
		t.Fatal("AppendCertsFromPEM() = false")
	}
	return store, roots
}

func newTestCA(t *testing.T, commonName string) (*tls.Certificate, []byte) {
	t.Helper()

	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey() error = %v", err)
	}
	serialLimit := new(big.Int).Lsh(big.NewInt(1), 128)
	serial, err := rand.Int(rand.Reader, serialLimit)
	if err != nil {
		t.Fatalf("rand.Int() error = %v", err)
	}

	template := &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			CommonName:   commonName,
			Organization: []string{"Lhotse Proxy Test Root"},
		},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
	}

	der, err := x509.CreateCertificate(rand.Reader, template, template, &priv.PublicKey, priv)
	if err != nil {
		t.Fatalf("CreateCertificate() error = %v", err)
	}
	certPEM := pemEncodeCertificate(der)
	keyPEM, err := pemEncodePrivateKey(priv)
	if err != nil {
		t.Fatalf("pemEncodePrivateKey() error = %v", err)
	}

	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatalf("X509KeyPair() error = %v", err)
	}
	cert.Leaf, err = x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("ParseCertificate() error = %v", err)
	}
	return &cert, certPEM
}

func startMITMServer(t *testing.T, handler func(conn net.Conn) error) (string, func()) {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	errCh := make(chan error, 1)
	go func() {
		defer close(errCh)
		conn, acceptErr := ln.Accept()
		if acceptErr != nil {
			errCh <- acceptErr
			return
		}
		defer conn.Close()
		errCh <- handler(conn)
	}()

	return ln.Addr().String(), func() {
		_ = ln.Close()
		select {
		case err := <-errCh:
			if err != nil && err != errProxyConnectionDone && err != net.ErrClosed {
				t.Fatalf("proxy server error = %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for proxy server")
		}
	}
}

func overrideMITMTransportFactory(factory func(serverName string, rootCAs *x509.CertPool) *http.Transport) func() {
	original := newMITMHTTPTransport
	newMITMHTTPTransport = factory
	return func() {
		newMITMHTTPTransport = original
	}
}

func pemEncodeCertificate(der []byte) []byte {
	return pemEncode("CERTIFICATE", der)
}

func pemEncodePrivateKey(priv any) ([]byte, error) {
	keyBytes, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		return nil, err
	}
	return pemEncode("PRIVATE KEY", keyBytes), nil
}

func pemEncode(blockType string, der []byte) []byte {
	return pem.EncodeToMemory(&pem.Block{Type: blockType, Bytes: der})
}

func writeTempPEMFile(t *testing.T, name string, body []byte) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, body, 0600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	return path
}

func mustLoadUpstreamRootCAs(t *testing.T, cfg *config.Config) *x509.CertPool {
	t.Helper()

	pool, err := loadMITMUpstreamRootCAs(cfg)
	if err != nil {
		t.Fatalf("loadMITMUpstreamRootCAs() error = %v", err)
	}
	return pool
}

func newTLSServerWithCA(t *testing.T, ca *tls.Certificate, handler http.Handler) *httptest.Server {
	t.Helper()

	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey() error = %v", err)
	}
	serialLimit := new(big.Int).Lsh(big.NewInt(1), 128)
	serial, err := rand.Int(rand.Reader, serialLimit)
	if err != nil {
		t.Fatalf("rand.Int() error = %v", err)
	}

	template := &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			CommonName: "api.example.com",
		},
		NotBefore:   time.Now().Add(-time.Hour),
		NotAfter:    time.Now().Add(24 * time.Hour),
		KeyUsage:    x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses: []net.IP{net.ParseIP("127.0.0.1")},
		DNSNames:    []string{"api.example.com", "localhost"},
	}

	der, err := x509.CreateCertificate(rand.Reader, template, ca.Leaf, &priv.PublicKey, ca.PrivateKey)
	if err != nil {
		t.Fatalf("CreateCertificate() error = %v", err)
	}
	certPEM := pemEncodeCertificate(der)
	keyPEM, err := pemEncodePrivateKey(priv)
	if err != nil {
		t.Fatalf("pemEncodePrivateKey() error = %v", err)
	}
	serverCert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatalf("X509KeyPair() error = %v", err)
	}

	server := httptest.NewUnstartedServer(handler)
	server.TLS = &tls.Config{
		Certificates: []tls.Certificate{serverCert},
		MinVersion:   tls.VersionTLS12,
	}
	server.StartTLS()
	return server
}

func startTLSEchoServer(t *testing.T, ca *tls.Certificate, serverName string) (string, func()) {
	t.Helper()

	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey() error = %v", err)
	}
	serialLimit := new(big.Int).Lsh(big.NewInt(1), 128)
	serial, err := rand.Int(rand.Reader, serialLimit)
	if err != nil {
		t.Fatalf("rand.Int() error = %v", err)
	}

	template := &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			CommonName: serverName,
		},
		NotBefore:   time.Now().Add(-time.Hour),
		NotAfter:    time.Now().Add(24 * time.Hour),
		KeyUsage:    x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:    []string{serverName},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, ca.Leaf, &priv.PublicKey, ca.PrivateKey)
	if err != nil {
		t.Fatalf("CreateCertificate() error = %v", err)
	}
	certPEM := pemEncodeCertificate(der)
	keyPEM, err := pemEncodePrivateKey(priv)
	if err != nil {
		t.Fatalf("pemEncodePrivateKey() error = %v", err)
	}
	serverCert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatalf("X509KeyPair() error = %v", err)
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	tlsLn := tls.NewListener(ln, &tls.Config{
		Certificates: []tls.Certificate{serverCert},
		MinVersion:   tls.VersionTLS12,
	})
	errCh := make(chan error, 1)
	go func() {
		defer close(errCh)
		conn, err := tlsLn.Accept()
		if err != nil {
			errCh <- err
			return
		}
		defer conn.Close()

		buf := make([]byte, 4)
		if _, err := io.ReadFull(conn, buf); err != nil {
			errCh <- err
			return
		}
		_, err = conn.Write(buf)
		errCh <- err
	}()

	return tlsLn.Addr().String(), func() {
		_ = tlsLn.Close()
		select {
		case err := <-errCh:
			if err != nil && !errors.Is(err, net.ErrClosed) {
				t.Fatalf("echo server error = %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for echo server")
		}
	}
}
