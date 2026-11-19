package credentials

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/infrapi/thanos/internal/config"
)

type fakeReader struct {
	reads int
	data  map[string]map[string]any
}

func (f *fakeReader) GetSecret(_ context.Context, mount, path string) (map[string]any, error) {
	f.reads++
	d, ok := f.data[mount+"/"+path]
	if !ok {
		return nil, errors.New("not found")
	}
	return d, nil
}

func TestVaultCredential(t *testing.T) {
	r := &fakeReader{data: map[string]map[string]any{"secret/grafana/acme": {"token": "sa-acme"}, "secret/grafana/empty": {"other": "x"}}}
	v := NewVault(r, time.Minute)
	now := time.Unix(1000, 0)
	v.now = func() time.Time { return now }
	secret := config.VaultSecret{Mount: "secret", Path: "grafana/{tenant}", Key: "token"}

	for i := 0; i < 3; i++ {
		got, err := v.Credential(context.Background(), secret, "acme")
		if err != nil || got != "sa-acme" {
			t.Fatalf("Credential = %q, %v", got, err)
		}
	}
	if r.reads != 1 {
		t.Errorf("reads = %d, want 1 (cached)", r.reads)
	}
	now = now.Add(2 * time.Minute)
	if _, err := v.Credential(context.Background(), secret, "acme"); err != nil || r.reads != 2 {
		t.Errorf("expired cache: reads = %d, err %v", r.reads, err)
	}

	for _, tenant := range []string{"globex", "empty", "../acme", "acme/x", "", "ACME", "-acme"} {
		if _, err := v.Credential(context.Background(), secret, tenant); err == nil {
			t.Errorf("tenant %q: no error", tenant)
		}
	}
}
