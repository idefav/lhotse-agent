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
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"k8s.io/klog/v2"
)

type CertStore struct {
	mu        sync.RWMutex
	primary   *tls.Certificate
	secondary *tls.Certificate
	leafCache sync.Map
	vaultURI  string
}

func NewCertStore(vaultURI string) *CertStore {
	return &CertStore{
		vaultURI: strings.TrimRight(vaultURI, "/"),
	}
}

func (cs *CertStore) GetCertificate(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
	domain := strings.TrimSpace(hello.ServerName)
	if domain == "" {
		return nil, fmt.Errorf("missing tls server name")
	}
	if cached, ok := cs.leafCache.Load(domain); ok {
		leaf := cached.(*tls.Certificate)
		if leaf.Leaf.NotAfter.After(time.Now().Add(1 * time.Hour)) {
			return leaf, nil
		}
	}

	cert, err := cs.generateLeafCert(domain)
	if err != nil {
		return nil, err
	}

	cs.leafCache.Store(domain, cert)
	return cert, nil
}

func (cs *CertStore) generateLeafCert(domain string) (*tls.Certificate, error) {
	cs.mu.RLock()
	caCert := cs.primary
	cs.mu.RUnlock()

	if caCert == nil {
		return nil, fmt.Errorf("no CA certificate loaded")
	}

	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}

	serialNumberLimit := new(big.Int).Lsh(big.NewInt(1), 128)
	serialNumber, err := rand.Int(rand.Reader, serialNumberLimit)
	if err != nil {
		return nil, err
	}

	template := x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			Organization: []string{"Lhotse MITM Agent"},
			CommonName:   domain,
		},
		NotBefore:             time.Now().Add(-1 * time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}
	if ip := net.ParseIP(domain); ip != nil {
		template.IPAddresses = []net.IP{ip}
	} else {
		template.DNSNames = []string{domain}
	}

	derBytes, err := x509.CreateCertificate(rand.Reader, &template, caCert.Leaf, &priv.PublicKey, caCert.PrivateKey)
	if err != nil {
		return nil, err
	}

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: derBytes})

	privBytes, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		return nil, err
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privBytes})

	tlsCert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return nil, err
	}

	tlsCert.Leaf, err = x509.ParseCertificate(derBytes)
	if err != nil {
		return nil, err
	}

	return &tlsCert, nil
}

func (cs *CertStore) FetchAndReload() error {
	if cs.vaultURI == "" {
		return fmt.Errorf("vault URI not configured")
	}

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(fmt.Sprintf("%s/internal/ca/keypair", cs.vaultURI))
	if err != nil {
		return fmt.Errorf("failed to fetch CA from vault: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("vault CA returned %d: %s", resp.StatusCode, string(body))
	}

	var payload struct {
		CertPEM []string `json:"cert_pem"`
		KeyPEM  []string `json:"key_pem"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return fmt.Errorf("failed to decode CA response: %v", err)
	}

	if len(payload.CertPEM) == 0 || len(payload.KeyPEM) == 0 {
		return fmt.Errorf("no CA certificates returned")
	}
	if len(payload.CertPEM) != len(payload.KeyPEM) {
		return fmt.Errorf("mismatched CA payload: %d certs, %d keys", len(payload.CertPEM), len(payload.KeyPEM))
	}

	var certs []*tls.Certificate
	for i := range payload.CertPEM {
		certPEM := strings.TrimSpace(payload.CertPEM[i])
		keyPEM := strings.TrimSpace(payload.KeyPEM[i])
		if certPEM == "" || keyPEM == "" {
			klog.Errorf("Failed to parse CA %d: empty cert or key", i)
			continue
		}

		cert, err := tls.X509KeyPair([]byte(certPEM), []byte(keyPEM))
		if err != nil {
			klog.Errorf("Failed to parse CA %d: %v", i, err)
			continue
		}
		if cert.Leaf, err = x509.ParseCertificate(cert.Certificate[0]); err != nil {
			klog.Errorf("Failed to parse leaf for CA %d: %v", i, err)
			continue
		}
		certs = append(certs, &cert)
	}

	if len(certs) == 0 {
		return fmt.Errorf("no valid CA certificates loaded")
	}

	cs.Reload(certs)
	return nil
}

func (cs *CertStore) Reload(certs []*tls.Certificate) {
	if len(certs) == 0 {
		return
	}
	cs.mu.Lock()
	defer cs.mu.Unlock()
	cs.primary = certs[0]
	if len(certs) > 1 {
		cs.secondary = certs[1]
	} else {
		cs.secondary = nil
	}
	// Clear leaf cache on CA rotation
	cs.leafCache.Range(func(key, value interface{}) bool {
		cs.leafCache.Delete(key)
		return true
	})
}

func (cs *CertStore) GetActiveCAPEM() []byte {
	cs.mu.RLock()
	defer cs.mu.RUnlock()

	var bundle []byte
	if cs.primary != nil {
		block := &pem.Block{Type: "CERTIFICATE", Bytes: cs.primary.Certificate[0]}
		bundle = append(bundle, pem.EncodeToMemory(block)...)
	}
	if cs.secondary != nil {
		block := &pem.Block{Type: "CERTIFICATE", Bytes: cs.secondary.Certificate[0]}
		bundle = append(bundle, pem.EncodeToMemory(block)...)
	}
	return bundle
}
