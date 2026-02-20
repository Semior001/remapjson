package rest

import (
	"log/slog"
	"net/http"
	"runtime/debug"

	"github.com/cappuccinotm/slogx/slogm"
	"github.com/google/uuid"
)

// Recoverer is a middleware that recovers from panics in the request handling
// chain and returns a 500 Internal Server Error response.
func Recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		defer func() {
			if rvr := recover(); rvr != nil {
				attrs := []any{
					slog.String("url", r.URL.String()),
					slog.String("remote_addr", r.RemoteAddr),
					slog.Any("panic", rvr),
				}
				if rvr != http.ErrAbortHandler {
					attrs = append(attrs, slog.String("stack", string(debug.Stack())))
				}

				slog.ErrorContext(ctx, "request panic", attrs...)
				http.Error(w, http.StatusText(http.StatusInternalServerError),
					http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// AssignRequestID is a middleware that assigns a unique request ID to each
// incoming HTTP request.
func AssignRequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()

		reqID := r.Header.Get("X-Request-ID")
		if reqID == "" {
			reqID = uuid.NewString()
			r.Header.Set("X-Request-ID", reqID)
		}

		ctx = slogm.ContextWithRequestID(ctx, reqID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
