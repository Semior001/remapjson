package rest

import (
	"bytes"
	"context"
	"crypto/sha256"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"text/template"
	"time"

	"github.com/cappuccinotm/slogx"
	slogxl "github.com/cappuccinotm/slogx/logger"
	"github.com/didip/tollbooth/v8"
	R "github.com/go-pkgz/rest"
	"github.com/go-pkgz/routegroup"
)

//go:embed web/*
var webFS embed.FS

// Sealer defines methods to crypt and decrypt webhook configurations,
// allowing them to be safely included in URLs without exposing sensitive
// information or risking tampering.
type Sealer interface {
	Seal(urlStr, tmplStr string) (string, error)
	Unseal(token string) (urlStr, tmplStr string, err error)
}

// Server remaps the incoming JSON to the request, as specified by the
// configuration in the URL
type Server struct {
	Addr     string
	BaseURL  string // must be without trailing slash, e.g. http://localhost:8080
	Version  string
	Password string //nolint:gosec // intentional secret field

	Client *http.Client
	Debug  bool
	Sealer Sealer

	templates sync.Map // map[string]*template.Template - cache of parsed templates
}

// Run starts the server and listens for incoming requests.
// It blocks until the context is canceled.
func (s *Server) Run(ctx context.Context) (err error) {
	stripFS, err := fs.Sub(webFS, "web")
	if err != nil {
		return fmt.Errorf("strip web prefix from embedded FS: %w", err)
	}

	srv := &http.Server{
		Addr:              s.Addr,
		Handler:           s.routes(stripFS),
		ReadHeaderTimeout: 5 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       30 * time.Second,
	}

	go func() {
		<-ctx.Done()
		if srv != nil {
			// graceful shutdown with 10 second timeout
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if serr := srv.Shutdown(shutdownCtx); serr != nil {
				slog.Error("failed to gracefully shutdown http server", slogx.Error(serr))
				if cerr := srv.Close(); cerr != nil {
					slog.Error("failed to forcefully close http server", slogx.Error(cerr))
				}
			}
		}
	}()

	slog.Info("starting server",
		slog.String("addr", s.Addr),
		slog.String("base_url", s.BaseURL),
		slog.Bool("password", s.Password != ""))

	defer func() { slog.WarnContext(ctx, "server stopped", slogx.Error(err)) }()

	if err = srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("listen and serve: %w", err)
	}

	return nil
}

func (s *Server) routes(staticFS fs.FS) http.Handler {
	rtr := routegroup.New(http.NewServeMux())

	logger := slogxl.New()

	rtr.Use(
		AssignRequestID,
		R.RealIP,
		Recoverer,
		R.Throttle(1000),
		R.AppInfo("remapjson", "semior", s.Version),
		R.Ping,
		R.SizeLimit(1*1024*1024), // 1MB max body size
		tollbooth.HTTPMiddleware(tollbooth.NewLimiter(10, nil)), // 10 req/s global rate limit
		R.Maybe(logger.HTTPServerMiddleware, func(*http.Request) bool { return s.Debug }),
	)

	rtr.HandleFunc("/wh/{token}", s.handleWebhook)

	rtr.Group().Route(func(webapi *routegroup.Bundle) {
		webapi.Use(
			R.Maybe(R.BasicAuthWithPrompt("remapjson", s.Password), func(_ *http.Request) bool { return s.Password != "" }),
			logger.HTTPServerMiddleware,
		)

		webapi.Handle("GET /web/", http.StripPrefix("/web/", http.FileServer(http.FS(staticFS))))

		webapi.HandleFunc("POST /configure", s.handleConfigure)
		webapi.HandleFunc("POST /render", s.handleRender)
		webapi.HandleFunc("POST /unseal", s.handleUnseal)
	})

	return rtr
}

// POST /configure - encode the provided URL and template, effectively preparing
// the webhook URL for future requests.
// This endpoint can be used to pre-cache templates or validate them before use.
func (s *Server) handleConfigure(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if err := r.ParseForm(); err != nil {
		s.error(w, r, http.StatusBadRequest, "invalid form data: %v", err)
		return
	}
	urlStr, tmplStr := r.FormValue("url"), r.FormValue("template")

	if urlStr == "" || tmplStr == "" {
		s.error(w, r, http.StatusBadRequest, "missing URL or template")
		return
	}

	// precompile template
	if _, err := s.template(urlStr, tmplStr); err != nil {
		s.error(w, r, http.StatusBadRequest, "invalid template: %v", err)
		return
	}

	token, err := s.Sealer.Seal(urlStr, tmplStr)
	if err != nil {
		s.error(w, r, http.StatusInternalServerError, "failed to seal configuration: %v", err)
		return
	}
	webhookURL := s.BaseURL + "/wh/" + token

	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		escaped := html.EscapeString(webhookURL)
		//nolint:gosec // webhookURL is escaped with html.EscapeString above
		fmt.Fprintf(w, `<input type="text" readonly value="%s">`+
			`<button class="btn-copy" onclick="navigator.clipboard.writeText(this.previousElementSibling.value)">Copy</button>`,
			escaped)
		return
	}

	var resp struct {
		WebhookURL string `json:"webhook_url"`
	}
	resp.WebhookURL = webhookURL

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		slog.WarnContext(ctx, "failed to write response", slogx.Error(err))
		return
	}
}

