// Package credentials reads a tenant's upstream credential from Vault
// (KV v2), with a short in-memory cache so a burst of tool calls doesn't
// read the same secret every time.
package credentials

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/infrapi/lib/pkg/vault"

	"github.com/infrapi/ulgen/internal/config"
)

// Source returns the credential of tenant for one upstream.
type Source interface {
	Credential(ctx context.Context, secret config.VaultSecret, tenant string) (string, error)
}

// tenantName is what a tenant may look like before it goes into a Vault
// path: a Kayra namespace name. Anything else (a slash, "..") is refused,
// so a caller can't make ulgen read another path.
var tenantName = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

// ValidTenant reports whether tenant may be used in a credential path.
func ValidTenant(tenant string) bool {
	return tenantName.MatchString(tenant)
}

// SecretReader is the part of a Vault client used (vault.Client).
type SecretReader interface {
	GetSecret(ctx context.Context, mountPath, path string) (map[string]any, error)
}

type entry struct {
	value   string
	expires time.Time
}

// Vault is a Source reading Vault, caching each credential for ttl.
type Vault struct {
	reader SecretReader
	ttl    time.Duration
	now    func() time.Time

	mu    sync.Mutex
	cache map[string]entry
}

// NewVault returns a Source over reader.
func NewVault(reader SecretReader, ttl time.Duration) *Vault {
	return &Vault{reader: reader, ttl: ttl, now: time.Now, cache: map[string]entry{}}
}

// Login builds a Vault client logged in with AppRole per cfg.
func Login(ctx context.Context, cfg *config.VaultConfig) (*vault.Client, error) {
	client, err := vault.NewClient(vault.ClientOptions{Address: cfg.Address, CACertPath: cfg.CACertPath})
	if err != nil {
		return nil, err
	}
	err = client.Login(ctx, vault.LoginOptions{AppRole: &vault.AppRoleLogin{
		RoleID:       os.Getenv(cfg.RoleIDEnv),
		SecretIDFile: cfg.SecretIDFile,
		MountPath:    cfg.AppRoleMount,
	}})
	if err != nil {
		return nil, fmt.Errorf("credentials: vault login: %w", err)
	}
	return client, nil
}

// Credential reads secret's key at its path for tenant.
func (v *Vault) Credential(ctx context.Context, secret config.VaultSecret, tenant string) (string, error) {
	if !ValidTenant(tenant) {
		return "", fmt.Errorf("credentials: invalid tenant %q", tenant)
	}
	path := strings.ReplaceAll(secret.Path, "{tenant}", tenant)
	cacheKey := secret.Mount + "\x00" + path + "\x00" + secret.Key

	v.mu.Lock()
	if e, ok := v.cache[cacheKey]; ok && v.now().Before(e.expires) {
		v.mu.Unlock()
		return e.value, nil
	}
	v.mu.Unlock()

	data, err := v.reader.GetSecret(ctx, secret.Mount, path)
	if err != nil {
		return "", fmt.Errorf("credentials: read %s/%s: %w", secret.Mount, path, err)
	}
	value, ok := data[secret.Key].(string)
	if !ok || value == "" {
		return "", fmt.Errorf("credentials: %s/%s has no %q", secret.Mount, path, secret.Key)
	}

	v.mu.Lock()
	v.cache[cacheKey] = entry{value: value, expires: v.now().Add(v.ttl)}
	v.mu.Unlock()
	return value, nil
}
