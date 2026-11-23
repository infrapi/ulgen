// Package tlsfile serves a certificate from PEM files, re-read when they
// change: a certificate renewed on disk (autosec, cert-manager) is picked up
// without a restart.
package tlsfile

import (
	"crypto/tls"
	"fmt"
	"os"
	"sync"
	"time"
)

// Loader holds the certificate of CertFile and KeyFile.
type Loader struct {
	CertFile, KeyFile string

	mu      sync.Mutex
	cert    *tls.Certificate
	certMod time.Time
	keyMod  time.Time
}

// New loads the certificate once: an unreadable or invalid pair is an error
// at startup, not at the first handshake.
func New(certFile, keyFile string) (*Loader, error) {
	l := &Loader{CertFile: certFile, KeyFile: keyFile}
	if _, err := l.load(); err != nil {
		return nil, err
	}
	return l, nil
}

// GetCertificate is tls.Config.GetCertificate: the current certificate,
// re-read when either file's modification time changed. A pair that fails
// to load (a renewal half written) keeps the previous certificate served.
func (l *Loader) GetCertificate(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	cert, err := l.load()
	if err != nil {
		l.mu.Lock()
		defer l.mu.Unlock()
		if l.cert != nil {
			return l.cert, nil
		}
		return nil, err
	}
	return cert, nil
}

func (l *Loader) load() (*tls.Certificate, error) {
	certInfo, err := os.Stat(l.CertFile)
	if err != nil {
		return nil, fmt.Errorf("tlsfile: %w", err)
	}
	keyInfo, err := os.Stat(l.KeyFile)
	if err != nil {
		return nil, fmt.Errorf("tlsfile: %w", err)
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	if l.cert != nil && certInfo.ModTime().Equal(l.certMod) && keyInfo.ModTime().Equal(l.keyMod) {
		return l.cert, nil
	}
	cert, err := tls.LoadX509KeyPair(l.CertFile, l.KeyFile)
	if err != nil {
		return nil, fmt.Errorf("tlsfile: %s, %s: %w", l.CertFile, l.KeyFile, err)
	}
	l.cert, l.certMod, l.keyMod = &cert, certInfo.ModTime(), keyInfo.ModTime()
	return l.cert, nil
}
