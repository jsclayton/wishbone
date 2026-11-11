package pipeline

import (
	"github.com/prometheus/client_golang/prometheus"

	"jsclayton.dev/wishbone/internal/extract/llm"
)

type metrics struct {
	extractions *prometheus.CounterVec
	llmTokens   *prometheus.CounterVec
}

func newMetrics(reg prometheus.Registerer, bdRequests func() int64) *metrics {
	m := &metrics{
		extractions: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "wishbone_extractions_total",
			Help: "Extraction stage runs by stage (direct, brightdata, llm) and outcome (success, failure).",
		}, []string{"stage", "outcome"}),
		llmTokens: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "wishbone_llm_tokens_total",
			Help: "LLM tokens used by extraction, by direction (input, output).",
		}, []string{"direction"}),
	}
	// Show every series from the start, so rate() works on the first event.
	for _, st := range []string{StageDirect, StageBrightData, StageLLM} {
		m.extractions.WithLabelValues(st, "success")
		m.extractions.WithLabelValues(st, "failure")
	}
	m.llmTokens.WithLabelValues("input")
	m.llmTokens.WithLabelValues("output")
	if reg != nil {
		reg.MustRegister(m.extractions, m.llmTokens)
		if bdRequests != nil {
			reg.MustRegister(prometheus.NewCounterFunc(prometheus.CounterOpts{
				Name: "wishbone_brightdata_requests_total", Help: "Requests sent to Bright Data Web Unlocker.",
			}, func() float64 { return float64(bdRequests()) }))
		}
	}
	return m
}

func (m *metrics) record(stage string, ok bool) {
	outcome := "failure"
	if ok {
		outcome = "success"
	}
	m.extractions.WithLabelValues(stage, outcome).Inc()
}

func (m *metrics) tokens(u llm.Usage) {
	m.llmTokens.WithLabelValues("input").Add(float64(u.InputTokens))
	m.llmTokens.WithLabelValues("output").Add(float64(u.OutputTokens))
}
