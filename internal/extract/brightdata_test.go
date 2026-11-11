package extract

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func newBD(url string) *BrightData {
	return NewBrightData(BrightDataOptions{APIKey: "key", Zone: "z1", Endpoint: url})
}

func TestBrightDataOK(t *testing.T) {
	var auth string
	var body map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &body)
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(productPage))
	}))
	defer srv.Close()

	bd := newBD(srv.URL)
	res, err := bd.Fetch(context.Background(), "https://shop.example/p?utm_source=x&id=1")
	require.NoError(t, err)
	require.Equal(t, StatusOK, res.Status)
	require.Equal(t, "Widget", res.Title)
	require.Equal(t, "https://shop.example/p?id=1", res.URL)
	require.Equal(t, "shop.example", res.Store)
	require.Equal(t, []string{"https://shop.example/w.jpg"}, res.Images)
	require.Equal(t, "Bearer key", auth)
	require.Equal(t, map[string]string{"zone": "z1", "url": "https://shop.example/p?id=1", "format": "raw"}, body)
	require.EqualValues(t, 1, bd.Requests())
}

func TestBrightDataNoData(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<html><head><title>Hi</title></head></html>`))
	}))
	defer srv.Close()
	res, err := newBD(srv.URL).Fetch(context.Background(), "https://shop.example/p")
	require.NoError(t, err)
	require.Equal(t, StatusNoData, res.Status)
	require.Equal(t, "Hi", res.Title)
	require.True(t, res.NeedsFallback())
}

func TestBrightDataTargetBlocked(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()
	res, err := newBD(srv.URL).Fetch(context.Background(), "https://shop.example/p")
	require.NoError(t, err)
	require.Equal(t, StatusBlocked, res.Status)
	require.Equal(t, http.StatusForbidden, res.HTTPStatus)
}

func TestBrightDataTargetStatuses(t *testing.T) {
	tests := []struct {
		name   string
		status int
		ct     string
		want   Status
	}{
		{"503 is blocked", http.StatusServiceUnavailable, "text/html", StatusBlocked},
		{"404 is no data", http.StatusNotFound, "text/html", StatusNoData},
		{"non-HTML is no data", http.StatusOK, "application/pdf", StatusNoData},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", tt.ct)
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(productPage))
			}))
			defer srv.Close()
			res, err := newBD(srv.URL).Fetch(context.Background(), "https://shop.example/p")
			require.NoError(t, err)
			require.Equal(t, tt.want, res.Status)
			require.Equal(t, tt.status, res.HTTPStatus)
		})
	}
}

func TestBrightDataUpstreamErrors(t *testing.T) {
	tests := map[string]http.HandlerFunc{
		"brd error header": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-Brd-Err-Code", "client_10010")
			w.Header().Set("X-Brd-Err-Msg", "bad zone")
			w.WriteHeader(http.StatusBadRequest)
		},
		"bad api key": func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusUnauthorized) },
		"server error": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusBadGateway)
		},
	}
	for name, h := range tests {
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewServer(h)
			defer srv.Close()
			bd := newBD(srv.URL)
			_, err := bd.Fetch(context.Background(), "https://shop.example/p")
			require.Error(t, err)
			require.EqualValues(t, 1, bd.Requests())
		})
	}
}

func TestBrightDataTimeout(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()
	defer close(release)
	bd := NewBrightData(BrightDataOptions{APIKey: "k", Zone: "z", Endpoint: srv.URL, Timeout: 50 * time.Millisecond})
	_, err := bd.Fetch(context.Background(), "https://shop.example/p")
	var ne net.Error
	require.ErrorAs(t, err, &ne)
	require.True(t, ne.Timeout())
}

func TestBrightDataDisabled(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true }))
	defer srv.Close()
	for _, o := range []BrightDataOptions{
		{Endpoint: srv.URL},
		{APIKey: "k", Endpoint: srv.URL},
		{Zone: "z", Endpoint: srv.URL},
	} {
		bd := NewBrightData(o)
		require.False(t, bd.Enabled())
		_, err := bd.Fetch(context.Background(), "https://shop.example/p")
		require.ErrorIs(t, err, ErrBrightDataDisabled)
		require.Zero(t, bd.Requests())
	}
	require.False(t, called)
	require.True(t, newBD(srv.URL).Enabled())
}

func TestBrightDataInvalidURL(t *testing.T) {
	bd := newBD("http://127.0.0.1:1")
	_, err := bd.Fetch(context.Background(), "not a url")
	require.Error(t, err)
	require.Zero(t, bd.Requests())
}
