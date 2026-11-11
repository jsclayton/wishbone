package server

import (
	"encoding/json"
	"errors"
	"flag"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/stretchr/testify/require"
)

// problem is the body of every 4xx and 5xx answer: RFC 9457 with a code.
type problem struct {
	Status int    `json:"status"`
	Title  string `json:"title"`
	Code   string `json:"code"`
	Detail string `json:"detail"`
}

func readProblem(t *testing.T, res *http.Response) problem {
	t.Helper()
	require.Contains(t, res.Header.Get("Content-Type"), "application/problem+json")
	var p problem
	require.NoError(t, json.NewDecoder(res.Body).Decode(&p))
	require.Equal(t, res.StatusCode, p.Status)
	require.NotEmpty(t, p.Title)
	return p
}

func do(t *testing.T, method, url string) *http.Response {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), method, url, strings.NewReader(`{}`))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	t.Cleanup(func() { res.Body.Close() })
	return res
}

func TestHealthIsServedUnderTheAPIPrefix(t *testing.T) {
	ts := newTestServer(t, fakePinger{})

	res := get(t, ts.URL+"/api/v2/health")
	require.Equal(t, http.StatusOK, res.StatusCode)
	var body struct{ Status, Version, Commit string }
	require.NoError(t, json.NewDecoder(res.Body).Decode(&body))
	require.Equal(t, "ok", body.Status)
	require.Equal(t, "test", body.Version)
	require.Equal(t, "abc123", body.Commit)
}

func TestHealthWithTheDatabaseDownIsACodedProblem(t *testing.T) {
	ts := newTestServer(t, fakePinger{err: errors.New("boom")})

	res := get(t, ts.URL+"/api/v2/health")
	require.Equal(t, http.StatusServiceUnavailable, res.StatusCode)
	require.Equal(t, "service_unavailable", readProblem(t, res).Code)
}

func TestRetiredAPIVersionsAnswer410(t *testing.T) {
	ts := newTestServer(t, fakePinger{})

	for _, tc := range []struct{ method, path string }{
		{http.MethodGet, "/api/v1/health"},
		{http.MethodGet, "/api/v1/me"},
		{http.MethodPost, "/api/v1/login-links/redeem"},
		{http.MethodPut, "/api/v1/me"},
		{http.MethodDelete, "/api/v1/me/items/3"},
		{http.MethodGet, "/api/v1/openapi.json"},
		{http.MethodGet, "/api/v1/nothing/at/all"},
		{http.MethodGet, "/api/v1/"},
		{http.MethodGet, "/api/v1"},
		{http.MethodGet, "/api/v0/x"},
		{http.MethodGet, "/api/v0"},
	} {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			res := do(t, tc.method, ts.URL+tc.path)
			require.Equal(t, http.StatusGone, res.StatusCode)
			require.Equal(t, "no-store", res.Header.Get("Cache-Control"))
			p := readProblem(t, res)
			require.Equal(t, "api_version_removed", p.Code)
			require.NotEmpty(t, p.Detail)
		})
	}
}

func TestRetiredAPIVersionAnswerNamesTheVersionAndTheActiveOne(t *testing.T) {
	ts := newTestServer(t, fakePinger{})

	res := get(t, ts.URL+"/api/v1/me")
	require.Equal(t, http.StatusGone, res.StatusCode)
	require.Equal(t, "Version 1 of the API has been removed. Use /api/v2.", readProblem(t, res).Detail)
}

func TestTheActiveAPIVersionIsNotRetired(t *testing.T) {
	ts := newTestServer(t, fakePinger{})

	res := get(t, ts.URL+"/api/v2/health")
	require.Equal(t, http.StatusOK, res.StatusCode)
}

func TestAPIVersionsAboveTheActiveOnesAnswerAsUnknownPaths(t *testing.T) {
	ts := newTestServer(t, fakePinger{})

	for _, path := range []string{"/api/v3/x", "/api/v3", "/api/v10/health", "/api/v99999999999999999999/x"} {
		t.Run(path, func(t *testing.T) {
			res := get(t, ts.URL+path)
			require.Equal(t, http.StatusNotFound, res.StatusCode)
			require.Equal(t, "not_found", readProblem(t, res).Code)
		})
	}
}

