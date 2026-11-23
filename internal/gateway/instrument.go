package gateway

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/infrapi/lib/pkg/auth"
	infraerrors "github.com/infrapi/lib/pkg/errors"

	"github.com/infrapi/ulgen/internal/metrics"
)

// requestIDHeader names a request across the components it crosses: set
// by the proxy in front (APISIX's request-id), logged by ulgen and passed
// on to the upstreams.
const requestIDHeader = "X-Request-Id"

type requestIDKey struct{}

// requestID is the request's id, set by Handler: "" without one.
func requestID(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey{}).(string)
	return id
}

// okOrError is a result label: ok, or error.
func okOrError(err error) string {
	if err != nil {
		return "error"
	}
	return "ok"
}

// instrumentedKayra times what ulgen asks Kayra, by op and result.
type instrumentedKayra struct {
	Kayra
	m *metrics.Metrics
}

// kayraResult names an answer: ok, denied (a check's no), refused (4xx:
// a token or a request Kayra turns down), error (Kayra unreachable or 5xx).
func kayraResult(err error, allowed bool) string {
	var ie *infraerrors.Error
	switch {
	case err == nil && allowed:
		return "ok"
	case err == nil:
		return "denied"
	case errors.As(err, &ie) && ie.Code >= http.StatusBadRequest && ie.Code < http.StatusInternalServerError:
		return "refused"
	default:
		return "error"
	}
}

func (k instrumentedKayra) observe(op string, start time.Time, err error, allowed bool) {
	k.m.Kayra.WithLabelValues(op, kayraResult(err, allowed)).Observe(time.Since(start).Seconds())
}

func (k instrumentedKayra) Verify(ctx context.Context, token string) (*auth.UserInfo, error) {
	start := time.Now()
	u, err := k.Kayra.Verify(ctx, token)
	k.observe("verify", start, err, true)
	return u, err
}

func (k instrumentedKayra) Authorize(ctx context.Context, token string, req auth.AuthorizeRequest) (bool, string, error) {
	start := time.Now()
	allowed, reason, err := k.Kayra.Authorize(ctx, token, req)
	k.observe("check", start, err, allowed)
	return allowed, reason, err
}

func (k instrumentedKayra) Namespaces(ctx context.Context, token string) ([]string, error) {
	start := time.Now()
	ns, err := k.Kayra.Namespaces(ctx, token)
	k.observe("namespaces", start, err, true)
	return ns, err
}
