package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexedwards/argon2id"
)

func TestPasswordHashCommand(t *testing.T) {
	var output, diagnostics bytes.Buffer
	if err := run(context.Background(), []string{"hash-password"}, strings.NewReader("a private password\n"), &output, &diagnostics, func(string) string { return "" }); err != nil {
		t.Fatal(err)
	}
	hash := strings.TrimSpace(output.String())
	match, err := argon2id.ComparePasswordAndHash("a private password", hash)
	if err != nil || !match {
		t.Fatalf("incorrect password hash: %v", err)
	}
	if diagnostics.Len() != 0 || strings.Contains(hash, "private") {
		t.Fatal("plaintext leaked")
	}
}

type failedReader struct{}

func (failedReader) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }

type failedWriter struct{}

func (failedWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func TestPasswordInputContract(t *testing.T) {
	for _, password := range []string{"", "\n", "two\nlines", strings.Repeat("p", 1025)} {
		if _, err := readPassword(strings.NewReader(password), io.Discard); err == nil {
			t.Fatal("invalid password accepted")
		}
	}
	if _, err := readPassword(failedReader{}, io.Discard); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("reader cause lost: %v", err)
	}
	password, err := readPassword(strings.NewReader("  secret  \r\n"), io.Discard)
	if err != nil || password != "  secret  " {
		t.Fatalf("password whitespace modified: %q %v", password, err)
	}
}

func TestCommandSelectionAndOutputErrors(t *testing.T) {
	for _, args := range [][]string{{"bad"}, {"serve", "extra"}, {"hash-password", "plaintext"}} {
		if err := run(context.Background(), args, strings.NewReader(""), io.Discard, io.Discard, func(string) string { return "" }); err == nil {
			t.Fatalf("invalid args accepted: %v", args)
		}
	}
	for _, help := range []string{"help", "-h", "--help"} {
		var out bytes.Buffer
		if err := run(context.Background(), []string{help}, nil, &out, io.Discard, func(string) string { return "" }); err != nil || !strings.Contains(out.String(), "UNIAUTH_PUBLIC_URL") {
			t.Fatalf("help: %v %s", err, out.String())
		}
	}
	if err := run(context.Background(), []string{"help"}, nil, failedWriter{}, io.Discard, func(string) string { return "" }); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("help write failure: %v", err)
	}
	if err := Run(context.Background(), []string{"help"}, nil, io.Discard, io.Discard); err != nil {
		t.Fatal(err)
	}
	if err := run(context.Background(), []string{"hash-password"}, strings.NewReader("password"), failedWriter{}, io.Discard, func(string) string { return "" }); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("hash write failure: %v", err)
	}
}

func TestServeConfigurationStartupAndCancellation(t *testing.T) {
	getenv := func(string) string { return "" }
	if err := run(context.Background(), nil, nil, io.Discard, io.Discard, getenv); err == nil {
		t.Fatal("missing config accepted")
	}
	hash, err := argon2id.CreateHash("password", &argon2id.Params{Memory: 19456, Iterations: 1, Parallelism: 1, SaltLength: 16, KeyLength: 32})
	if err != nil {
		t.Fatal(err)
	}
	values := map[string]string{"UNIAUTH_PUBLIC_URL": "http://localhost:8080", "UNIAUTH_ALLOW_LOOPBACK_HTTP": "true", "UNIAUTH_ADMIN_PASSWORD_HASH": hash, "UNIAUTH_LISTEN_ADDR": "127.0.0.1:0", "UNIAUTH_DATABASE_PATH": filepath.Join(t.TempDir(), "apps.db")}
	getenv = func(key string) string { return values[key] }
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := run(ctx, []string{"serve"}, nil, io.Discard, io.Discard, getenv); err == nil {
		t.Fatal("canceled startup accepted")
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	values["UNIAUTH_LISTEN_ADDR"] = listener.Addr().String()
	if err := run(context.Background(), []string{"serve"}, nil, io.Discard, io.Discard, getenv); err == nil {
		t.Fatal("occupied listener accepted")
	}
	values["UNIAUTH_ADMIN_PASSWORD_HASH"] = "bad"
	if err := run(context.Background(), nil, nil, io.Discard, io.Discard, getenv); err == nil {
		t.Fatal("invalid hash accepted")
	}
}

type cancelOnStart struct {
	cancel context.CancelFunc
	log    bytes.Buffer
}

func (w *cancelOnStart) Write(body []byte) (int, error) {
	n, err := w.log.Write(body)
	if bytes.Contains(body, []byte("HTTP service started")) {
		w.cancel()
	}
	return n, err
}

func TestServeSuccessAndSignalShutdown(t *testing.T) {
	hash, err := argon2id.CreateHash("password", &argon2id.Params{Memory: 19456, Iterations: 1, Parallelism: 1, SaltLength: 16, KeyLength: 32})
	if err != nil {
		t.Fatal(err)
	}
	values := map[string]string{"UNIAUTH_PUBLIC_URL": "http://localhost:8080", "UNIAUTH_ALLOW_LOOPBACK_HTTP": "true", "UNIAUTH_ADMIN_PASSWORD_HASH": hash, "UNIAUTH_LISTEN_ADDR": "127.0.0.1:0", "UNIAUTH_DATABASE_PATH": filepath.Join(t.TempDir(), "apps.db")}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	output := &cancelOnStart{cancel: cancel}
	if err := run(ctx, nil, nil, io.Discard, output, func(name string) string { return values[name] }); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.log.String(), "HTTP service started") {
		t.Fatal("service never started")
	}
}
