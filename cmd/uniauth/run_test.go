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
)

type failedWriter struct{}

func (failedWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func TestCommandSelectionAndOutputErrors(t *testing.T) {
	for _, args := range [][]string{{"bad"}, {"serve", "extra"}, {"hash-password"}} {
		if err := run(context.Background(), args, io.Discard, io.Discard, func(string) string { return "" }); err == nil {
			t.Fatalf("invalid args accepted: %v", args)
		}
	}
	for _, help := range []string{"help", "-h", "--help"} {
		var out bytes.Buffer
		if err := run(context.Background(), []string{help}, &out, io.Discard, func(string) string { return "" }); err != nil || !strings.Contains(out.String(), "UNIAUTH_ADMIN_PASSWORD") || strings.Contains(out.String(), "hash-password") {
			t.Fatalf("help: %v %s", err, out.String())
		}
	}
	if err := run(context.Background(), []string{"help"}, failedWriter{}, io.Discard, func(string) string { return "" }); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("help write failure: %v", err)
	}
	if err := Run(context.Background(), []string{"help"}, io.Discard, io.Discard); err != nil {
		t.Fatal(err)
	}
}

func TestServeConfigurationStartupAndCancellation(t *testing.T) {
	getenv := func(string) string { return "" }
	if err := run(context.Background(), nil, io.Discard, io.Discard, getenv); err == nil {
		t.Fatal("missing config accepted")
	}
	values := map[string]string{"UNIAUTH_PUBLIC_URL": "http://localhost:8080", "UNIAUTH_ALLOW_LOOPBACK_HTTP": "true", "UNIAUTH_ADMIN_PASSWORD": "  $直接-password  ", "UNIAUTH_LISTEN_ADDR": "127.0.0.1:0", "UNIAUTH_DATABASE_PATH": filepath.Join(t.TempDir(), "apps.db")}
	getenv = func(key string) string { return values[key] }
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := run(ctx, []string{"serve"}, io.Discard, io.Discard, getenv); err == nil {
		t.Fatal("canceled startup accepted")
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	values["UNIAUTH_LISTEN_ADDR"] = listener.Addr().String()
	if err := run(context.Background(), []string{"serve"}, io.Discard, io.Discard, getenv); err == nil {
		t.Fatal("occupied listener accepted")
	}
	values["UNIAUTH_ADMIN_PASSWORD"] = strings.Repeat("x", 1025)
	if err := run(context.Background(), nil, io.Discard, io.Discard, getenv); err == nil {
		t.Fatal("invalid administrator password accepted")
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
	values := map[string]string{"UNIAUTH_PUBLIC_URL": "http://localhost:8080", "UNIAUTH_ALLOW_LOOPBACK_HTTP": "true", "UNIAUTH_ADMIN_PASSWORD": "  $直接-password  ", "UNIAUTH_LISTEN_ADDR": "127.0.0.1:0", "UNIAUTH_DATABASE_PATH": filepath.Join(t.TempDir(), "apps.db")}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	output := &cancelOnStart{cancel: cancel}
	if err := run(ctx, nil, io.Discard, output, func(name string) string { return values[name] }); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.log.String(), "HTTP service started") {
		t.Fatal("service never started")
	}
}
