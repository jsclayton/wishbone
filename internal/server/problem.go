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
	Code   string `json:"code" example:"item_fully_claimed" doc:"What went wrong, for a client to act on. A snake_case string that does not change within an API version."`
	Detail string `json:"detail" example:"Someone else just claimed the last one." doc:"A sentence in plain English. Its words can change."`
	// Errors lists the problems with each field of the input.
	Errors []*huma.ErrorDetail `json:"errors,omitempty" doc:"The problems with each field of the input."`
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
// A 500 is always internal_error with internalErrorDetail and no errors: the
// caller can't act on the server's own words, and they can name tables,
// columns and the steps of the server's work. Its cause goes to the log (see
// logInternalError), not to the answer.
func Error(status int, code, detail string, errs ...error) *Problem {
	if status == http.StatusInternalServerError {
		return &Problem{Status: status, Title: http.StatusText(status), Code: "internal_error", Detail: internalErrorDetail}
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
		if status == http.StatusInternalServerError {
			// huma.Error500InternalServerError, made with no request at hand.
			logInternalError(nil, msg, errs)
		}
		return Error(status, defaultCode(status), msg, errs...)
	}
	// huma calls this for a handler's error that is not a StatusError, with
	// that error in errs, and for the errors it writes itself in a request. A
	// 500's cause is logged here, with the request, because Error leaves it
	// out of the answer.
	huma.NewErrorWithContext = func(ctx huma.Context, status int, msg string, errs ...error) huma.StatusError {
		if status == http.StatusInternalServerError {
			logInternalError(ctx, msg, errs)
			return Error(status, "internal_error", msg)
		}
		return huma.NewError(status, msg, errs...)
	}
}

type loggerKey struct{}

// withLogger puts the server's logger in each request's context, for the
// errors that huma makes outside any one server (see logInternalError).
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

// logInternalError logs the cause of a 500 that huma makes, with the
// operation's method and path template. The template has no ids in it.
func logInternalError(ctx huma.Context, msg string, errs []error) {
	attrs := []any{slog.String("detail", msg)}
	if err := errors.Join(errs...); err != nil {
		attrs = append(attrs, slog.Any("error", err))
	}
	if ctx == nil {
		loggerFrom(context.Background()).Error("internal error", attrs...)
		return
	}
	attrs = append(attrs, slog.String("method", ctx.Method()))
	if op := ctx.Operation(); op != nil {
		attrs = append(attrs, slog.String("path", op.Path))
	}
	rctx := ctx.Context()
	attrs = append(attrs, slog.String("request_id", middleware.GetReqID(rctx)))
	loggerFrom(rctx).ErrorContext(rctx, "internal error", attrs...)
}

// writeProblem answers outside huma, for a path no operation matches.
func writeProblem(w http.ResponseWriter, status int, code, detail string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(Error(status, code, detail))
}
