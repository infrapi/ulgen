// Package metrics is ulgen's Prometheus metrics: what callers do (tool
// calls by API, tool and outcome, their durations), what ulgen asks Kayra,
// and the tools it serves. Registered on the registerer New is given, the
// default one in ulgen: infrapi/lib serves it at <root>/-/metrics, with the
// HTTP requests' and the Go runtime's.
package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
)

// Metrics holds ulgen's collectors.
type Metrics struct {
	// ToolCalls counts tool calls: api, tool (the API's own name), outcome
	// (ok, tool_error, error), as the audit log says them.
	ToolCalls *prometheus.CounterVec
	// ToolCallDuration is a call's duration.
	ToolCallDuration *prometheus.HistogramVec
	// Kayra times ulgen's requests to Kayra: op (apis, namespaces), result
	// (ok, refused, error).
	Kayra *prometheus.HistogramVec
	// ToolsServed is the number of tools served per API, as last listed.
	ToolsServed *prometheus.GaugeVec
	// ListErrors counts the failed listings of an API's tools.
	ListErrors *prometheus.CounterVec
}

// New registers ulgen's collectors on reg, and ulgen_build_info{version}.
func New(reg prometheus.Registerer, version string) *Metrics {
	m := &Metrics{
		ToolCalls: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "ulgen_tool_calls_total", Help: "Tool calls, by API, tool and outcome.",
		}, []string{"api", "tool", "outcome"}),
		ToolCallDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name: "ulgen_tool_call_duration_seconds", Help: "Tool call duration, by API and outcome.",
			Buckets: []float64{.05, .1, .25, .5, 1, 2.5, 5, 10, 30, 60, 120, 300},
		}, []string{"api", "outcome"}),
		Kayra: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name: "ulgen_kayra_request_duration_seconds", Help: "Requests to Kayra, by op (apis, namespaces) and result.",
			Buckets: prometheus.DefBuckets,
		}, []string{"op", "result"}),
		ToolsServed: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "ulgen_tools_served", Help: "Tools served, by API, as last listed.",
		}, []string{"api"}),
		ListErrors: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "ulgen_tools_list_errors_total", Help: "Failed listings of an API's tools.",
		}, []string{"api"}),
	}
	build := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "ulgen_build_info", Help: "ulgen's version, value 1.",
	}, []string{"version"})
	build.WithLabelValues(version).Set(1)
	reg.MustRegister(m.ToolCalls, m.ToolCallDuration, m.Kayra, m.ToolsServed, m.ListErrors, build)
	return m
}
