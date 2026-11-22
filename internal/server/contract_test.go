package server

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"
)

// documentedOperation is what the document says an operation can answer with.
type documentedOperation struct {
	id, method string
	// segments are the parts of the path under the API prefix. One in braces
	// stands for any value.
	segments []string
	// codes are the operation's `x-error-codes`: the codes of each status.
	codes map[string][]string
}

func (o documentedOperation) String() string {
	return o.method + " /" + strings.Join(o.segments, "/")
}

// documented is the error contract of a server, read from its document.
type documented struct {
	operations []documentedOperation
	// address has the codes of an address that has no operation.
	address map[string][]string
}

func documentOf(srv *Server) documented {
	oapi := srv.api.OpenAPI()
	doc := documented{address: oapi.Extensions[errorCodesExtension].(map[string][]string)}
	for path, item := range oapi.Paths {
		for method, op := range map[string]*huma.Operation{
			http.MethodGet: item.Get, http.MethodPost: item.Post, http.MethodPatch: item.Patch,
			http.MethodPut: item.Put, http.MethodDelete: item.Delete,
		} {
			if op == nil {
				continue
			}
			doc.operations = append(doc.operations, documentedOperation{
				id:       op.OperationID,
				method:   method,
				segments: segmentsOf(strings.TrimPrefix(path, apiPrefix)),
				codes:    op.Extensions[errorCodesExtension].(map[string][]string),
			})
		}
	}
	sort.Slice(doc.operations, func(i, j int) bool { return doc.operations[i].id < doc.operations[j].id })
	return doc
}

func segmentsOf(path string) []string {
	return strings.Split(strings.Trim(path, "/"), "/")
}

// find is the operation at a path under the API prefix. The path is one of a
// request (`/users/me`) or one of the route table (`/users/{id}`): a segment in
// braces in the document matches either. Of two operations that fit, the one
// with more fixed segments wins, as in the router.
func (d documented) find(method, path string) (documentedOperation, bool) {
	segments := segmentsOf(path)
	var best documentedOperation
	fixed := -1
	for _, op := range d.operations {
		if op.method != method || len(op.segments) != len(segments) {
			continue
		}
		n, fits := 0, true
		for i, want := range op.segments {
			if strings.HasPrefix(want, "{") {
				continue
			}
			if want != segments[i] {
				fits = false
				break
			}
			n++
		}
		if fits && n > fixed {
			best, fixed = op, n
		}
	}
	return best, fixed >= 0
}

// checked wraps a server for a test, so that every error answer under /api in
// the test is held against the document: the status and the code must be among
// the `x-error-codes` of the operation that the request reached, or among the
// ones at the top of the document when it reached none. Every test that starts
// a server through newEnv or newTestServer runs through it, so a route that
// answers with a code its operation doesn't name fails the test that reaches
// that answer. A route that is in the router and not in the document, such as
// one that a test adds, is not checked.
//
// The answers are reported when the test ends, after the server has closed:
// call this before the cleanup that closes the server is registered.
func checked(t testing.TB, srv *Server) http.Handler {
	doc := documentOf(srv)
	var mu sync.Mutex
	var broken []string
	t.Cleanup(func() {
		mu.Lock()
		defer mu.Unlock()
		slices.Sort(broken)
		for _, b := range slices.Compact(broken) {
			t.Error(b)
		}
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec := &answerRecorder{ResponseWriter: w}
		srv.ServeHTTP(rec, r)
		path := r.URL.Path
		if rec.status < 400 || (path != "/api" && !strings.HasPrefix(path, "/api/")) {
			return
		}
		var p struct {
			Code string `json:"code"`
		}
		_ = json.Unmarshal(rec.body.Bytes(), &p)
		where, allowed := "an address with no operation", doc.address
		under, inVersion := strings.CutPrefix(path, apiPrefix+"/")
		if op, found := doc.find(r.Method, under); inVersion && found {
			where, allowed = op.id, op.codes
		} else if srv.router.Match(chi.NewRouteContext(), r.Method, path) {
			// A route that is no operation of the document: the document's own
			// addresses, and a route that a test added to the router.
			return
		}
		if !slices.Contains(allowed[strconv.Itoa(rec.status)], p.Code) {
			mu.Lock()
			broken = append(broken, fmt.Sprintf("%s %s answered %d %q, which the OpenAPI document doesn't list for %s: name the code in the route's withCodes(...), and in its row of docs/routes.md",
				r.Method, path, rec.status, p.Code, where))
			mu.Unlock()
		}
	})
}

