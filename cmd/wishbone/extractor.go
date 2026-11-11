package main

import (
	"log/slog"
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"go.opentelemetry.io/otel/trace"

	"jsclayton.dev/wishbone/internal/config"
	"jsclayton.dev/wishbone/internal/extract"
	"jsclayton.dev/wishbone/internal/extract/llm"
	"jsclayton.dev/wishbone/internal/extract/pipeline"
	"jsclayton.dev/wishbone/internal/store"
	"jsclayton.dev/wishbone/internal/telemetry"
)

// newExtractor builds the extraction pipeline with the stages the config
// enables. Stages that are not configured stay nil and the pipeline skips them.
// allowPrivate lets the direct fetch connect to private addresses: only the e2e
// build ever sets it (see allowprivate_e2e.go).
func newExtractor(cfg config.Config, st *store.Store, logger *slog.Logger, reg prometheus.Registerer, allowPrivate bool, tracer trace.TracerProvider) (*pipeline.Service, error) {
	opts := pipeline.Options{
		DB:         st.DB(),
		Direct:     extract.NewDirect(extract.Options{AllowPrivateAddrs: allowPrivate, Tracer: tracer}),
		Logger:     logger,
		Registerer: reg,
	}
	if cfg.BrightDataEnabled() {
		bd := extract.NewBrightData(extract.BrightDataOptions{APIKey: cfg.BrightDataAPIKey, Zone: cfg.BrightDataZone, Tracer: tracer})
		opts.BrightData, opts.BrightDataRequests = bd, bd.Requests
	}
	provider, err := llm.New(llm.Config{Provider: cfg.LLMProvider, APIKey: cfg.LLMAPIKey, BaseURL: cfg.LLMBaseURL, Model: cfg.LLMModel},
		&http.Client{Timeout: llm.DefaultTimeout, Transport: telemetry.Transport(tracer, nil)})
	if err != nil {
		return nil, err
	}
	if provider != nil {
		x := &llm.Extractor{Provider: provider}
		opts.LLM, opts.Photo = x, x
	}
	return pipeline.New(opts), nil
}
