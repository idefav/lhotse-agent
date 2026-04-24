package proxy

import (
	"bytes"
	"crypto/tls"
	"encoding/pem"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"lhotse-agent/pkg/tls/mitm"
)

func TestCombinedTrustBundleIncludesSystemAndActiveCA(t *testing.T) {
	systemPEM := testPEM("system-root")
	lhotsePEM := testPEM("lhotse-root")
	restoreSystemCABundlePaths(t, writeTempBundle(t, systemPEM))

	store := mitm.NewCertStore("")
	store.Reload([]*tls.Certificate{{Certificate: [][]byte{[]byte("lhotse-root")}}})

	bundle, err := combinedTrustBundle(store)
	if err != nil {
		t.Fatalf("combinedTrustBundle() error = %v", err)
	}
	if !bytes.Contains(bundle, systemPEM) {
		t.Fatalf("bundle does not contain system PEM")
	}
	if !bytes.Contains(bundle, lhotsePEM) {
		t.Fatalf("bundle does not contain Lhotse PEM")
	}
	if bytes.Index(bundle, systemPEM) > bytes.Index(bundle, lhotsePEM) {
		t.Fatalf("system PEM should appear before Lhotse PEM")
	}
}

func TestCombinedTrustBundleRequiresSystemBundle(t *testing.T) {
	restoreSystemCABundlePaths(t, filepath.Join(t.TempDir(), "missing.crt"))

	store := mitm.NewCertStore("")
	store.Reload([]*tls.Certificate{{Certificate: [][]byte{[]byte("lhotse-root")}}})

	_, err := combinedTrustBundle(store)
	if !errors.Is(err, errNoSystemCABundle) {
		t.Fatalf("combinedTrustBundle() error = %v, want %v", err, errNoSystemCABundle)
	}
}

func TestCABundleHandlerStatuses(t *testing.T) {
	systemPath := writeTempBundle(t, testPEM("system-root"))
	restoreSystemCABundlePaths(t, systemPath)

	store := mitm.NewCertStore("")
	store.Reload([]*tls.Certificate{{Certificate: [][]byte{[]byte("lhotse-root")}}})
	mux := http.NewServeMux()
	registerCABundleHandler(mux, store)

	resp := httptest.NewRecorder()
	mux.ServeHTTP(resp, httptest.NewRequest(http.MethodGet, "/ca.crt", nil))
	if resp.Code != http.StatusOK {
		t.Fatalf("/ca.crt status = %d, want %d", resp.Code, http.StatusOK)
	}
	if got := resp.Header().Get("Content-Type"); got != "application/x-pem-file" {
		t.Fatalf("Content-Type = %q, want application/x-pem-file", got)
	}

	emptyStoreMux := http.NewServeMux()
	registerCABundleHandler(emptyStoreMux, mitm.NewCertStore(""))
	resp = httptest.NewRecorder()
	emptyStoreMux.ServeHTTP(resp, httptest.NewRequest(http.MethodGet, "/ca.crt", nil))
	if resp.Code != http.StatusServiceUnavailable {
		t.Fatalf("/ca.crt empty CA status = %d, want %d", resp.Code, http.StatusServiceUnavailable)
	}

	restoreSystemCABundlePaths(t, filepath.Join(t.TempDir(), "missing.crt"))
	resp = httptest.NewRecorder()
	mux.ServeHTTP(resp, httptest.NewRequest(http.MethodGet, "/ca.crt", nil))
	if resp.Code != http.StatusInternalServerError {
		t.Fatalf("/ca.crt missing system bundle status = %d, want %d", resp.Code, http.StatusInternalServerError)
	}
}

func testPEM(label string) []byte {
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: []byte(label)})
}

func writeTempBundle(t *testing.T, bundle []byte) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "ca-certificates.crt")
	if err := os.WriteFile(path, bundle, 0600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	return path
}

func restoreSystemCABundlePaths(t *testing.T, paths ...string) {
	t.Helper()

	original := systemCABundlePaths
	systemCABundlePaths = paths
	t.Cleanup(func() {
		systemCABundlePaths = original
	})
}
