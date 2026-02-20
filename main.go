// Package main is an application entrypoint.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"runtime/debug"
	"strings"
	"syscall"
	"time"

	"github.com/Semior001/remapjson/cmd"
	"github.com/cappuccinotm/slogx"
	"github.com/cappuccinotm/slogx/slogm"
	"github.com/jessevdk/go-flags"
	"github.com/lmittmann/tint"
	"github.com/mattn/go-isatty"
)

var opts struct {
	Version cmd.Version `command:"version" description:"print application version and build date"`
	Server  cmd.Server  `command:"server" description:"run the server"`

	JSON  bool `long:"json"  env:"JSON"  description:"Enable JSON logging"`
	Debug bool `long:"debug" env:"DEBUG" description:"Enable debug mode"`
}

var version = "unknown"
var buildDate = "unknown"

func getVersion() string {
	// IDEA incorrectly detects version to be permanent "unknown",
	// while we inject it at build time

	//goland:noinspection GoBoolExpressions
	if bi, ok := debug.ReadBuildInfo(); ok && version == "unknown" {
		return bi.Main.Version
	}
	return version
}

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	go func() { // catch signal and invoke graceful termination
		stop := make(chan os.Signal, 1)
		signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
		sig := <-stop
		slog.Warn("caught signal", slog.Any("signal", sig))
		cancel()
	}()

	p := flags.NewParser(&opts, flags.HelpFlag|flags.PassDoubleDash)
	p.CommandHandler = func(command flags.Commander, args []string) error {
		setupLog(opts.Debug, opts.JSON)

		co := cmd.CommonOpts{
			Context:              ctx,
			ApplicationVersion:   getVersion(),
			ApplicationBuildDate: buildDate,
		}

		if c, ok := command.(interface{ SetCommonOpts(cmd.CommonOpts) }); ok {
			c.SetCommonOpts(co)
		}

		return command.Execute(args)
	}

	if _, err := p.Parse(); err != nil {
		if e, ok := errors.AsType[*flags.Error](err); ok && errors.Is(e.Type, flags.ErrHelp) {
			fmt.Println(err)
			os.Exit(0)
		}

		fmt.Printf("failed to execute command: %v\n", err)
		os.Exit(1)
	}
}

func setupLog(dbg, json bool) {
	defer slog.Info("prepared logger",
		slog.Bool("debug", dbg),
		slog.Bool("json", json))

	tintOpts := func(opts *slog.HandlerOptions, timeFormat string) *tint.Options {
		return &tint.Options{
			AddSource:   opts.AddSource,
			Level:       opts.Level,
			ReplaceAttr: opts.ReplaceAttr,
			TimeFormat:  timeFormat,
			NoColor:     !isatty.IsTerminal(os.Stderr.Fd()),
		}
	}

	middlewares := []slogx.Middleware{
		slogm.RequestID(),
		slogm.TrimAttrs(1024), // 1Kb
	}

	timeFormat := time.DateTime
	handlerOpts := &slog.HandlerOptions{Level: slog.LevelInfo}
	if dbg {
		timeFormat = time.RFC3339Nano
		handlerOpts.Level = slog.LevelDebug
		handlerOpts.AddSource = true
		handlerOpts.ReplaceAttr = func(_ []string, a slog.Attr) slog.Attr {
			if a.Key != slog.SourceKey {
				return a
			}
			// shorten source to just file:line and trim or extend to 15 characters
			src := a.Value.Any().(*slog.Source)
			file := src.File[strings.LastIndex(src.File, "/")+1:]

			s := fmt.Sprintf("%s:%d", file, src.Line)
			if json {
				return slog.String("s", s)
			}

			switch {
			case len(s) > 15:
				return slog.String("s", s[:15])
			case len(s) < 15:
				return slog.String("s", s+strings.Repeat(" ", 15-len(s)))
			default:
				return slog.String("s", s)
			}
		}

		middlewares = []slogx.Middleware{
			slogm.RequestID(),
			slogm.StacktraceOnError(),
			slogm.TrimAttrs(1024), // 1Kb
		}
	}

	var handler slog.Handler
	if json {
		handler = slog.NewJSONHandler(os.Stderr, handlerOpts)
	} else {
		handler = tint.NewHandler(os.Stderr, tintOpts(handlerOpts, timeFormat))
	}

	handler = slogx.NewChain(handler, middlewares...)

	slog.SetDefault(slog.New(handler))
}
