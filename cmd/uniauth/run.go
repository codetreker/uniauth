package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"strings"

	"github.com/alexedwards/argon2id"
	"github.com/codetreker/uniauth/internal/server"
	"golang.org/x/term"
)

func Run(ctx context.Context, args []string, in io.Reader, out, errOut io.Writer) error {
	return run(ctx, args, in, out, errOut, os.Getenv)
}

func run(ctx context.Context, args []string, in io.Reader, out, errOut io.Writer, getenv func(string) string) error {
	command := "serve"
	if len(args) > 0 {
		command = args[0]
	}
	if len(args) > 1 {
		return errors.New("unexpected arguments; usage: uniauth [serve|hash-password|help]")
	}
	switch command {
	case "help", "-h", "--help":
		_, err := fmt.Fprintln(out, "Usage: uniauth [serve|hash-password|help]\n\nserve          Start the authentication service (default).\nhash-password  Read a password from stdin and print its Argon2id hash.\n\nServe requires UNIAUTH_PUBLIC_URL and UNIAUTH_ADMIN_PASSWORD_HASH.")
		return err
	case "hash-password":
		password, err := readPassword(in, errOut)
		if err != nil {
			return err
		}
		hash, err := argon2id.CreateHash(password, &argon2id.Params{Memory: 65536, Iterations: 3, Parallelism: 2, SaltLength: 16, KeyLength: 32})
		if err != nil {
			return fmt.Errorf("hash administrator password: %w", err)
		}
		_, err = fmt.Fprintln(out, hash)
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
		return errors.New("unknown command; usage: uniauth [serve|hash-password|help]")
	}
}

func readPassword(in io.Reader, prompt io.Writer) (string, error) {
	var raw []byte
	var err error
	if file, ok := in.(*os.File); ok && term.IsTerminal(int(file.Fd())) {
		if _, err = fmt.Fprint(prompt, "Administrator password: "); err != nil {
			return "", err
		}
		raw, err = term.ReadPassword(int(file.Fd()))
		if _, writeErr := fmt.Fprintln(prompt); writeErr != nil {
			err = errors.Join(err, writeErr)
		}
	} else {
		raw, err = io.ReadAll(io.LimitReader(in, 1027))
	}
	if err != nil {
		return "", fmt.Errorf("read administrator password: %w", err)
	}
	password := strings.TrimSuffix(strings.TrimSuffix(string(raw), "\n"), "\r")
	if len(password) == 0 || len(password) > 1024 || strings.ContainsAny(password, "\r\n") {
		return "", errors.New("administrator password must be one nonempty line of at most 1024 bytes")
	}
	return password, nil
}
