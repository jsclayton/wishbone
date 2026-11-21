package main

import (
	"bytes"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/require"
)

// gatherCause is an error with the server's own words in it.
const gatherCause = "read the queue depth: database is locked"

// failingCollector reports one metric that can't be gathered.
type failingCollector struct{ desc *prometheus.Desc }

func (c failingCollector) Describe(ch chan<- *prometheus.Desc) { ch <- c.desc }

func (c failingCollector) Collect(ch chan<- prometheus.Metric) {
	ch <- prometheus.NewInvalidMetric(c.desc, errors.New(gatherCause))
}

func TestAFailedMetricsGatherAnswersWithNoErrorTextAndLogsIt(t *testing.T) {
	reg := prometheus.NewRegistry()
	reg.MustRegister(failingCollector{prometheus.NewDesc("wishbone_queue_depth", "Depth.", nil, nil)})
	ok := prometheus.NewCounter(prometheus.CounterOpts{Name: "wishbone_ok_total", Help: "OK."})
	reg.MustRegister(ok)
	var log bytes.Buffer
	h := metricsHandler(reg, slog.New(slog.NewJSONHandler(&log, nil)))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))

	body, err := io.ReadAll(rec.Body)
	require.NoError(t, err)
	require.NotContains(t, string(body), gatherCause)
	require.Contains(t, string(body), "wishbone_ok_total", "the metrics that gathered are served")
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, log.String(), gatherCause)
	require.Contains(t, log.String(), `"level":"ERROR"`)
}
