package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"log/slog"
	"mime"
	"mime/multipart"
	"net/http"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"

	"jsclayton.dev/wishbone/internal/idempotency"
)

// idempotencyKeyHeader is the header of a write that a client can send again
// (docs/spec.md, API principles, Writes, Retries).
const idempotencyKeyHeader = "Idempotency-Key"

// answerNeverKept is the key of an operation's Metadata that marks a write
// whose answer is never kept for a repeat, so a repeat does the work again.
// Two kinds of write have it: one whose answer holds a secret that the server
// keeps nowhere (a login link), and one whose answer is over long before the
// 24 hours are (the options of a passkey registration). The value is the
// reason, as the words after "because" in the document of the header.
const answerNeverKept = "answerNeverKept"

// keptHeaders are the headers of an answer that a repeat gets again. Any other
// header is of the one response, such as Set-Cookie, and is never kept.
var keptHeaders = []string{"Content-Type", "Location", "ETag", "Link"}

// takesIdempotencyKey says whether a method is a write, which takes the key.
func takesIdempotencyKey(method string) bool {
	return method == http.MethodPost || method == http.MethodPatch || method == http.MethodDelete
}

// documentIdempotencyKey puts the key and its two codes in the document of
// every write on the signed-in group. huma runs a group's modifiers more than
// once on an operation, so it adds only what is missing.
func documentIdempotencyKey(api huma.API) func(*huma.Operation) {
	problem := map[string]*huma.MediaType{
		"application/problem+json": {Schema: api.OpenAPI().Components.Schemas.Schema(reflect.TypeFor[Problem](), true, "")},
	}
	return func(op *huma.Operation) {
		if !takesIdempotencyKey(op.Method) {
			return
		}
		if !slices.ContainsFunc(op.Parameters, func(p *huma.Param) bool { return p.In == "header" && p.Name == idempotencyKeyHeader }) {
			desc := "Optional, at most " + strconv.Itoa(idempotency.MaxKeyLength) + " bytes. A repeat of this write with the same key, from the same session, " +
				"within 24 hours of the first request, gets the first 2xx answer again, and the server does no work again. " +
				"The same key with another method, path or body is a 422 idempotency_key_reused. " +
				"A repeat while the first request is still running is a 409 idempotency_key_in_use."
			if why, never := op.Metadata[answerNeverKept]; never {
				desc = "Optional, at most " + strconv.Itoa(idempotency.MaxKeyLength) + " bytes. This route keeps no answer for a repeat, because " +
					fmt.Sprint(why) + ": a repeat with the same key does the work again. " +
					"A repeat while the first request is still running is a 409 idempotency_key_in_use."
			}
			op.Parameters = append(op.Parameters, &huma.Param{
				Name:        idempotencyKeyHeader,
				In:          "header",
				Description: desc,
				Schema:      &huma.Schema{Type: "string", MaxLength: new(idempotency.MaxKeyLength)},
			})
		}
		for _, status := range []int{http.StatusConflict, http.StatusUnprocessableEntity} {
			if code := strconv.Itoa(status); op.Responses[code] == nil {
				op.Responses[code] = &huma.Response{Description: http.StatusText(status), Content: problem}
			}
		}
	}
}

