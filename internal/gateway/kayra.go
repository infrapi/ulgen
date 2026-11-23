package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/infrapi/lib/pkg/auth"
)

// Kayra is what ulgen asks Kayra: who a token is, whether it may act,
// and which namespaces (namespaces) it is in.
type Kayra interface {
	Verify(ctx context.Context, token string) (*auth.UserInfo, error)
	Authorize(ctx context.Context, token string, req auth.AuthorizeRequest) (allowed bool, reason string, err error)
	Namespaces(ctx context.Context, token string) ([]string, error)
}

// KayraClient is Kayra over infrapi/lib's auth client, plus the namespace list.
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

type namespace struct {
	Name string `json:"name"`
}

// Namespaces lists the namespaces token's caller is in (Kayra's
// GET /apis/system.<domain>/v1/namespaces answers a member's own).
func (k *KayraClient) Namespaces(ctx context.Context, token string) (names []string, err error) {
	doc, err := k.Discover(ctx)
	if err != nil {
		return nil, err
	}
	base, ok := doc.APIs["system"]
	if !ok {
		return nil, fmt.Errorf("kayra: its discovery names no system api")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, k.baseURL+strings.TrimRight(base, "/")+"/namespaces", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	resp, err := k.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("kayra: namespaces: %w", err)
	}
	defer func() {
		if cerr := resp.Body.Close(); cerr != nil && err == nil {
			err = fmt.Errorf("kayra: close namespaces response: %w", cerr)
		}
	}()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("kayra: read namespaces: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("kayra: namespaces answered %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var ns []namespace
	if err := json.Unmarshal(body, &ns); err != nil {
		return nil, fmt.Errorf("kayra: decode namespaces: %w", err)
	}
	for _, n := range ns {
		names = append(names, n.Name)
	}
	return names, nil
}
