package cmd

import (
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/Semior001/remapjson/pkg/config"
	"github.com/Semior001/remapjson/pkg/rest"
	slogxl "github.com/cappuccinotm/slogx/logger"
)

// Server command starts the HTTP server.
type Server struct {
	Addr     string        `long:"addr"     env:"ADDR"     description:"address to listen on" default:":8080"`
	Timeout  time.Duration `long:"timeout"  env:"TIMEOUT"  description:"HTTP client timeout"  default:"90s"`
	BaseURL  string        `long:"base-url" env:"BASE_URL" description:"base URL for webhook" required:"true"`
	Secret   string        `long:"secret"   env:"SECRET"   description:"secret for sealing webhook configurations" required:"true"` //nolint:gosec // intentional secret field
	Password string        `long:"password" env:"PASSWORD" description:"password for basic auth, if not set, basic auth is disabled"`   //nolint:gosec // intentional secret field

	CommonOpts
}

// Execute runs the command
func (c Server) Execute([]string) error {
	ctx := c.Context

	debug := slog.Default().Enabled(ctx, slog.LevelDebug)

	srv := rest.Server{
		Addr:     c.Addr,
		BaseURL:  strings.TrimSuffix(c.BaseURL, "/"),
		Version:  c.ApplicationVersion,
		Password: c.Password,
		Sealer:   config.Sealer{Secret: c.Secret},
		Client:   &http.Client{Timeout: c.Timeout},
		Debug:    debug,
	}

	if debug {
		srv.Client.Transport = slogxl.New().HTTPClientRoundTripper(http.DefaultTransport)
	}

	if err := srv.Run(ctx); err != nil {
		return fmt.Errorf("run server: %w", err)
	}

	return nil
}