// idempotent is the one place that handles the Idempotency-Key of a write. It
// runs on every operation of the signed-in group, after the caller is known,
// so no route handles the key itself.
//
// With no key, or on a read, the request goes on as it is. With a key, the
// first request runs, and its answer is kept if it is a 2xx. A repeat gets the
// kept answer, and no route runs. The key belongs to the caller's session.
func (s *Server) idempotent() func(huma.Context, func(huma.Context)) {
	return func(ctx huma.Context, next func(huma.Context)) {
		key := ctx.Header(idempotencyKeyHeader)
		caller, signedIn := CallerFrom(ctx.Context())
		if key == "" || !signedIn || !takesIdempotencyKey(ctx.Method()) {
			next(ctx)
			return
		}
		if len(key) > idempotency.MaxKeyLength {
			detail := fmt.Sprintf("Use an %s of at most %d bytes.", idempotencyKeyHeader, idempotency.MaxKeyLength)
			writeProblemOn(ctx, Error(http.StatusUnprocessableEntity, "invalid_input", detail,
				&huma.ErrorDetail{Location: "header." + idempotencyKeyHeader, Message: "is too long"}))
			return
		}
		// A route that declares no body never reads one, and huma gives it no
		// limit and no deadline for one. So its body is not read here either:
		// it is no part of the request, and neither is its Content-Type.
		var body []byte
		var contentType string
		if ctx.Operation().RequestBody != nil {
			var ok bool
			if body, ok = bufferBody(ctx); !ok {
				// The route refuses a body that is too large, or that can't be
				// read, as it does with no key. Such a request keeps nothing.
				next(ctx)
				return
			}
			contentType = ctx.Header("Content-Type")
		}
		u := ctx.URL()
		request := requestHash(ctx.Method(), u.Path, u.RawQuery, contentType, body)

		run, answer, err := s.opts.Idempotency.Begin(ctx.Context(),
			idempotency.Session{ID: caller.SessionID, TokenHash: caller.tokenHash}, key, request)
		switch {
		case errors.Is(err, idempotency.ErrInUse):
			writeProblemOn(ctx, Error(http.StatusConflict, "idempotency_key_in_use",
				"The first request with this "+idempotencyKeyHeader+" is still running. Send it again in a moment."))
			return
		case errors.Is(err, idempotency.ErrReused):
			writeProblemOn(ctx, Error(http.StatusUnprocessableEntity, "idempotency_key_reused",
				"This "+idempotencyKeyHeader+" was used for another request. Use a new key for a new request."))
			return
		case err != nil:
			s.opts.Logger.ErrorContext(ctx.Context(), "find the idempotency key failed", slog.Any("error", err))
			writeProblemOn(ctx, Error(http.StatusInternalServerError, "internal_error", "Something went wrong on our side. Try again."))
			return
		case answer != nil:
			for name, values := range answer.Header {
				for _, v := range values {
					ctx.AppendHeader(name, v)
				}
			}
			ctx.SetStatus(answer.Status)
			_, _ = ctx.BodyWriter().Write(answer.Body)
			return
		}
		defer run.End()

		rec := &recordedAnswer{humaContext: ctx}
		next(rec)

		if _, never := ctx.Operation().Metadata[answerNeverKept]; never || rec.status < 200 || rec.status > 299 {
			return
		}
		// The work is done, so the answer is kept even if the client has gone:
		// a client that lost the answer is the one that sends the repeat.
		if err := run.Keep(context.WithoutCancel(ctx.Context()), rec.answer()); err != nil {
			s.opts.Logger.ErrorContext(ctx.Context(), "keep the answer of an idempotency key failed", slog.Any("error", err))
		}
	}
}

// recordedAnswer is a huma.Context that copies the answer as the route writes
// it to the client.
type recordedAnswer struct {
	humaContext
	status int
	body   bytes.Buffer
}

// humaContext is huma.Context under a name that an embedding struct can use:
// a field named Context would hide the method of that name.
type humaContext huma.Context

// SetStatus keeps the first status. huma sets a second one only when the
// client went away while the body was written.
func (r *recordedAnswer) SetStatus(code int) {
	if r.status == 0 {
		r.status = code
	}
	r.humaContext.SetStatus(code)
}

// BodyWriter writes to the copy first, so the copy is whole even when the
// client has gone.
func (r *recordedAnswer) BodyWriter() io.Writer {
	return io.MultiWriter(&r.body, r.humaContext.BodyWriter())
}

// Unwrap lets humachi.Unwrap find the request under this context.
func (r *recordedAnswer) Unwrap() huma.Context { return r.humaContext }

// answer is the recorded answer, with the headers that are kept.
func (r *recordedAnswer) answer() idempotency.Answer {
	_, w := humachi.Unwrap(r.humaContext)
	header := http.Header{}
	for _, name := range keptHeaders {
		if values := w.Header().Values(name); len(values) > 0 {
			header[name] = slices.Clone(values)
		}
	}
	return idempotency.Answer{Status: r.status, Header: header, Body: r.body.Bytes()}
}