func TestMalformedAPIVersionsAnswerAsUnknownPaths(t *testing.T) {
	ts := newTestServer(t, fakePinger{})

	for _, path := range []string{"/api/vx/", "/api/vx/me", "/api/v01/me", "/api/v-1/me", "/api/v/me", "/api/1/me", "/api/V1/me", "/api/v1x/me", "/api/v+1/me"} {
		t.Run(path, func(t *testing.T) {
			res := get(t, ts.URL+path)
			require.Equal(t, http.StatusNotFound, res.StatusCode)
			require.Equal(t, "not_found", readProblem(t, res).Code)
		})
	}
}

func TestRetiredVersionsAreEveryWholeNumberBelowTheLowestActiveOne(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTeapot) })
	h := retiredAPIVersions([]int{3, 4}, next)

	for path, want := range map[string]int{
		"/api/v0/x":   http.StatusGone,
		"/api/v1":     http.StatusGone,
		"/api/v2/x":   http.StatusGone,
		"/api/v3/x":   http.StatusTeapot,
		"/api/v4/x":   http.StatusTeapot,
		"/api/v5/x":   http.StatusTeapot,
		"/api/v02/x":  http.StatusTeapot,
		"/apiv1/x":    http.StatusTeapot,
		"/other/v1/x": http.StatusTeapot,
	} {
		t.Run(path, func(t *testing.T) {
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, http.NoBody))
			require.Equal(t, want, rec.Code)
		})
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v2/x", http.NoBody))
	require.Contains(t, rec.Body.String(), "Version 2 of the API has been removed. Use /api/v4.")
}

func TestTheAPIPrefixIsTheNewestActiveVersion(t *testing.T) {
	require.Equal(t, "/api/v"+strconv.Itoa(slices.Max(activeAPIVersions)), apiPrefix)
}

func TestUnknownAPIPathsAre404WithACode(t *testing.T) {
	ts := newTestServer(t, fakePinger{})

	for _, path := range []string{"/api/v2/nope", "/api/v2", "/api/nope", "/api/v3/health"} {
		t.Run(path, func(t *testing.T) {
			res := get(t, ts.URL+path)
			require.Equal(t, http.StatusNotFound, res.StatusCode)
			require.Equal(t, "no-store", res.Header.Get("Cache-Control"))
			require.Equal(t, "not_found", readProblem(t, res).Code)
		})
	}
}

func TestWrongMethodIsA405WithACode(t *testing.T) {
	ts := newTestServer(t, fakePinger{})

	res := do(t, http.MethodPost, ts.URL+"/api/v2/health")
	require.Equal(t, http.StatusMethodNotAllowed, res.StatusCode)
	require.Equal(t, "method_not_allowed", readProblem(t, res).Code)
}

// Huma writes its own errors (a body that fails validation, for one) with
// huma.NewError, so every one of them carries a code too.
func TestHumaErrorsCarryACodeFromTheirStatus(t *testing.T) {
	for _, tc := range []struct {
		err  huma.StatusError
		code string
	}{
		{huma.Error400BadRequest("bad"), "bad_request"},
		{huma.Error401Unauthorized("no"), "unauthorized"},
		{huma.Error403Forbidden("no"), "forbidden"},
		{huma.Error404NotFound("gone"), "not_found"},
		{huma.Error409Conflict("clash"), "conflict"},
		{huma.Error412PreconditionFailed("changed"), "resource_changed"},
		{huma.Error422UnprocessableEntity("bad", &huma.ErrorDetail{Location: "body.name", Message: "required"}), "invalid_input"},
		{huma.Error429TooManyRequests("slow"), "rate_limited"},
		{huma.Error500InternalServerError("oops"), "internal_error"},
		{huma.Error503ServiceUnavailable("down"), "service_unavailable"},
	} {
		t.Run(tc.code, func(t *testing.T) {
			raw, err := json.Marshal(tc.err)
			require.NoError(t, err)
			var got struct {
				Status int    `json:"status"`
				Code   string `json:"code"`
				Detail string `json:"detail"`
				Errors []struct {
					Location string `json:"location"`
				} `json:"errors"`
			}
			require.NoError(t, json.Unmarshal(raw, &got))
			require.Equal(t, tc.err.GetStatus(), got.Status)
			require.Equal(t, tc.code, got.Code)
			require.NotEmpty(t, got.Detail)
		})
	}
}

