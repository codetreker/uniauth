package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestServePersistsStartupJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "service.log")
	values := serveEnvironment(filepath.Join(dir, "apps.db"), "127.0.0.1:0", "http://localhost:8080")
	values["UNIAUTH_LOG_FILE"] = path
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	stderr := &cancelOnStart{cancel: cancel}
	if err := run(ctx, nil, io.Discard, stderr, func(name string) string { return values[name] }); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(body, stderr.log.Bytes()) || !bytes.Contains(body, []byte("HTTP service started")) {
		t.Fatalf("startup log was not persisted alongside stderr: %v", err)
	}
}

func TestLoggingPreflightFailurePreservesDatabaseAndReleasesListener(t *testing.T) {
	for _, destination := range []string{"missing parent", "file parent", "directory", "read-only file"} {
		t.Run(destination, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "service.log")
			switch destination {
			case "missing parent":
				path = filepath.Join(dir, "missing", "service.log")
			case "directory":
				if err := os.Mkdir(path, 0700); err != nil {
					t.Fatal(err)
				}
			case "file parent":
				if err := os.WriteFile(path, nil, 0600); err != nil {
					t.Fatal(err)
				}
				path = filepath.Join(path, "child.log")
			case "read-only file":
				if os.Geteuid() == 0 {
					t.Skip("root bypasses file permission checks")
				}
				if err := os.WriteFile(path, nil, 0400); err != nil {
					t.Fatal(err)
				}
			}
			address := availableServeAddress(t)
			database := filepath.Join(dir, "apps.db")
			values := serveEnvironment(database, address, "http://localhost:8080")
			values["UNIAUTH_LOG_FILE"] = path
			err := run(t.Context(), nil, io.Discard, io.Discard, func(name string) string { return values[name] })
			var cause *os.PathError
			if err == nil || !strings.Contains(err.Error(), "initialize logging") {
				t.Fatalf("log preflight cause lost: %v", err)
			}
			if destination == "directory" {
				if !errors.Is(err, errLogNotRegular) {
					t.Fatalf("nonregular log destination accepted: %v", err)
				}
			} else if !errors.As(err, &cause) {
				t.Fatalf("log filesystem cause lost: %v", err)
			}
			if destination == "read-only file" && !errors.Is(err, os.ErrPermission) {
				t.Fatalf("file permission error lost: %v", err)
			}
			for _, suffix := range []string{"", "-wal", "-shm"} {
				if _, err := os.Stat(database + suffix); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("failed log preflight touched database %q: %v", suffix, err)
				}
			}
			assertServeAddressReleased(t, address)
		})
	}
}

func TestServeRuntimeRotationFailureIsReturnedAfterShutdown(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "service.log")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := errors.Join(file.Truncate(logFileLimit), file.Close()); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path+".3", 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path+".3", "blocker"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	address := availableServeAddress(t)
	values := serveEnvironment(filepath.Join(dir, "apps.db"), address, "http://localhost:8080")
	values["UNIAUTH_LOG_FILE"] = path
	var stderr bytes.Buffer
	err = run(t.Context(), nil, io.Discard, &stderr, func(name string) string { return values[name] })
	var cause *os.PathError
	if err == nil || !strings.Contains(err.Error(), "write log file") || !errors.As(err, &cause) || cause.Op != "remove" {
		t.Fatalf("runtime rotation failure was discarded: %v", err)
	}
	if !strings.Contains(stderr.String(), "HTTP service started") {
		t.Fatal("file failure suppressed stderr")
	}
	assertServeAddressReleased(t, address)
}

func availableServeAddress(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	return address
}

func assertServeAddressReleased(t *testing.T, address string) {
	t.Helper()
	listener, err := net.Listen("tcp", address)
	if err != nil {
		t.Fatalf("failed logging retained listener: %v", err)
	}
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
}
