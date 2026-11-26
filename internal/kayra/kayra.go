// Package kayra is what ulgen asks Kayra, with the caller's token: the
// platform's APIs that serve MCP, and the caller's namespaces.
package kayra

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/infrapi/lib/pkg/auth"
	infraerrors "github.com/infrapi/lib/pkg/errors"
	"github.com/infrapi/lib/pkg/server"
	"github.com/infrapi/lib/pkg/std"
)

// API is an API of the platform that serves MCP.
type API struct {
	// Name is its group, compute.infrapi.io.
	Name string
	// Short is its short name, the first label of Name when it has none:
	// its tools are <Short>_<tool> on ulgen's aggregate endpoint.
	Short string
	// Version is the version it is served at, v1.
	Version string
}

// Root is the API's root: /apis/<Name>/<Version>.
func (a API) Root() string { return "/apis/" + a.Name + "/" + a.Version }

// Client asks Kayra.
type Client struct {
	auth    *auth.Client
	baseURL string
	http    *http.Client
}

// New is a client of the Kayra at baseURL; a, its auth client, reads its
// discovery document.
func New(a *auth.Client, baseURL string, timeout time.Duration) *Client {
	return &Client{auth: a, baseURL: strings.TrimRight(baseURL, "/"), http: std.UserAgentClient(&http.Client{Timeout: timeout})} // ulgen/<version>
}

// group is one API group of Kayra's GET /apis.
type group struct {
	API       string `json:"api"`
	ShortName string `json:"short_name"`
	Version   string `json:"version"`
	Builtin   bool   `json:"builtin"`
}

// APIs are the registered APIs serves keeps, among those whose spec, as
// Kayra last read it, has an MCP endpoint (<root>/-/mcp, lib's
// server.MCPPath). Any authenticated caller may read them.
func (c *Client) APIs(ctx context.Context, token string, serves func(name string) bool) ([]API, error) {
	doc, err := c.auth.Discover(ctx)
	if err != nil {
		return nil, err
	}
	catalog, ok := doc.APIs["catalog"]
	if !ok || doc.Endpoints.APIs == "" {
		return nil, infraerrors.Upstream(false, "kayra: its discovery document names no catalog or APIs endpoint")
	}
	var groups []group
	if err := c.get(ctx, token, doc.Endpoints.APIs, &groups); err != nil {
		return nil, err
	}
	var out []API
	for _, g := range groups {
		if g.Builtin || !serves(g.API) {
			continue
		}
		a := API{Name: g.API, Short: g.ShortName, Version: g.Version}
		if a.Short == "" {
			a.Short, _, _ = strings.Cut(a.Name, ".")
		}
		if a.Version == "" {
			a.Version = "v1"
		}
		var spec struct {
			Paths map[string]json.RawMessage `json:"paths"`
		}
		err := c.get(ctx, token, strings.TrimRight(catalog, "/")+"/apiregistrations/"+url.PathEscape(a.Name)+"/openapi.json", &spec)
		if infraerrors.ErrorCode(err) == http.StatusNotFound {
			continue // no spec read yet
		}
		if err != nil {
			return nil, err
		}
		if _, ok := spec.Paths[a.Root()+server.MCPPath]; ok {
			out = append(out, a)
		}
	}
	return out, nil
}

// Namespaces are the caller's namespaces: Kayra's
// GET /apis/system.<domain>/v1/namespaces answers a member's own.
func (c *Client) Namespaces(ctx context.Context, token string) ([]string, error) {
	doc, err := c.auth.Discover(ctx)
	if err != nil {
		return nil, err
	}
	system, ok := doc.APIs["system"]
	if !ok {
		return nil, infraerrors.Upstream(false, "kayra: its discovery document names no system api")
	}
	var ns []struct {
		Name string `json:"name"`
	}
	if err := c.get(ctx, token, strings.TrimRight(system, "/")+"/namespaces", &ns); err != nil {
		return nil, err
	}
	names := make([]string, 0, len(ns))
	for _, n := range ns {
		names = append(names, n.Name)
	}
	return names, nil
}

// get reads path's JSON document into out, as the caller. An error is an
// *errors.Error with Kayra's status, or a 502/503 when it does not answer.
func (c *Client) get(ctx context.Context, token, path string, out any) (err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	if id := server.CorrelationID(ctx); id != "" {
		req.Header.Set(server.RequestIDHeader, id)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return infraerrors.Unavailable("kayra: %s: %v", path, err)
	}
	defer func() {
		if cerr := resp.Body.Close(); cerr != nil && err == nil {
			err = fmt.Errorf("kayra: close %s: %w", path, cerr)
		}
	}()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return infraerrors.Upstream(true, "kayra: read %s: %v", path, err)
	}
	if resp.StatusCode != http.StatusOK {
		return infraerrors.New(resp.StatusCode, "kayra.get", "kayra answered %d to %s: %s", resp.StatusCode, path, strings.TrimSpace(string(body)))
	}
	if err := json.Unmarshal(body, out); err != nil {
		return infraerrors.Upstream(false, "kayra: decode %s: %v", path, err)
	}
	return nil
}