// POST /render - renders a Go template with example JSON data and returns an HTML preview.
// Accepts application/x-www-form-urlencoded with fields: template, data.
func (s *Server) handleRender(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		//nolint:gosec // error message is escaped with html.EscapeString
		fmt.Fprintf(w, `<span class="error">invalid form: %s</span>`, html.EscapeString(err.Error()))
		return
	}

	tmplStr := r.FormValue("template")
	dataStr := r.FormValue("data")

	if tmplStr == "" {
		return
	}

	var data map[string]any
	if dataStr != "" {
		if err := json.Unmarshal([]byte(dataStr), &data); err != nil {
			//nolint:gosec // error message is escaped with html.EscapeString
			fmt.Fprintf(w, `<span class="error">example data: %s</span>`, html.EscapeString(err.Error()))
			return
		}
	}

	tmpl, err := template.New("").Parse(tmplStr)
	if err != nil {
		//nolint:gosec // error message is escaped with html.EscapeString
		fmt.Fprintf(w, `<span class="error">template: %s</span>`, html.EscapeString(err.Error()))
		return
	}

	buf := &bytes.Buffer{}
	if err = tmpl.Execute(buf, data); err != nil {
		//nolint:gosec // error message is escaped with html.EscapeString
		fmt.Fprintf(w, `<span class="error">render: %s</span>`, html.EscapeString(err.Error()))
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	//nolint:gosec // buf content is escaped with html.EscapeString
	fmt.Fprintf(w, `<pre>%s</pre>`, html.EscapeString(buf.String()))
}

// POST /unseal - decodes a token (or full webhook URL) and returns the target URL and template.
// Accepts application/x-www-form-urlencoded with field: token.
func (s *Server) handleUnseal(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		//nolint:gosec // error message is escaped with html.EscapeString
		fmt.Fprintf(w, `<span class="error">invalid form: %s</span>`, html.EscapeString(err.Error()))
		return
	}

	raw := r.FormValue("token")
	if raw == "" {
		return
	}

	// accept either a full webhook URL or just the bare token
	token := raw
	if idx := strings.LastIndex(raw, "/wh/"); idx != -1 {
		token = raw[idx+len("/wh/"):]
	}

	urlStr, tmplStr, err := s.Sealer.Unseal(token)
	if err != nil {
		//nolint:gosec // error message is escaped with html.EscapeString
		fmt.Fprintf(w, `<span class="error">%s</span>`, html.EscapeString(err.Error()))
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	//nolint:gosec // urlStr and tmplStr are escaped with html.EscapeString
	fmt.Fprintf(w,
		`<div class="field"><div class="section-label">Target URL</div>`+
			`<div class="preview-box"><pre>%s</pre></div></div>`+
			`<div class="field"><div class="section-label">Template</div>`+
			`<div class="preview-box"><pre>%s</pre></div></div>`,
		html.EscapeString(urlStr), html.EscapeString(tmplStr))
}

// ANY /wh/<base64url-encoded-aes-gcm-sealed-config>
// sends a request to the remote server, remapping the incoming JSON to
// the request, as specified by the sealed configuration token in the URL.
func (s *Server) handleWebhook(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	remoteURL, rawTmpl, err := s.Sealer.Unseal(r.PathValue("token"))
	if err != nil {
		s.error(w, r, http.StatusBadRequest, "invalid token: %v", err)
		return
	}

	//nolint:gosec // remoteURL and rawTmpl come from operator-sealed token, log injection is accepted
	slog.Info("handling request",
		slog.String("remote_url", remoteURL),
		slog.String("template", rawTmpl))

	body, err := io.ReadAll(r.Body)
	if err != nil {
		s.error(w, r, http.StatusBadRequest, "failed to read request body: %v", err)
		return
	}

	var data map[string]any
	if len(body) > 0 {
		if err = json.Unmarshal(body, &data); err != nil {
			s.error(w, r, http.StatusBadRequest, "invalid JSON: %v", err)
			return
		}
	}

	tmpl, err := s.template(remoteURL, rawTmpl)
	if err != nil {
		s.error(w, r, http.StatusBadRequest, "invalid template: %v", err)
		return
	}

	buf := &bytes.Buffer{}
	if err = tmpl.Execute(buf, data); err != nil {
		s.error(w, r, http.StatusInternalServerError, "failed to execute template: %v", err)
		return
	}

	req, err := http.NewRequestWithContext(ctx, r.Method, remoteURL, buf)
	if err != nil {
		s.error(w, r, http.StatusInternalServerError, "failed to create request: %v", err)
		return
	}

	//nolint:gosec // remoteURL comes from operator-sealed token, SSRF is accepted by design
	resp, err := s.Client.Do(req)
	if err != nil {
		s.error(w, r, http.StatusInternalServerError, "failed to send request: %v", err)
		return
	}
	defer resp.Body.Close()

	w.WriteHeader(resp.StatusCode)
	if _, err = io.Copy(w, resp.Body); err != nil {
		slog.WarnContext(ctx, "failed to copy response body", slogx.Error(err))
		return
	}
}

func (s *Server) template(url, tstr string) (*template.Template, error) {
	h := sha256.New()
	_, _ = h.Write([]byte(url))
	_, _ = h.Write([]byte(tstr))
	key := fmt.Sprintf("%x", h.Sum(nil))

	if tmpl, ok := s.templates.Load(key); ok {
		return tmpl.(*template.Template), nil
	}

	tmpl, err := template.New("").Parse(tstr)
	if err != nil {
		return nil, fmt.Errorf("parse template: %w", err)
	}

	s.templates.Store(key, tmpl)
	return tmpl, nil
}

func (s *Server) error(w http.ResponseWriter, r *http.Request, status int, format string, args ...any) {
	ctx := r.Context()
	err := fmt.Errorf(format, args...)

	slog.WarnContext(ctx, "request failed",
		slog.String("remote", r.RemoteAddr),
		slog.Int("status", status), slogx.Error(err))
	w.WriteHeader(status)
	if _, werr := fmt.Fprintf(w, `{"error": %q}`, err); werr != nil {
		slog.WarnContext(ctx, "failed to write error response", slogx.Error(werr))
	}
}
