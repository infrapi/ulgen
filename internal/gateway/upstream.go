package gateway

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// callTimeout bounds one upstream HTTP request.
const callTimeout = 5 * time.Minute

type headersKey struct{}

// withHeaders makes the upstream requests made with ctx carry h: each
// call brings its own credential, over a session shared by every caller.
func withHeaders(ctx context.Context, h http.Header) context.Context {
	return context.WithValue(ctx, headersKey{}, h)
}

func headersOf(ctx context.Context) http.Header {
	h, _ := ctx.Value(headersKey{}).(http.Header)
	return h
}

// detach returns a context cancelled with parent but carrying none of its
// values: parent is thanos' own server request, whose values (the
// protocol version negotiated with thanos' client, for one) must not leak
// into the upstream client's requests.
func detach(parent context.Context) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(context.Background())
	stop := context.AfterFunc(parent, cancel)
	return ctx, func() {
		stop()
		cancel()
	}
}

// headerTransport adds the upstream's static headers, then the call's own.
type headerTransport struct {
	static http.Header
	next   http.RoundTripper
}

func (t headerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	for k, vs := range t.static {
		req.Header[k] = vs
	}
	if h, ok := req.Context().Value(headersKey{}).(http.Header); ok {
		for k, vs := range h {
			req.Header[k] = vs
		}
	}
	return t.next.RoundTrip(req)
}

// upstreamClient keeps one MCP session to an upstream, reconnecting when
// it breaks.
type upstreamClient struct {
	url      string
	protocol string
	client   *mcp.Client
	http     *http.Client
	log      *slog.Logger

	mu      sync.Mutex
	session *mcp.ClientSession
}

func newUpstreamClient(url, protocol string, static http.Header, base http.RoundTripper, log *slog.Logger) *upstreamClient {
	if base == nil {
		base = http.DefaultTransport
	}
	return &upstreamClient{
		url:      url,
		protocol: protocol,
		client:   mcp.NewClient(&mcp.Implementation{Name: "thanos", Version: "v1"}, nil),
		// Bounds a whole call, streamed response included: long enough for
		// a heavy Loki or Prometheus query, short enough not to hang.
		http: &http.Client{Transport: headerTransport{static: static, next: base}, Timeout: callTimeout},
		log:  log,
	}
}

func (u *upstreamClient) connect(ctx context.Context) (*mcp.ClientSession, error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.session != nil {
		return u.session, nil
	}
	// The session outlives the call that opened it: only this call's
	// headers (for the initialize request) carry over, not its
	// cancellation.
	s, err := u.client.Connect(withHeaders(context.Background(), headersOf(ctx)), &mcp.StreamableClientTransport{
		Endpoint:             u.url,
		HTTPClient:           u.http,
		MaxRetries:           -1,
		DisableStandaloneSSE: true,
	}, &mcp.ClientSessionOptions{ProtocolVersion: u.protocol})
	if err != nil {
		return nil, fmt.Errorf("connect %s: %w", u.url, err)
	}
	u.session = s
	return s, nil
}

// drop forgets s, if it is still the current session, so the next call
// reconnects.
func (u *upstreamClient) drop(s *mcp.ClientSession) {
	u.mu.Lock()
	if u.session != s {
		u.mu.Unlock()
		return
	}
	u.session = nil
	u.mu.Unlock()
	if err := s.Close(); err != nil {
		u.log.Debug("closing a broken upstream session", "upstream", u.url, "error", err)
	}
}

// do runs fn over the session with headers, retrying once on a fresh
// session when the call fails at the transport/protocol level (an
// upstream restart, an expired session). A tool's own failure is a
// result, not an error, and is never retried.
func (u *upstreamClient) do(parent context.Context, headers http.Header, fn func(context.Context, *mcp.ClientSession) error) error {
	ctx, cancel := detach(parent)
	defer cancel()
	ctx = withHeaders(ctx, headers)
	var err error
	for attempt := 0; attempt < 2; attempt++ {
		var s *mcp.ClientSession
		s, err = u.connect(ctx)
		if err != nil {
			continue
		}
		if err = fn(ctx, s); err == nil {
			return nil
		}
		if ctx.Err() != nil {
			return err
		}
		u.drop(s)
	}
	return err
}

func (u *upstreamClient) listTools(ctx context.Context, headers http.Header) ([]*mcp.Tool, error) {
	var tools []*mcp.Tool
	err := u.do(ctx, headers, func(ctx context.Context, s *mcp.ClientSession) error {
		tools = nil
		for t, err := range s.Tools(ctx, nil) {
			if err != nil {
				return err
			}
			tools = append(tools, t)
		}
		return nil
	})
	return tools, err
}

func (u *upstreamClient) callTool(ctx context.Context, headers http.Header, name string, args map[string]any) (*mcp.CallToolResult, error) {
	var res *mcp.CallToolResult
	err := u.do(ctx, headers, func(ctx context.Context, s *mcp.ClientSession) error {
		var err error
		res, err = s.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
		return err
	})
	return res, err
}

func (u *upstreamClient) close() {
	u.mu.Lock()
	s := u.session
	u.session = nil
	u.mu.Unlock()
	if s != nil {
		if err := s.Close(); err != nil {
			u.log.Debug("closing upstream session", "upstream", u.url, "error", err)
		}
	}
}
