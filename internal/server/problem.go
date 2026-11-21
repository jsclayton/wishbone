package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5/middleware"

	"jsclayton.dev/wishbone/internal/imagestore"
)

// Problem is the body of every 4xx and 5xx answer under /api: RFC 9457
// problem+json with one more field, a snake_case `code` that does not change
// within an API version (docs/spec.md, API principles, Errors). It has no `type` URI,
// because a self-hosted instance has no fixed host for one.
type Problem struct {
	Status int    `json:"status" example:"409" doc:"HTTP status code"`
	Title  string `json:"title" example:"Conflict" doc:"The status text. It does not change between occurrences."`
	Code   string `json:"code" example:"item_fully_claimed" doc:"What went wrong, for a client to act on. A snake_case string that does not change within an API version. An operation's x-error-codes lists the codes that it can answer with, by status. The server can add a code within an API version: show detail for one you don't know."`
	Detail string `json:"detail" example:"Someone else just claimed the last one." doc:"A sentence in plain English. Its words can change."`
	// Errors lists the problems with each field of the input.
	Errors []*huma.ErrorDetail `json:"errors,omitempty" doc:"The problems with each field of the input."`

	// msg and cause are what a 5xx was made with. They go to the log (see
	// logServerError), never to the answer.
	msg   string
	cause error
}

// Error returns the problem's detail, so a Problem is a Go error.
func (p *Problem) Error() string { return p.Detail }

// GetStatus makes a Problem a huma.StatusError: huma sets the response status
// from it.
func (p *Problem) GetStatus() int { return p.Status }

// ContentType answers with problem+json in place of JSON.
func (p *Problem) ContentType(ct string) string {
	if ct == "application/json" {
		return "application/problem+json"
	}
	return ct
}

// internalErrorDetail is the detail of every 500 (docs/routes.md, "Codes on
// every route").
const internalErrorDetail = "Something went wrong on our side. Try again."

// Error returns a Problem with its own code. A handler returns it when the
// code is more specific than the one its status gets by default.
//
// A 5xx has no errors: the caller can't act on the server's own words, and
// they can name tables, columns and the steps of the server's work. Its
// detail and errors go to the log (see logServerError), not to the answer. A
// 500 is always internal_error with internalErrorDetail. Another 5xx keeps
// its code and detail, so its detail must be a fixed sentence.
func Error(status int, code, detail string, errs ...error) *Problem {
	if status >= 500 {
		p := &Problem{Status: status, Title: http.StatusText(status), Code: code, Detail: detail, msg: detail, cause: errors.Join(errs...)}
		if status == http.StatusInternalServerError {
			p.Code, p.Detail = "internal_error", internalErrorDetail
		}
		return p
	}
	p := &Problem{Status: status, Title: http.StatusText(status), Code: code, Detail: detail}
	for _, err := range errs {
		if err == nil {
			continue
		}
		if d, ok := err.(huma.ErrorDetailer); ok {
			p.Errors = append(p.Errors, d.ErrorDetail())
			continue
		}
		p.Errors = append(p.Errors, &huma.ErrorDetail{Message: err.Error()})
	}
	return p
}

// defaultCodes are the codes of the statuses that docs/routes.md lists under
// "Codes on every route", for an error that names no code of its own.
var defaultCodes = map[int]string{
	http.StatusBadRequest:            "bad_request",
	http.StatusUnauthorized:          "unauthorized",
	http.StatusForbidden:             "forbidden",
	http.StatusNotFound:              "not_found",
	http.StatusMethodNotAllowed:      "method_not_allowed",
	http.StatusConflict:              "conflict",
	http.StatusGone:                  "gone",
	http.StatusRequestEntityTooLarge: "request_too_large",
	http.StatusPreconditionFailed:    "resource_changed",
	http.StatusUnprocessableEntity:   "invalid_input",
	http.StatusTooManyRequests:       "rate_limited",
	http.StatusInternalServerError:   "internal_error",
	http.StatusServiceUnavailable:    "service_unavailable",
}

