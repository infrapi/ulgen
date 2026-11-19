package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/infrapi/lib/pkg/auth"
)

// Kayra is what thanos asks Kayra: who a token is, whether it may act,
// and which tenants it belongs to.
type Kayra interface {
	Verify(ctx context.Context, token string) (*auth.UserInfo, error)
	Authorize(ctx context.Context, token string, req auth.AuthorizeRequest) (allowed bool, reason string, err error)
	Memberships(ctx context.Context, token, api string) ([]string, error)
}

// KayraClient is Kayra over infrapi/lib's auth client, plus /memberships.
type KayraClient struct {
	*auth.Client
	baseURL string
	http    *http.Client
}

// NewKayraClient returns a client of the Kayra at baseURL.
func NewKayraClient(c *auth.Client, baseURL string, httpClient *http.Client) *KayraClient {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return &KayraClient{Client: c, baseURL: strings.TrimRight(baseURL, "/"), http: httpClient}
}

type membership struct {
	Namespace string `json:"namespace"`
}

// Memberships lists the namespaces token's caller belongs to where api is
// usable (Kayra's GET .../memberships?api=).
func (k *KayraClient) Memberships(ctx context.Context, token, api string) (namespaces []string, err error) {
	doc, err := k.Discover(ctx)
	if err != nil {
		return nil, err
	}
	base, ok := doc.APIs["authorization"]
	if !ok {
		return nil, fmt.Errorf("kayra: its discovery names no authorization api")
	}
	u := k.baseURL + strings.TrimRight(base, "/") + "/memberships"
	if api != "" {
		u += "?" + url.Values{"api": {api}}.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	resp, err := k.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("kayra: memberships: %w", err)
	}
	defer func() {
		if cerr := resp.Body.Close(); cerr != nil && err == nil {
			err = fmt.Errorf("kayra: close memberships response: %w", cerr)
		}
	}()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("kayra: read memberships: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("kayra: memberships answered %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var ms []membership
	if err := json.Unmarshal(body, &ms); err != nil {
		return nil, fmt.Errorf("kayra: decode memberships: %w", err)
	}
	for _, m := range ms {
		namespaces = append(namespaces, m.Namespace)
	}
	return namespaces, nil
}
