package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"sync"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/stretchr/testify/require"
)

// cause is an error with the server's own words in it, as a store error has.
const cause = "add the first recipient: constraint failed: FOREIGN KEY constraint failed"

// lockedBuffer is a log that a handler writes while the test reads it.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// failingEnv is a server with operations that fail in each way a route can,
// and the log it writes.
func failingEnv(t *testing.T) (*env, *lockedBuffer) {
	t.Helper()
	log := &lockedBuffer{}
	e := newEnv(t, func(o *Options) { o.Logger = slog.New(slog.NewJSONHandler(log, nil)) })
	cfg := huma.DefaultConfig("test", "test")
	cfg.OpenAPIPath, cfg.DocsPath, cfg.SchemasPath = "", "", ""
	api := humachi.New(e.srv.router, cfg)
	huma.Register(api, huma.Operation{
		OperationID: "fail", Method: http.MethodPost, Path: "/api/v2/fail/{id}",
	}, func(context.Context, *struct {
		ID string `path:"id"`
	}) (*struct{}, error) {
		return nil, errors.New(cause)
	})
	huma.Register(api, huma.Operation{
		OperationID: "fail-500", Method: http.MethodGet, Path: "/api/v2/fail-500",
	}, func(context.Context, *struct{}) (*struct{}, error) {
		return nil, huma.Error500InternalServerError(cause)
	})
	huma.Register(api, huma.Operation{
		OperationID: "panic", Method: http.MethodGet, Path: "/api/v2/panic/{id}",
	}, func(context.Context, *struct {
		ID string `path:"id"`
	}) (*struct{}, error) {
		panic(cause)
	})
	huma.Register(api, huma.Operation{
		OperationID: "named", Method: http.MethodPost, Path: "/api/v2/named",
	}, func(context.Context, *struct {
		Body struct {
			Name string `json:"name" minLength:"1"`
		}
	}) (*struct{}, error) {
		return nil, nil
	})
	return e, log
}

// requirePlain500 checks that an answer is a 500 with only the four fields of
// a problem, and none of the cause's words.
func requirePlain500(t *testing.T, res *http.Response) {
	t.Helper()
	require.Equal(t, http.StatusInternalServerError, res.StatusCode)
	require.Contains(t, res.Header.Get("Content-Type"), "application/problem+json")
	var body map[string]any
	require.NoError(t, json.NewDecoder(res.Body).Decode(&body))
	delete(body, "$schema") // huma's link to the schema of a problem
	require.Equal(t, map[string]any{
		"status": float64(http.StatusInternalServerError),
		"title":  "Internal Server Error",
		"code":   "internal_error",
		"detail": internalErrorDetail,
	}, body)
}

func TestAnUnexpectedErrorAnswersAPlain500(t *testing.T) {
	e, _ := failingEnv(t)
	requirePlain500(t, e.do(request{method: http.MethodPost, path: "/fail/1"}))
}

func TestAn500ErrorFromAHandlerAnswersAPlain500(t *testing.T) {
	e, _ := failingEnv(t)
	requirePlain500(t, e.do(request{path: "/fail-500"}))
}

func TestAPanicAnswersAPlain500(t *testing.T) {
	e, _ := failingEnv(t)
	requirePlain500(t, e.do(request{path: "/panic/1"}))
}

func TestTheCauseOfA500IsLogged(t *testing.T) {
	e, log := failingEnv(t)

	e.do(request{method: http.MethodPost, path: "/fail/secret-id"})

	line := findLogLine(t, log.String(), cause)
	require.Equal(t, "ERROR", line["level"])
	require.Equal(t, http.MethodPost, line["method"])
	require.Equal(t, "/api/v2/fail/{id}", line["path"], "the path is the template, with no ids in it")
	require.NotEmpty(t, line["request_id"])
}

func TestTheCauseOfAPanicIsLogged(t *testing.T) {
	e, log := failingEnv(t)

	e.do(request{path: "/panic/secret-id"})

	line := findLogLine(t, log.String(), cause)
	require.Equal(t, "ERROR", line["level"])
	require.Equal(t, http.MethodGet, line["method"])
	require.Equal(t, "/api/v2/panic/{id}", line["path"])
}

func TestA422KeepsItsFieldErrors(t *testing.T) {
	e, _ := failingEnv(t)

	res := e.do(request{method: http.MethodPost, path: "/named", body: map[string]any{"name": ""}})

	require.Equal(t, http.StatusUnprocessableEntity, res.StatusCode)
	var p struct {
		Code   string `json:"code"`
		Errors []struct {
			Location string `json:"location"`
		} `json:"errors"`
	}
	require.NoError(t, json.NewDecoder(res.Body).Decode(&p))
	require.Equal(t, "invalid_input", p.Code)
	require.NotEmpty(t, p.Errors)
	require.Equal(t, "body.name", p.Errors[0].Location)
}

// findLogLine returns the JSON log line that has want in it.
func findLogLine(t *testing.T, log, want string) map[string]any {
	t.Helper()
	for _, raw := range bytes.Split([]byte(log), []byte("\n")) {
		if !bytes.Contains(raw, []byte(want)) {
			continue
		}
		var line map[string]any
		require.NoError(t, json.Unmarshal(raw, &line))
		return line
	}
	require.Failf(t, "no log line", "the log has no line with %q:\n%s", want, log)
	return nil
}
