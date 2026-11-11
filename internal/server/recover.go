package server

import (
	"errors"
	"log/slog"
	"net/http"
	"runtime/debug"

	"github.com/go-chi/chi/v5/middleware"
)

// recoverer turns a panic in a handler into a 500 problem with a code. It
// replaces chi's, which answers in plain text. The panic and its stack go to
// the log, and the answer says nothing about it.
func recoverer(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
			defer func() {
				rvr := recover()
				if rvr == nil {
					return
				}
				// net/http uses this panic to cut a connection. Let it through.
				if err, ok := rvr.(error); ok && errors.Is(err, http.ErrAbortHandler) {
					panic(rvr)
				}
				logger.ErrorContext(r.Context(), "panic in handler",
					slog.Any("panic", rvr),
					slog.String("stack", string(debug.Stack())),
					slog.String("request_id", middleware.GetReqID(r.Context())),
				)
				if ww.Status() != 0 || ww.BytesWritten() > 0 {
					return // the answer has begun, and a second one would not parse
				}
				writeProblem(ww, http.StatusInternalServerError, "internal_error", "Something went wrong on our side. Try again.")
			}()
			next.ServeHTTP(ww, r)
		})
	}
}
