package rest

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	neturl "net/url"

	"github.com/Semior001/remapjson/pkg/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func configureRequest(urlStr, tmplStr string) *http.Request {
	form := neturl.Values{"url": {urlStr}, "template": {tmplStr}}.Encode()
	req := httptest.NewRequest(http.MethodPost, "/configure", strings.NewReader(form))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return req
}

func TestHandleConfigure(t *testing.T) {
	t.Run("returns webhook URL for valid request", func(t *testing.T) {
		remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
		defer remote.Close()

		s := &Server{BaseURL: "http://localhost:8080", Version: "test", Sealer: config.Sealer{Secret: "test-secret"}, Client: remote.Client()}

		rec := httptest.NewRecorder()
		s.handleConfigure(rec, configureRequest(remote.URL, "{{.value}}"))

		require.Equal(t, http.StatusOK, rec.Code)
		var resp struct {
			WebhookURL string `json:"webhook_url"`
		}
		require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
		assert.Contains(t, resp.WebhookURL, "http://localhost:8080/wh/")
	})

	t.Run("missing URL returns 400", func(t *testing.T) {
		s := &Server{BaseURL: "http://localhost:8080", Version: "test", Sealer: config.Sealer{Secret: "test-secret"}, Client: &http.Client{}}

		rec := httptest.NewRecorder()
		s.handleConfigure(rec, configureRequest("", "{{.value}}"))

		assert.Equal(t, http.StatusBadRequest, rec.Code)
		assert.Contains(t, rec.Body.String(), `"error"`)
	})

	t.Run("missing template returns 400", func(t *testing.T) {
		s := &Server{BaseURL: "http://localhost:8080", Version: "test", Sealer: config.Sealer{Secret: "test-secret"}, Client: &http.Client{}}

		rec := httptest.NewRecorder()
		s.handleConfigure(rec, configureRequest("http://remote.example.com", ""))

		assert.Equal(t, http.StatusBadRequest, rec.Code)
		assert.Contains(t, rec.Body.String(), `"error"`)
	})

	t.Run("invalid template syntax returns 400", func(t *testing.T) {
		s := &Server{BaseURL: "http://localhost:8080", Version: "test", Sealer: config.Sealer{Secret: "test-secret"}, Client: &http.Client{}}

		rec := httptest.NewRecorder()
		s.handleConfigure(rec, configureRequest("http://remote.example.com", "{{invalid"))

		assert.Equal(t, http.StatusBadRequest, rec.Code)
		assert.Contains(t, rec.Body.String(), `"error"`)
	})
}

func webhookRequest(method, token string, body string) *http.Request {
	var bodyReader *strings.Reader
	if body != "" {
		bodyReader = strings.NewReader(body)
	} else {
		bodyReader = strings.NewReader("")
	}
	req := httptest.NewRequest(method, "/wh/"+token, bodyReader)
	req.SetPathValue("token", token)
	return req
}

