package server

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/stretchr/testify/require"
)

func TestMetricsEndpoint(t *testing.T) {
	reg := prometheus.NewRegistry()
	c := prometheus.NewCounter(prometheus.CounterOpts{Name: "wishbone_test_total", Help: "test"})
	reg.MustRegister(c)
	c.Inc()
	opts, _, _ := testOptions(t)
	opts.Metrics = promhttp.HandlerFor(reg, promhttp.HandlerOpts{})
	ts := httptest.NewServer(New(opts))
	t.Cleanup(ts.Close)

	res := get(t, ts.URL+"/metrics")
	require.Equal(t, http.StatusOK, res.StatusCode)
	body, err := io.ReadAll(res.Body)
	require.NoError(t, err)
	require.Contains(t, string(body), "wishbone_test_total 1")
}

func TestNoMetricsHandlerLeavesRouteUnregistered(t *testing.T) {
	ts := newTestServer(t, fakePinger{})
	require.Equal(t, http.StatusOK, get(t, ts.URL+"/metrics").StatusCode, "falls through to the SPA")
}