func TestAProblemWithItsOwnCode(t *testing.T) {
	raw, err := json.Marshal(Error(http.StatusConflict, "item_fully_claimed", "Someone else just claimed the last one."))
	require.NoError(t, err)
	require.JSONEq(t, `{"status":409,"title":"Conflict","code":"item_fully_claimed","detail":"Someone else just claimed the last one."}`, string(raw))
}

func TestOpenAPIAndDocsAreServedUnderV2(t *testing.T) {
	ts := newTestServer(t, fakePinger{})

	for _, path := range []string{"/api/v2/openapi", "/api/v2/openapi.json", "/api/v2/openapi.yaml", "/api/v2/docs"} {
		res := get(t, ts.URL+path)
		require.Equal(t, http.StatusOK, res.StatusCode, path)
	}
	res := get(t, ts.URL+"/api/v2/openapi")
	var doc struct {
		OpenAPI string `json:"openapi"`
		Info    struct{ Title, Version string }
	}
	require.NoError(t, json.NewDecoder(res.Body).Decode(&doc))
	require.NotEmpty(t, doc.OpenAPI)
	require.Equal(t, "test", doc.Info.Version)
}

func TestSchemasTheOpenAPIDocumentRefersToAreServed(t *testing.T) {
	ts := newTestServer(t, fakePinger{})

	raw, err := io.ReadAll(get(t, ts.URL+"/api/v2/openapi.json").Body)
	require.NoError(t, err)
	var doc struct {
		Paths map[string]struct {
			Get struct {
				Responses map[string]struct {
					Content map[string]struct {
						Schema struct {
							Ref string `json:"$ref"`
						}
					}
				}
			}
		}
	}
	require.NoError(t, json.Unmarshal(raw, &doc))
	ref := doc.Paths["/api/v2/health"].Get.Responses["200"].Content["application/json"].Schema.Ref
	require.Contains(t, ref, "#/components/schemas/")
	name := strings.TrimPrefix(ref, "#/components/schemas/")

	res := get(t, ts.URL+"/api/v2/schemas/"+name+".json")
	require.Equal(t, http.StatusOK, res.StatusCode)
}

var update = flag.Bool("update", false, "rewrite testdata/openapi.json from the server's OpenAPI document")

// The OpenAPI document is the contract (docs/spec.md, Versions of the API).
// This compares it with a copy in the repo, so a change to the contract shows
// in the PR diff. After an intended change, run
// `go test ./internal/server -run OpenAPIDocument -update` and commit the copy.
func TestOpenAPIDocumentMatchesTheCopyInTheRepo(t *testing.T) {
	const snapshot = "testdata/openapi.json"
	rec := httptest.NewRecorder()
	opts, _, _ := testOptions(t)
	New(opts).
		ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v2/openapi.json", nil))
	require.Equal(t, http.StatusOK, rec.Code)
	var pretty any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &pretty))
	got, err := json.MarshalIndent(pretty, "", "  ")
	require.NoError(t, err)
	got = append(got, '\n')

	if *update {
		require.NoError(t, os.MkdirAll("testdata", 0o750))
		require.NoError(t, os.WriteFile(snapshot, got, 0o600))
	}
	want, err := os.ReadFile(snapshot)
	require.NoError(t, err, "no copy of the OpenAPI document: run the test with -update")
	require.Equal(t, string(want), string(got), "the contract changed: run the test with -update and commit testdata/openapi.json")
}
