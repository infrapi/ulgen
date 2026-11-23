package tlsfile

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// writePair writes a self-signed certificate for cn and its key.
func writePair(t *testing.T, certFile, keyFile, cn string, mod time.Time) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: cn},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour)}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyFile, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{certFile, keyFile} {
		if err := os.Chtimes(f, mod, mod); err != nil {
			t.Fatal(err)
		}
	}
}

func commonName(t *testing.T, l *Loader) string {
	t.Helper()
	cert, err := l.GetCertificate(nil)
	if err != nil {
		t.Fatalf("GetCertificate: %v", err)
	}
	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	return leaf.Subject.CommonName
}

func TestReloadsARenewedCertificate(t *testing.T) {
	dir := t.TempDir()
	certFile, keyFile := filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")
	start := time.Now().Add(-time.Minute)
	writePair(t, certFile, keyFile, "first", start)

	l, err := New(certFile, keyFile)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if cn := commonName(t, l); cn != "first" {
		t.Errorf("cn = %q, want first", cn)
	}

	writePair(t, certFile, keyFile, "renewed", start.Add(30*time.Second))
	if cn := commonName(t, l); cn != "renewed" {
		t.Errorf("after renewal, cn = %q, want renewed", cn)
	}

	// a half-written renewal: the previous certificate is still served
	if err := os.WriteFile(keyFile, []byte("not a key"), 0o600); err != nil {
		t.Fatal(err)
	}
	if cn := commonName(t, l); cn != "renewed" {
		t.Errorf("with a broken key, cn = %q, want the previous one", cn)
	}
}

func TestNewRefusesABadPair(t *testing.T) {
	dir := t.TempDir()
	if _, err := New(filepath.Join(dir, "missing.pem"), filepath.Join(dir, "missing.key")); err == nil {
		t.Error("missing files: no error")
	}
	certFile, keyFile := filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")
	writePair(t, certFile, keyFile, "x", time.Now())
	if err := os.WriteFile(keyFile, []byte("garbage"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := New(certFile, keyFile); err == nil {
		t.Error("bad key: no error")
	}
}