func defaultCode(status int) string {
	if c, ok := defaultCodes[status]; ok {
		return c
	}
	if status >= 500 {
		return "internal_error"
	}
	return "bad_request"
}

func init() {
	// Every error that huma writes itself (input that fails validation, a
	// missing header) goes through here, so it gets a code too.
	huma.NewError = func(status int, msg string, errs ...error) huma.StatusError {
		if status == http.StatusRequestEntityTooLarge && (strings.HasSuffix(msg, fmt.Sprintf("limit=%d bytes", imageTooLargeLimit)) ||
			strings.HasSuffix(msg, fmt.Sprintf("limit=%d bytes", searchBodyLimit))) {
			// huma stopped reading an upload at the limit of POST /images or of
			// a search form, the only operations with those limits.
			// TestAnImageOverTheLimitIsA413 and TestAPhotoOverTheImageLimitIsA413
			// keep the match true.
			return Error(status, "image_too_large", fmt.Sprintf("Use an image smaller than %d MB.", imagestore.MaxBytes>>20))
		}
		return Error(status, defaultCode(status), msg, errs...)
	}
	// huma calls this for a handler's error that is not a StatusError, with
	// that error in errs, and for the errors it writes itself in a request.
	huma.NewErrorWithContext = func(_ huma.Context, status int, msg string, errs ...error) huma.StatusError {
		return huma.NewError(status, msg, errs...)
	}
}

type loggerKey struct{}

// withLogger puts the server's logger in each request's context, for the
// errors that huma makes outside any one server (see logServerError).
func withLogger(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), loggerKey{}, logger)))
		})
	}
}

func loggerFrom(ctx context.Context) *slog.Logger {
	if l, ok := ctx.Value(loggerKey{}).(*slog.Logger); ok {
		return l
	}
	return slog.Default()
}

// logServerErrors is a huma transformer. It logs each 5xx problem as huma
// writes it, with the request at hand. huma makes some of them, such as
// huma.Error500InternalServerError, before it has a request, so this is the
// one place their causes go to the log.
func logServerErrors(ctx huma.Context, _ string, v any) (any, error) {
	if p, ok := v.(*Problem); ok {
		logServerError(ctx, p)
	}
	return v, nil
}

// logServerError logs the detail and cause of a 5xx, with the operation's
// method and path template. The template has no ids in it.
//
// A request whose client went away logs at INFO, not ERROR: the server did
// nothing wrong, and nobody reads the answer. It stays at INFO, the default
// level, so that a run of them, which can mean the server is too slow, still
// shows. The line keeps the status, detail and cause.
func logServerError(ctx huma.Context, p *Problem) {
	if p.Status < 500 {
		return
	}
	rctx := ctx.Context()
	attrs := []any{slog.Int("status", p.Status), slog.String("detail", p.msg)}
	if p.cause != nil {
		attrs = append(attrs, slog.Any("error", p.cause))
	}
	attrs = append(attrs, slog.String("method", ctx.Method()))
	if op := ctx.Operation(); op != nil {
		attrs = append(attrs, slog.String("path", op.Path))
	}
	attrs = append(attrs, slog.String("request_id", middleware.GetReqID(rctx)))
	logger := loggerFrom(rctx)
	if clientWentAway(rctx) {
		logger.InfoContext(rctx, "client went away", attrs...)
		return
	}
	logger.ErrorContext(rctx, "internal error", attrs...)
}

// clientWentAway says whether a request's client left before its answer.
// net/http cancels a request's context when its client closes the
// connection, and when a read of the body fails, as it does for a body cut
// off. The server never cancels a request's context itself: its own
// deadlines end as context.DeadlineExceeded.
func clientWentAway(ctx context.Context) bool {
	return errors.Is(ctx.Err(), context.Canceled)
}

// writeProblem answers outside huma, for a path no operation matches.
func writeProblem(w http.ResponseWriter, status int, code, detail string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(Error(status, code, detail))
}
