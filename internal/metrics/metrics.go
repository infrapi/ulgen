// Package metrics is ulgen's Prometheus metrics, served at /metrics: what
// callers do (tool calls by upstream, tool and outcome, their durations),
// what ulgen asks Kayra and Vault, and the tools it exposes.
package metrics

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Metrics holds ulgen's collectors, on a registry of its own.
type Metrics struct {
	registry *prometheus.Registry

	// ToolCalls counts tool calls: upstream, tool (the upstream's name),
	// outcome (ok, refused, tool_error, error), as the audit log says them.
	ToolCalls *prometheus.CounterVec
	// ToolCallDuration is a call's duration, checks and credential included.
	ToolCallDuration *prometheus.HistogramVec
	// Kayra times ulgen's requests to Kayra: op (verify, check,
	// namespaces), result (ok, denied, refused, error).
	Kayra *prometheus.HistogramVec
	// CredentialReads counts tenant credential reads: upstream, result
	// (ok, error).
	CredentialReads *prometheus.CounterVec
	// Unauthenticated counts requests refused before any tool: reason
	// (no_token, refused_token).
	Unauthenticated *prometheus.CounterVec
	// ToolsExposed is the number of tools exposed per upstream, as last
	// listed.
	ToolsExposed *prometheus.GaugeVec
	// ListErrors counts the failed listings of an upstream's tools.
	ListErrors *prometheus.CounterVec
}

// New registers ulgen's collectors, the Go runtime's and the process's,
// and ulgen_build_info{version}.
func New(version string) *Metrics {
	reg := prometheus.NewRegistry()
	m := &Metrics{
		registry: reg,
		ToolCalls: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "ulgen_tool_calls_total", Help: "Tool calls, by upstream, tool and outcome.",
		}, []string{"upstream", "tool", "outcome"}),
		ToolCallDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name: "ulgen_tool_call_duration_seconds", Help: "Tool call duration, by upstream and outcome.",
			Buckets: []float64{.05, .1, .25, .5, 1, 2.5, 5, 10, 30, 60, 120, 300},
		}, []string{"upstream", "outcome"}),
		Kayra: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name: "ulgen_kayra_request_duration_seconds", Help: "Requests to Kayra, by op (verify, check, namespaces) and result.",
			Buckets: prometheus.DefBuckets,
		}, []string{"op", "result"}),
		CredentialReads: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "ulgen_credential_reads_total", Help: "Tenant credential reads (Vault, cached), by upstream and result.",
		}, []string{"upstream", "result"}),
		Unauthenticated: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "ulgen_unauthenticated_requests_total", Help: "Requests refused for want of a valid token, by reason.",
		}, []string{"reason"}),
		ToolsExposed: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "ulgen_tools_exposed", Help: "Tools exposed, by upstream, as last listed.",
		}, []string{"upstream"}),
		ListErrors: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "ulgen_tools_list_errors_total", Help: "Failed listings of an upstream's tools.",
		}, []string{"upstream"}),
	}
	build := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "ulgen_build_info", Help: "ulgen's version, value 1.",
	}, []string{"version"})
	build.WithLabelValues(version).Set(1)
	reg.MustRegister(m.ToolCalls, m.ToolCallDuration, m.Kayra, m.CredentialReads, m.Unauthenticated,
		m.ToolsExposed, m.ListErrors, build,
		collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
	return m
}

// Handler serves the metrics, Prometheus' text format.
func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{})
}