// answerRecorder keeps the status of an answer, and the body of an error.
type answerRecorder struct {
	http.ResponseWriter
	status int
	body   bytes.Buffer
}

func (r *answerRecorder) WriteHeader(status int) {
	if r.status == 0 {
		r.status = status
	}
	r.ResponseWriter.WriteHeader(status)
}

func (r *answerRecorder) Write(b []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	if r.status >= 400 && r.body.Len() < 1<<16 {
		r.body.Write(b)
	}
	return r.ResponseWriter.Write(b)
}

// Unwrap lets http.ResponseController reach the real writer, for the read
// deadline that huma sets on a body.
func (r *answerRecorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }

// routeTable is what docs/routes.md says about codes.
type routeTable struct {
	// everyRoute are the pairs of "Codes on every route": a code that a row
	// needn't repeat.
	everyRoute []errorAnswer
	rows       []routeRow
}

// routeRow is one route of the table, with the codes that its notes give.
type routeRow struct {
	method, path string
	codes        []errorAnswer
}

var (
	everyRouteRow = regexp.MustCompile("^\\| (\\d{3}) \\| `([a-z_]+)` \\|")
	routeRowStart = regexp.MustCompile("^\\| (GET|POST|PATCH|PUT|DELETE|any) \\| `([^`]+)` \\| ")
	// A code in a row's notes is a status and then the code in backticks: "409
	// `setup_complete`".
	codeInNotes = regexp.MustCompile("\\b([45]\\d{2}) `([a-z_]+)`")
)

func readRouteTable(t *testing.T) routeTable {
	t.Helper()
	raw, err := os.ReadFile("../../docs/routes.md")
	require.NoError(t, err)
	var table routeTable
	inEveryRoute := false
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.HasPrefix(line, "#") {
			inEveryRoute = line == "### Codes on every route"
			continue
		}
		if m := everyRouteRow.FindStringSubmatch(line); m != nil && inEveryRoute {
			status, _ := strconv.Atoi(m[1])
			table.everyRoute = append(table.everyRoute, errorAnswer{status, m[2]})
			continue
		}
		if m := routeRowStart.FindStringSubmatch(line); m != nil {
			row := routeRow{method: m[1], path: m[2]}
			for _, c := range codeInNotes.FindAllStringSubmatch(line[len(m[0]):], -1) {
				status, _ := strconv.Atoi(c[1])
				if a := (errorAnswer{status, c[2]}); !slices.Contains(row.codes, a) {
					row.codes = append(row.codes, a)
				}
			}
			table.rows = append(table.rows, row)
		}
	}
	require.NotEmpty(t, table.everyRoute, "docs/routes.md has no table under \"Codes on every route\"")
	require.NotEmpty(t, table.rows)
	return table
}

func pairsOf(codes map[string][]string) []errorAnswer {
	var out []errorAnswer
	for status, names := range codes {
		n, _ := strconv.Atoi(status)
		for _, name := range names {
			out = append(out, errorAnswer{n, name})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].status < out[j].status || (out[i].status == out[j].status && out[i].code < out[j].code)
	})
	return out
}

func (a errorAnswer) String() string { return strconv.Itoa(a.status) + " `" + a.code + "`" }