func TestHandle(t *testing.T) {
	t.Run("invalid token returns 400", func(t *testing.T) {
		s := &Server{BaseURL: "http://localhost:8080", Version: "test", Sealer: config.Sealer{Secret: "test-secret"}, Client: &http.Client{}}

		req := httptest.NewRequest(http.MethodGet, "/wh/!!!notbase64!!!", nil)
		req.SetPathValue("token", "!!!notbase64!!!")
		rec := httptest.NewRecorder()
		s.handleWebhook(rec, req)

		assert.Equal(t, http.StatusBadRequest, rec.Code)
		assert.Contains(t, rec.Body.String(), "invalid token")
	})

	t.Run("token from wrong secret returns 400", func(t *testing.T) {
		s1 := &Server{BaseURL: "http://localhost:8080", Version: "test", Sealer: config.Sealer{Secret: "secret-a"}, Client: &http.Client{}}
		s2 := &Server{BaseURL: "http://localhost:8080", Version: "test", Sealer: config.Sealer{Secret: "secret-b"}, Client: &http.Client{}}

		token, err := s1.Sealer.Seal("http://remote.example.com", "{{.value}}")
		require.NoError(t, err)

		req := webhookRequest(http.MethodGet, token, `{"value":"hello"}`)
		rec := httptest.NewRecorder()
		s2.handleWebhook(rec, req)

		assert.Equal(t, http.StatusBadRequest, rec.Code)
		assert.Contains(t, rec.Body.String(), "invalid token")
	})

	t.Run("invalid JSON body returns 400", func(t *testing.T) {
		s := &Server{BaseURL: "http://localhost:8080", Version: "test", Sealer: config.Sealer{Secret: "test-secret"}, Client: &http.Client{}}

		token, err := s.Sealer.Seal("http://remote.example.com", "{{.value}}")
		require.NoError(t, err)

		req := webhookRequest(http.MethodGet, token, "not-json")
		rec := httptest.NewRecorder()
		s.handleWebhook(rec, req)

		assert.Equal(t, http.StatusBadRequest, rec.Code)
		assert.Contains(t, rec.Body.String(), "invalid JSON")
	})

	t.Run("forwards transformed body and proxies remote response", func(t *testing.T) {
		var capturedBody string
		remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			b, err := io.ReadAll(r.Body)
			require.NoError(t, err)
			capturedBody = string(b)
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"ok":true}`))
		}))
		defer remote.Close()

		s := &Server{BaseURL: "http://localhost:8080", Version: "test", Sealer: config.Sealer{Secret: "test-secret"}, Client: remote.Client()}

		token, err := s.Sealer.Seal(remote.URL, `{"mapped":"{{.value}}"}`)
		require.NoError(t, err)

		req := webhookRequest(http.MethodPost, token, `{"value":"hello"}`)
		rec := httptest.NewRecorder()
		s.handleWebhook(rec, req)

		assert.Equal(t, http.StatusCreated, rec.Code)
		assert.Equal(t, `{"ok":true}`, rec.Body.String())
		assert.Equal(t, `{"mapped":"hello"}`, capturedBody)
	})

	t.Run("empty body is forwarded with nil data", func(t *testing.T) {
		var capturedBody string
		remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			b, err := io.ReadAll(r.Body)
			require.NoError(t, err)
			capturedBody = string(b)
			w.WriteHeader(http.StatusOK)
		}))
		defer remote.Close()

		s := &Server{BaseURL: "http://localhost:8080", Version: "test", Sealer: config.Sealer{Secret: "test-secret"}, Client: remote.Client()}

		token, err := s.Sealer.Seal(remote.URL, `static-payload`)
		require.NoError(t, err)

		req := webhookRequest(http.MethodPost, token, "")
		rec := httptest.NewRecorder()
		s.handleWebhook(rec, req)

		assert.Equal(t, http.StatusOK, rec.Code)
		assert.Equal(t, "static-payload", capturedBody)
	})

	t.Run("remote call failure returns 500", func(t *testing.T) {
		remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
		remoteURL := remote.URL
		remote.Close() // close immediately so the connection is refused

		s := &Server{BaseURL: "http://localhost:8080", Version: "test", Sealer: config.Sealer{Secret: "test-secret"}, Client: &http.Client{}}

		token, err := s.Sealer.Seal(remoteURL, `{{.value}}`)
		require.NoError(t, err)

		req := webhookRequest(http.MethodPost, token, `{"value":"hello"}`)
		rec := httptest.NewRecorder()
		s.handleWebhook(rec, req)

		assert.Equal(t, http.StatusInternalServerError, rec.Code)
		assert.Contains(t, rec.Body.String(), "failed to send request")
	})
}

func unsealRequest(token string) *http.Request {
	form := neturl.Values{"token": {token}}.Encode()
	req := httptest.NewRequest(http.MethodPost, "/unseal", strings.NewReader(form))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return req
}

func TestHandleUnseal(t *testing.T) {
	s := &Server{BaseURL: "http://localhost:8080", Version: "test", Sealer: config.Sealer{Secret: "test-secret"}, Client: &http.Client{}}

	t.Run("bare token is unsealed", func(t *testing.T) {
		token, err := s.Sealer.Seal("https://example.com/hook", `{"msg":"{{.text}}"}`)
		require.NoError(t, err)

		rec := httptest.NewRecorder()
		s.handleUnseal(rec, unsealRequest(token))

		assert.Equal(t, http.StatusOK, rec.Code)
		assert.Contains(t, rec.Body.String(), "https://example.com/hook")
		assert.Contains(t, rec.Body.String(), `{&#34;msg&#34;:&#34;{{.text}}&#34;}`)
	})

	t.Run("full webhook URL is unsealed", func(t *testing.T) {
		token, err := s.Sealer.Seal("https://example.com/hook", `{{.value}}`)
		require.NoError(t, err)

		rec := httptest.NewRecorder()
		s.handleUnseal(rec, unsealRequest("http://localhost:8080/wh/"+token))

		assert.Equal(t, http.StatusOK, rec.Code)
		assert.Contains(t, rec.Body.String(), "https://example.com/hook")
		assert.Contains(t, rec.Body.String(), `{{.value}}`)
	})

	t.Run("invalid token returns error fragment", func(t *testing.T) {
		rec := httptest.NewRecorder()
		s.handleUnseal(rec, unsealRequest("notvalidbase64!!!"))

		assert.Equal(t, http.StatusOK, rec.Code)
		assert.Contains(t, rec.Body.String(), `class="error"`)
	})

	t.Run("empty token returns empty body", func(t *testing.T) {
		rec := httptest.NewRecorder()
		s.handleUnseal(rec, unsealRequest(""))

		assert.Equal(t, http.StatusOK, rec.Code)
		assert.Empty(t, rec.Body.String())
	})
}
