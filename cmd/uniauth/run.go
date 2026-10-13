package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"

	"github.com/codetreker/uniauth/internal/server"
)

func Run(ctx context.Context, args []string, out, errOut io.Writer) error {
	return run(ctx, args, out, errOut, os.Getenv)
}

func run(ctx context.Context, args []string, out, errOut io.Writer, getenv func(string) string) error {
	command := "serve"
	if len(args) > 0 {
		command = args[0]
	}
	if len(args) > 1 {
		return errors.New("unexpected arguments; usage: uniauth [serve|help]")
	}
	switch command {
	case "help", "-h", "--help":
		_, err := fmt.Fprintln(out, "Usage: uniauth [serve|help]\n\nserve  Start the authentication service (default).\n\nServe requires UNIAUTH_PUBLIC_URL and UNIAUTH_ADMIN_PASSWORD.")
		return err
	case "serve":
		cfg, err := server.LoadConfig(getenv)
		if err != nil {
			return err
		}
		logger := slog.New(slog.NewJSONHandler(errOut, nil))
		service, err := server.New(ctx, cfg, nil, logger)
		if err != nil {
			return fmt.Errorf("initialize service: %w", err)
		}
		listener, err := net.Listen("tcp", cfg.ListenAddr)
		if err != nil {
			return errors.Join(fmt.Errorf("listen for HTTP requests: %w", err), service.Close())
		}
		logger.InfoContext(ctx, "HTTP service started", "component", "server")
		return service.Serve(ctx, listener)
	default:
		return errors.New("unknown command; usage: uniauth [serve|help]")
	}
}
