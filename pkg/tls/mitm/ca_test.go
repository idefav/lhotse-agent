package mitm

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestFetchAndReloadRejectsMismatchedCounts(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"cert_pem": []string{"cert-1", "cert-2"},
			"key_pem":  []string{"key-1"},
		})
	}))
	defer server.Close()

	store := NewCertStore(server.URL)
	if err := store.FetchAndReload(); err == nil {
		t.Fatal("FetchAndReload() error = nil, want non-nil")
	}
	if got := store.GetActiveCAPEM(); len(got) != 0 {
		t.Fatalf("GetActiveCAPEM() = %q, want empty", string(got))
	}
}

func TestFetchAndReloadKeepsExistingCAOnInvalidPayload(t *testing.T) {
	existing, existingPEM := newTestCA(t, "existing-root")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"cert_pem": []string{"bad-cert"},
			"key_pem":  []string{"bad-key"},
		})
	}))
	defer server.Close()

	store := NewCertStore(server.URL)
	store.Reload([]*tls.Certificate{existing})

	if err := store.FetchAndReload(); err == nil {
		t.Fatal("FetchAndReload() error = nil, want non-nil")
	}
	if got := string(store.GetActiveCAPEM()); got != string(existingPEM) {
		t.Fatalf("active CA changed after invalid payload: got %q want %q", got, string(existingPEM))
	}
}

func TestGetCertificateCachesAndReloadInvalidatesLeafs(t *testing.T) {
	firstCA, _ := newTestCA(t, "first-root")
	secondCA, _ := newTestCA(t, "second-root")

	store := NewCertStore("")
	store.Reload([]*tls.Certificate{firstCA})

	firstLeaf, err := store.GetCertificate(&tls.ClientHelloInfo{ServerName: "api.example.com"})
	if err != nil {
		t.Fatalf("GetCertificate() error = %v", err)
	}
	secondLeaf, err := store.GetCertificate(&tls.ClientHelloInfo{ServerName: "api.example.com"})
	if err != nil {
		t.Fatalf("GetCertificate() second error = %v", err)
	}
	if firstLeaf != secondLeaf {
		t.Fatal("GetCertificate() did not reuse cached leaf")
	}
	if got := firstLeaf.Leaf.Issuer.CommonName; got != "first-root" {
		t.Fatalf("first leaf issuer = %q, want %q", got, "first-root")
	}
	if len(firstLeaf.Leaf.DNSNames) != 1 || firstLeaf.Leaf.DNSNames[0] != "api.example.com" {
		t.Fatalf("leaf DNSNames = %v, want [api.example.com]", firstLeaf.Leaf.DNSNames)
	}

	store.Reload([]*tls.Certificate{secondCA})

	rotatedLeaf, err := store.GetCertificate(&tls.ClientHelloInfo{ServerName: "api.example.com"})
	if err != nil {
		t.Fatalf("GetCertificate() after reload error = %v", err)
	}
	if rotatedLeaf == firstLeaf {
		t.Fatal("GetCertificate() reused cached leaf after reload")
	}
	if got := rotatedLeaf.Leaf.Issuer.CommonName; got != "second-root" {
		t.Fatalf("rotated leaf issuer = %q, want %q", got, "second-root")
	}
}

func TestGetCertificateUsesIPSANForIPTargets(t *testing.T) {
	ca, _ := newTestCA(t, "ip-root")

	store := NewCertStore("")
	store.Reload([]*tls.Certificate{ca})

	leaf, err := store.GetCertificate(&tls.ClientHelloInfo{ServerName: "203.0.113.10"})
	if err != nil {
		t.Fatalf("GetCertificate() error = %v", err)
	}
	if len(leaf.Leaf.IPAddresses) != 1 || leaf.Leaf.IPAddresses[0].String() != "203.0.113.10" {
		t.Fatalf("leaf IPAddresses = %v, want [203.0.113.10]", leaf.Leaf.IPAddresses)
	}
	if len(leaf.Leaf.DNSNames) != 0 {
		t.Fatalf("leaf DNSNames = %v, want empty", leaf.Leaf.DNSNames)
	}
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
			Organization: []string{"Lhotse Test Root"},
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
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})

	keyBytes, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		t.Fatalf("MarshalPKCS8PrivateKey() error = %v", err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyBytes})

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