// The codes of a route are in two places: the route table, which is what a
// person reads, and `x-error-codes` in the OpenAPI document, which is what a
// client is built from and checked against. This holds them equal, route by
// route:
//
//   - A code in a route's row is in the document for that route.
//   - A code that the document gives a route is in that route's row, or in the
//     table of "Codes on every route", which a row needn't repeat.
//   - A code of "Codes on every route" is in the document somewhere, with the
//     status that the table gives it.
//
// It also holds the two lists of routes equal: an operation has a row. The row
// of `/api/v{N}/*` is the codes at the top of the document, which belong to no
// operation.
func TestErrorCodesMatchTheRouteTable(t *testing.T) {
	t.Parallel()
	opts, _, _ := testOptions(t)
	doc := documentOf(New(opts))
	table := readRouteTable(t)

	rowsOf := map[string][]errorAnswer{} // by operation ID
	var address []errorAnswer
	for _, row := range table.rows {
		if row.method == "any" {
			address = append(address, row.codes...)
			continue
		}
		op, found := doc.find(row.method, row.path)
		if !found {
			// The contract's own addresses are no operations of the document.
			require.Contains(t, []string{"/openapi", "/docs", "/schemas/{name}"}, row.path,
				"docs/routes.md has %s %s, which is no operation of the document", row.method, row.path)
			require.Empty(t, row.codes, "docs/routes.md gives codes for %s %s, which is no operation of the document", row.method, row.path)
			continue
		}
		rowsOf[op.id] = append(rowsOf[op.id], row.codes...)
		for _, c := range row.codes {
			if !slices.Contains(pairsOf(op.codes), c) {
				t.Errorf("docs/routes.md gives %s %s the code %s, and the document doesn't give it to %s: name it in the route's withCodes(...), or take it out of the row", row.method, row.path, c, op.id)
			}
		}
	}

	everywhere := map[errorAnswer]bool{}
	check := func(where string, got, row []errorAnswer) {
		for _, c := range got {
			everywhere[c] = true
			if slices.Contains(table.everyRoute, c) || slices.Contains(row, c) {
				continue
			}
			t.Errorf("the document gives %s the code %s, and docs/routes.md doesn't: add it to the route's row, or to \"Codes on every route\" with this status", where, c)
		}
	}
	for _, op := range doc.operations {
		row, has := rowsOf[op.id]
		require.True(t, has, "%s (%s) has no row in docs/routes.md", op, op.id)
		check(op.String(), pairsOf(op.codes), row)
	}
	check("an address with no operation", pairsOf(doc.address), address)
	for _, c := range address {
		require.Contains(t, pairsOf(doc.address), c, "docs/routes.md gives /api/v{N}/* the code %s, and the top of the document doesn't have it", c)
	}
	for _, c := range table.everyRoute {
		require.True(t, everywhere[c], "docs/routes.md has %s under \"Codes on every route\", and the document gives it to no operation", c)
	}
}

// An operation's error responses and its `x-error-codes` say the same: every
// status that it can answer with has a code, and every code has its status. No
// operation has a `default` response in place of them. Every one can answer
// 500, and every code in the server's list is one that some operation names.
func TestEveryErrorResponseHasItsCodes(t *testing.T) {
	t.Parallel()
	opts, _, _ := testOptions(t)
	srv := New(opts)
	named := map[string]bool{}
	for _, op := range operations(srv.api.OpenAPI()) {
		codes := op.Extensions[errorCodesExtension].(map[string][]string)
		var statuses []string
		for status, res := range op.Responses {
			require.NotEqual(t, "default", status, op.OperationID)
			if n, _ := strconv.Atoi(status); n < 400 {
				continue
			}
			statuses = append(statuses, status)
			require.Equal(t, "#/components/schemas/Problem", res.Content["application/problem+json"].Schema.Ref, "%s %s", op.OperationID, status)
			for _, code := range codes[status] {
				require.Contains(t, res.Description, "`"+code+"`", "%s %s", op.OperationID, status)
			}
		}
		for status, names := range codes {
			require.Contains(t, statuses, status, "%s lists codes for a status it has no response for", op.OperationID)
			require.NotEmpty(t, names, "%s %s", op.OperationID, status)
			require.True(t, sort.StringsAreSorted(names), "%s %s", op.OperationID, status)
			for _, name := range names {
				named[name] = true
			}
		}
		require.Len(t, statuses, len(codes), "%s has an error response with no code", op.OperationID)
		require.Equal(t, []string{"internal_error"}, codes["500"], op.OperationID)
	}
	for name := range codeStatus {
		require.True(t, named[name], "codeStatus has %q, and no operation names it", name)
	}
}

// documentJSON is the OpenAPI document as a client gets it.
func documentJSON(t *testing.T) map[string]any {
	t.Helper()
	opts, _, _ := testOptions(t)
	rec := httptest.NewRecorder()
	New(opts).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v2/openapi.json", nil))
	require.Equal(t, http.StatusOK, rec.Code)
	var doc map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &doc))
	return doc
}

// schemasUnder visits a schema of the document, and every schema under it
// unless visit says not to go under it. It follows each reference once.
func schemasUnder(doc map[string]any, node any, seen map[string]bool, visit func(path string, schema map[string]any) (under bool), path string) {
	switch n := node.(type) {
	case map[string]any:
		if ref, ok := n["$ref"].(string); ok {
			name := strings.TrimPrefix(ref, "#/components/schemas/")
			if !seen[name] {
				seen[name] = true
				schemas := doc["components"].(map[string]any)["schemas"].(map[string]any)
				schemasUnder(doc, schemas[name], seen, visit, name)
			}
			return
		}
		if !visit(path, n) {
			return
		}
		for _, key := range []string{"properties", "items", "additionalProperties", "anyOf", "oneOf", "allOf", "not"} {
			if key == "properties" {
				props, _ := n[key].(map[string]any)
				for name, p := range props {
					schemasUnder(doc, p, seen, visit, path+"."+name)
				}
				continue
			}
			schemasUnder(doc, n[key], seen, visit, path+"."+key)
		}
	case []any:
		for i, sub := range n {
			schemasUnder(doc, sub, seen, visit, path+"."+strconv.Itoa(i))
		}
	}
}