// bufferBody reads the body of a request before its route does, so that the
// body can be hashed, and puts it back for the route. It reads no more than
// the route does (the operation's MaxBodyBytes), for no longer (its
// BodyReadTimeout). huma sets both only on an operation that declares a body,
// so call this for no other: with neither, the read would have no end. ok is
// false for a body at the limit, and for one that can't be read: the route
// then meets the same bytes or the same error, and answers as it does with no
// key.
func bufferBody(ctx huma.Context) (body []byte, ok bool) {
	r, _ := humachi.Unwrap(ctx)
	if r.Body == nil || r.Body == http.NoBody {
		return nil, true
	}
	op := ctx.Operation()
	if op.BodyReadTimeout > 0 {
		_ = ctx.SetReadDeadline(time.Now().Add(op.BodyReadTimeout))
		defer func() { _ = ctx.SetReadDeadline(time.Time{}) }()
	}
	rest := r.Body
	var reader io.Reader = rest
	if op.MaxBodyBytes > 0 {
		reader = io.LimitReader(rest, op.MaxBodyBytes)
	}
	body, err := io.ReadAll(reader)
	var after io.Reader = rest
	if err != nil {
		after = failedReader{err}
	}
	r.Body = struct {
		io.Reader
		io.Closer
	}{io.MultiReader(bytes.NewReader(body), after), rest}
	return body, err == nil && (op.MaxBodyBytes <= 0 || int64(len(body)) < op.MaxBodyBytes)
}

// failedReader gives the error that stopped the read of a body.
type failedReader struct{ err error }

func (f failedReader) Read([]byte) (int, error) { return 0, f.err }

// requestHash is what makes two requests the same request: the method, the
// path with its query, and the body. Only the hash is kept, so an upload is
// never stored. The path is the one after `me` and `current` are resolved. A
// route that declares no body has none here, and no type of one.
func requestHash(method, path, query, contentType string, body []byte) string {
	h := sha256.New()
	for _, part := range []string{method, path, query} {
		writePart(h, []byte(part))
	}
	hashBody(h, contentType, body)
	return hex.EncodeToString(h.Sum(nil))
}

// writePart writes one part of a hash with its length, so two lists of parts
// hash the same only when each part is the same.
func writePart(h hash.Hash, part []byte) {
	_, _ = h.Write([]byte(strconv.Itoa(len(part)) + ":"))
	_, _ = h.Write(part)
}

// hashBody adds a body to a hash, by what the body says and not by its bytes.
// A client can build the body again for a repeat, so the same JSON with its
// keys in another order, and the same form with another boundary, are the same
// body. Any other body, and one that can't be parsed, is its bytes.
func hashBody(h hash.Hash, contentType string, body []byte) {
	mediaType, params, _ := mime.ParseMediaType(contentType)
	switch {
	case mediaType == "application/json" || strings.HasSuffix(mediaType, "+json"):
		if canonical, ok := canonicalJSON(body); ok {
			writePart(h, []byte("json"))
			writePart(h, canonical)
			return
		}
	case mediaType == "multipart/form-data" && params["boundary"] != "":
		if sum, ok := formHash(body, params["boundary"]); ok {
			writePart(h, []byte("form"))
			writePart(h, sum)
			return
		}
	}
	writePart(h, []byte(mediaType))
	writePart(h, body)
}

// canonicalJSON writes a JSON body again with the keys of each object sorted
// and no spaces. ok is false for a body that is not one JSON value.
func canonicalJSON(body []byte) (canonical []byte, ok bool) {
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, false
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, false
	}
	canonical, err := json.Marshal(v)
	return canonical, err == nil
}

// formHash hashes the parts of a multipart form in their order: the name, the
// file name, the type and the content of each. ok is false for a form that
// can't be read.
func formHash(body []byte, boundary string) (sum []byte, ok bool) {
	h := sha256.New()
	r := multipart.NewReader(bytes.NewReader(body), boundary)
	for {
		part, err := r.NextRawPart()
		if errors.Is(err, io.EOF) {
			return h.Sum(nil), true
		}
		if err != nil {
			return nil, false
		}
		content := sha256.New()
		if _, err := io.Copy(content, part); err != nil {
			return nil, false
		}
		for _, field := range []string{part.FormName(), part.FileName(), part.Header.Get("Content-Type"), part.Header.Get("Content-Transfer-Encoding")} {
			writePart(h, []byte(field))
		}
		writePart(h, content.Sum(nil))
	}
}
