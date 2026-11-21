package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

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
	e, log, _ := failingEnvWaiting(t)
	return e, log
}

// failingEnvWaiting is failingEnv, and a channel that GET /wait closes when
// its handler starts to wait for the client to go away.
func failingEnvWaiting(t *testing.T) (*env, *lockedBuffer, <-chan struct{}) {
	t.Helper()
	waiting := make(chan struct{})
	log := &lockedBuffer{}
	e := newEnv(t, func(o *Options) { o.Logger = slog.New(slog.NewJSONHandler(log, nil)) })
	cfg := huma.DefaultConfig("test", "test")
	cfg.OpenAPIPath, cfg.DocsPath, cfg.SchemasPath = "", "", ""
	cfg.Transformers = append(cfg.Transformers, logServerErrors) // as New sets it
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
		OperationID: "fail-500-cause", Method: http.MethodGet, Path: "/api/v2/fail-500-cause",
	}, func(context.Context, *struct{}) (*struct{}, error) {
		return nil, Error(http.StatusInternalServerError, "internal_error", "save the item", errors.New(cause))
	})
	huma.Register(api, huma.Operation{
		OperationID: "fail-502", Method: http.MethodGet, Path: "/api/v2/fail-502",
	}, func(context.Context, *struct{}) (*struct{}, error) {
		return nil, huma.NewError(http.StatusBadGateway, "The link reader failed.", errors.New(cause))
	})
	huma.Register(api, huma.Operation{
		OperationID: "fail-503", Method: http.MethodGet, Path: "/api/v2/fail-503",
	}, func(context.Context, *struct{}) (*struct{}, error) {
		return nil, huma.Error503ServiceUnavailable("database unavailable", errors.New(cause))
	})
	huma.Register(api, huma.Operation{
		OperationID: "wait", Method: http.MethodGet, Path: "/api/v2/wait",
	}, func(ctx context.Context, _ *struct{}) (*struct{}, error) {
		close(waiting)
		<-ctx.Done()
		// As a store call fails when its context is canceled.
		return nil, fmt.Errorf("load the list: %w", ctx.Err())
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
	return e, log, waiting
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

func TestA500MadeBeforeTheRequestLogsThroughTheServersLogger(t *testing.T) {
	e, log := failingEnv(t)

	e.do(request{path: "/fail-500"})

	line := findLogLine(t, log.String(), cause)
	require.Equal(t, "ERROR", line["level"])
	require.Equal(t, "/api/v2/fail-500", line["path"])
	require.NotEmpty(t, line["request_id"])
}

func TestTheCauseGivenToErrorIsLogged(t *testing.T) {
	e, log := failingEnv(t)

	requirePlain500(t, e.do(request{path: "/fail-500-cause"}))

	line := findLogLine(t, log.String(), cause)
	require.Equal(t, "ERROR", line["level"])
	require.Equal(t, "save the item", line["detail"])
	require.Equal(t, 1, strings.Count(log.String(), cause), "the cause is logged once")
}

func TestAnother5xxAnswersWithNoErrorsAndLogsItsCause(t *testing.T) {
	for _, tc := range []struct {
		path   string
		status int
		code   string
	}{
		{"/fail-502", http.StatusBadGateway, "internal_error"},
		{"/fail-503", http.StatusServiceUnavailable, "service_unavailable"},
	} {
		t.Run(tc.path, func(t *testing.T) {
			e, log := failingEnv(t)

			res := e.do(request{path: tc.path})

			require.Equal(t, tc.status, res.StatusCode)
			var body map[string]any
			require.NoError(t, json.NewDecoder(res.Body).Decode(&body))
			delete(body, "$schema")
			require.ElementsMatch(t, []string{"status", "title", "code", "detail"}, keys(body))
			require.Equal(t, tc.code, body["code"])
			require.NotContains(t, body["detail"], "constraint")
			line := findLogLine(t, log.String(), cause)
			require.Equal(t, "ERROR", line["level"])
			require.Equal(t, float64(tc.status), line["status"])
		})
	}
}

func TestAClientThatDropsARequestWritesNoErrorLine(t *testing.T) {
	e, log, waiting := failingEnvWaiting(t)
	ctx, cancel := context.WithCancel(t.Context())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, e.ts.URL+"/api/v2/wait", nil)
	require.NoError(t, err)
	go func() {
		<-waiting
		cancel()
	}()

	_, err = http.DefaultClient.Do(req)
	require.ErrorIs(t, err, context.Canceled)

	requireClientWentAway(t, log, "load the list")
}

func TestACutOffBodyWritesNoErrorLine(t *testing.T) {
	e, log := failingEnv(t)
	conn, err := net.Dial("tcp", strings.TrimPrefix(e.ts.URL, "http://"))
	require.NoError(t, err)
	defer conn.Close()

	_, err = fmt.Fprint(conn, "POST /api/v2/named HTTP/1.1\r\nHost: wishbone\r\n"+
		"Content-Type: application/json\r\nContent-Length: 100\r\n\r\n{\"na")
	require.NoError(t, err)
	require.NoError(t, conn.(*net.TCPConn).CloseWrite())

	requireClientWentAway(t, log, "cannot read request body") // huma's detail
}

// requireClientWentAway waits for the INFO line of a client that went away,
// with want in it, and checks that the log has no ERROR line.
func requireClientWentAway(t *testing.T, log *lockedBuffer, want string) {
	t.Helper()
	require.Eventually(t, func() bool { return strings.Contains(log.String(), "client went away") },
		5*time.Second, 10*time.Millisecond, "no line for the client that went away:\n%s", log)
	line := findLogLine(t, log.String(), "client went away")
	require.Equal(t, "INFO", line["level"])
	require.Contains(t, fmt.Sprint(line["error"], line["detail"]), want)
	require.NotContains(t, log.String(), `"level":"ERROR"`)
}

func keys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// loggedEnv is a server whose log the test reads.
func loggedEnv(t *testing.T, change ...func(*Options)) (*env, *lockedBuffer) {
	t.Helper()
	log := &lockedBuffer{}
	change = append(change, func(o *Options) { o.Logger = slog.New(slog.NewJSONHandler(log, nil)) })
	return newEnv(t, change...), log
}

// The one test of the log on the API that New builds: every other one uses
// failingEnv's own API.
func TestA503OnTheServersOwnAPIIsLoggedOnce(t *testing.T) {
	e, log := loggedEnv(t, func(o *Options) { o.Store = fakePinger{err: errors.New(cause)} })

	res := e.do(request{path: "/health"})

	require.Equal(t, http.StatusServiceUnavailable, res.StatusCode)
	require.Equal(t, 1, strings.Count(log.String(), cause))
	line := findLogLine(t, log.String(), cause)
	require.Equal(t, "ERROR", line["level"])
	require.Equal(t, "/api/v2/health", line["path"])
	require.NotEmpty(t, line["request_id"])
}

func TestAFailedIdempotencyKeyLookupIsLoggedOnce(t *testing.T) {
	e, log := loggedEnv(t)
	_, token := e.signUp()
	_, err := e.db.Exec("DROP TABLE idempotency_keys")
	require.NoError(t, err)

	res := e.do(request{method: http.MethodPost, path: "/lists", body: map[string]any{"name": "Wishes"}, header: keyed(token, "key-1")})

	requirePlain500(t, res)
	require.Equal(t, 1, strings.Count(log.String(), `"level":"ERROR"`), "one ERROR line for one 500:\n%s", log)
	line := findLogLine(t, log.String(), "idempotency_keys")
	require.Equal(t, "/api/v2/lists", line["path"])
	require.NotEmpty(t, line["request_id"])
}

func TestAClientGoneWhenTheCallerIsFoundWritesNoErrorLine(t *testing.T) {
	e, log := loggedEnv(t)
	_, token := e.signUp()
	req := httptest.NewRequest(http.MethodGet, "/api/v2/users/me", nil)
	req.Header = cookie(token)
	ctx, cancel := context.WithCancel(req.Context())
	cancel() // as net/http does when the client closes the connection

	e.srv.ServeHTTP(httptest.NewRecorder(), req.WithContext(ctx))

	line := findLogLine(t, log.String(), "client went away")
	require.Equal(t, "INFO", line["level"])
	require.NotEmpty(t, line["request_id"])
	require.NotContains(t, log.String(), `"level":"ERROR"`)
}
