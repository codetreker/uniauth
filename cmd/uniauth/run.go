package main

import (
	"context"
	"errors"
	"fmt"
	"io"
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
		_, err := fmt.Fprintln(out, "Usage: uniauth [serve|help]\n\nserve  Start the authentication service (default).\n\nServe requires UNIAUTH_PUBLIC_URL, UNIAUTH_INITIAL_ADMIN_EMAIL, and at least one complete OAuth provider credential pair.")
		return err
	case "serve":
		cfg, err := server.LoadConfig(getenv)
		if err != nil {
			return err
		}
		return serve(ctx, cfg, errOut)
	default:
		return errors.New("unknown command; usage: uniauth [serve|help]")
	}
}

func serve(ctx context.Context, cfg server.Config, stderr io.Writer) (result error) {
	// A failed bind must not migrate data or replace a running Console credential.
	listener, err := net.Listen("tcp", cfg.ListenAddr)
	if err != nil {
		return fmt.Errorf("listen for HTTP requests: %w", err)
	}
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	output, err := newLogOutput(cfg.LogFile, stderr, cancel)
	if err != nil {
		return errors.Join(fmt.Errorf("initialize logging: %w", err), listener.Close())
	}
	defer func() { result = errors.Join(result, output.Close()) }()
	logger := output.Logger()
	service, err := server.New(ctx, cfg, nil, logger)
	if err != nil {
		return errors.Join(fmt.Errorf("initialize service: %w", err), listener.Close())
	}
	logger.InfoContext(ctx, "HTTP service started", "component", "server")
	return service.Serve(ctx, listener)
}