// eachOperation calls f with every operation of the document.
func eachOperation(doc map[string]any, f func(name string, op map[string]any)) {
	for path, item := range doc["paths"].(map[string]any) {
		for method, op := range item.(map[string]any) {
			f(strings.ToUpper(method)+" "+path, op.(map[string]any))
		}
	}
}

// The spec lets the server add a response field and an enum value within an
// API version (docs/spec.md, Versions of the API). So no schema that a
// response can hold refuses a property that it doesn't list, and none has a
// closed enum: each enum is `anyOf` its values and any string, which a
// generated client reads as an enum with an unknown case.
func TestResponseSchemasAllowWhatTheSpecAllows(t *testing.T) {
	t.Parallel()
	doc := documentJSON(t)
	seen := map[string]bool{}
	objects, enums := 0, 0
	eachOperation(doc, func(name string, op map[string]any) {
		for status, res := range op["responses"].(map[string]any) {
			response := res.(map[string]any)
			for _, group := range []string{"content", "headers"} {
				for key, part := range mapOf(response[group]) {
					schemasUnder(doc, mapOf(part)["schema"], seen, func(path string, s map[string]any) bool {
						where := name + " " + status + " " + key + " " + path
						require.NotEqual(t, false, s["additionalProperties"], "%s refuses other properties", where)
						if s["type"] == "object" {
							objects++
						}
						if values, ok := s["enum"]; ok {
							require.Fail(t, "a closed enum in a response", "%s: %v", where, values)
						}
						if anyOf, ok := s["anyOf"].([]any); ok {
							// An open enum: the values of today, or any string.
							require.Len(t, anyOf, 2, where)
							known, open := mapOf(anyOf[0]), mapOf(anyOf[1])
							require.NotEmpty(t, known["enum"], where)
							require.Equal(t, "string", known["type"], where)
							require.Equal(t, map[string]any{"type": "string"}, open, where)
							enums++
							return false
						}
						return true
					}, "")
				}
			}
		}
	})
	// The walk found the schemas: it didn't pass because it saw nothing.
	require.Greater(t, objects, 50)
	require.GreaterOrEqual(t, enums, 7)
	for _, name := range []string{"UserBody", "GiftBody", "GiftFlag", "Problem", "ErrorDetail", "PartialDate", "RuleBody"} {
		require.True(t, seen[name], "%s is in no response", name)
	}
}

func mapOf(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}

// A request stays as strict as the server is: a body refuses a field that it
// doesn't know, and an enum in a request lists the only values the server
// takes. Loosening a request is allowed within an API version, and is no part
// of opening the responses.
func TestRequestSchemasStayStrict(t *testing.T) {
	t.Parallel()
	doc := documentJSON(t)
	schemas := doc["components"].(map[string]any)["schemas"].(map[string]any)
	for _, name := range []string{"CreateGiftInputBody", "CreateItemInputBody", "CreateUserInputBody", "SetupInputBody", "RuleInput", "PartialDateInput"} {
		require.Equal(t, false, mapOf(schemas[name])["additionalProperties"], name)
	}
	kind := mapOf(mapOf(mapOf(schemas["CreateOccasionInputBody"])["properties"])["kind"])
	require.Equal(t, []any{"custom", "shared"}, kind["enum"])
	session := mapOf(mapOf(mapOf(schemas["RedeemLoginLinkInputBody"])["properties"])["session"])
	require.Equal(t, []any{"cookie", "bearer"}, session["enum"])

	gifts := mapOf(mapOf(mapOf(doc["paths"])["/api/v2/gifts"])["get"])
	var state map[string]any
	for _, p := range gifts["parameters"].([]any) {
		if mapOf(p)["name"] == "state" {
			state = mapOf(mapOf(p)["schema"])
		}
	}
	require.Equal(t, []any{"open", "given"}, state["enum"])
}

