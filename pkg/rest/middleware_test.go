package rest

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRecoverer(t *testing.T) {
	t.Run("passes through on normal request", func(t *testing.T) {
		handler := Recoverer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))

		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

		assert.Equal(t, http.StatusOK, rec.Code)
	})

	t.Run("recovers from panic and returns 500", func(t *testing.T) {
		handler := Recoverer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			panic("something went wrong")
		}))

		rec := httptest.NewRecorder()
		require.NotPanics(t, func() { handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil)) })
		assert.Equal(t, http.StatusInternalServerError, rec.Code)
	})

	t.Run("recovers from ErrAbortHandler and returns 500", func(t *testing.T) {
		handler := Recoverer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			panic(http.ErrAbortHandler)
		}))

		rec := httptest.NewRecorder()
		require.NotPanics(t, func() { handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil)) })
		assert.Equal(t, http.StatusInternalServerError, rec.Code)
	})
}

func TestAssignRequestID(t *testing.T) {
	t.Run("generates new UUID when header is absent", func(t *testing.T) {
		var capturedID string
		handler := AssignRequestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			capturedID = r.Header.Get("X-Request-ID")
		}))

		handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
		assert.NotEmpty(t, capturedID)
	})

	t.Run("preserves existing X-Request-ID header", func(t *testing.T) {
		const existingID = "my-existing-id"
		var capturedID string
		handler := AssignRequestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			capturedID = r.Header.Get("X-Request-ID")
		}))

		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("X-Request-ID", existingID)
		handler.ServeHTTP(httptest.NewRecorder(), req)

		assert.Equal(t, existingID, capturedID)
	})

	t.Run("generates unique IDs for separate requests", func(t *testing.T) {
		seen := make(map[string]bool)
		for range 5 {
			var id string
			handler := AssignRequestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				id = r.Header.Get("X-Request-ID")
			}))
			handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
			assert.False(t, seen[id], "duplicate request ID: %s", id)
			seen[id] = true
		}
	})
}