// The server still refuses what the document says a request refuses, and
// answers with the codes that the document gives the route for it.
func TestRequestsAreRefusedAsTheDocumentSays(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	_, token := e.signUp()
	admin := cookie(token)
	post := func(path string, raw []byte, contentType string) *http.Response {
		return e.do(request{method: http.MethodPost, path: path, header: admin, raw: raw, contentType: contentType})
	}
	refused := func(res *http.Response, status int, code string) {
		t.Helper()
		require.Equal(t, status, res.StatusCode)
		require.Equal(t, code, readProblem(t, res).Code)
	}

	// An unknown field in a body, and an unknown value of a request enum.
	refused(e.do(request{method: http.MethodPost, path: "/groups", header: admin, body: map[string]any{"name": "Family", "color": "red"}}),
		http.StatusUnprocessableEntity, "invalid_input")
	refused(e.do(request{path: "/gifts?state=wrapped", header: admin}), http.StatusUnprocessableEntity, "invalid_input")

	// A body that isn't JSON, a type the server doesn't know, and a body over
	// the route's limit.
	refused(post("/groups", []byte(`{"name":`), "application/json"), http.StatusBadRequest, "bad_request")
	refused(post("/groups", []byte(`name=Family`), "application/x-www-form-urlencoded"), http.StatusUnsupportedMediaType, "bad_request")
	refused(post("/groups", bytes.Repeat([]byte(" "), 1<<20+1), "application/json"), http.StatusRequestEntityTooLarge, "request_too_large")

	// A search with no body at all, and one that is not a form.
	refused(e.do(request{method: http.MethodPost, path: "/search", header: admin}), http.StatusBadRequest, "bad_request")
	refused(post("/search", []byte("kettle"), "application/octet-stream"), http.StatusUnprocessableEntity, "invalid_input")
}

// huma gives a body 5 seconds to arrive, and answers a body that takes longer
// with a 408. It has no code of its own, so it carries `bad_request`, as the
// document says for every route that takes a body. The test takes those 5
// seconds, because the time is huma's default for an operation.
func TestABodyThatDoesNotArriveInTimeIsA408(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	_, token := e.signUp()
	conn, err := net.Dial("tcp", strings.TrimPrefix(e.ts.URL, "http://"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	// The headers promise 20 bytes, and 4 come.
	_, err = io.WriteString(conn, "POST /api/v2/groups HTTP/1.1\r\nHost: wish.example\r\nContent-Type: application/json\r\n"+
		"Content-Length: 20\r\nCookie: "+sessionCookieName+"="+token+"\r\n\r\n{\"na")
	require.NoError(t, err)
	res, err := http.ReadResponse(bufio.NewReader(conn), nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = res.Body.Close() })
	require.Equal(t, http.StatusRequestTimeout, res.StatusCode)
	require.Equal(t, "bad_request", readProblem(t, res).Code)
}

// POST /search takes a form and nothing else, and the document says so.
func TestSearchTakesOnlyAForm(t *testing.T) {
	t.Parallel()
	doc := documentJSON(t)
	search := mapOf(mapOf(mapOf(doc["paths"])["/api/v2/search"])["post"])
	content := mapOf(mapOf(search["requestBody"])["content"])
	require.Len(t, content, 1)
	require.Contains(t, content, "multipart/form-data")
}

// The document says in words what a client must accept, and lists the codes of
// an address that has no operation.
func TestTheDocumentSaysWhatAClientMustAccept(t *testing.T) {
	t.Parallel()
	doc := documentJSON(t)
	description := mapOf(doc["info"])["description"].(string)
	for _, part := range []string{"ignore a response field", "accept an enum value", "`detail`", "`next_cursor`", errorCodesExtension} {
		require.Contains(t, description, part)
	}
	require.Equal(t, map[string]any{
		"404": []any{"not_found"},
		"405": []any{"method_not_allowed"},
		"410": []any{"api_version_removed"},
	}, doc[errorCodesExtension])
}

// A schema that a request and a response share can't be opened for one and
// kept strict for the other, so building the server refuses it.
func TestARequestAndAResponseCannotShareAClosedSchema(t *testing.T) {
	t.Parallel()
	type Shared struct {
		Kind string `json:"kind" enum:"a,b"`
	}
	type in struct{ Body Shared }
	type out struct{ Body Shared }
	api := humachi.New(chi.NewRouter(), huma.DefaultConfig("test", "test"))
	huma.Register(api, huma.Operation{OperationID: "share", Method: http.MethodPost, Path: "/share"},
		func(context.Context, *in) (*out, error) { return &out{}, nil })
	require.PanicsWithValue(t,
		"server: a request and a response share a schema that only the response may open: Shared; an enum of [a b]. Give the request a type of its own.",
		func() { finishContract(api) })
}
