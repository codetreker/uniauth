//go:build unix

package main

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestFIFOLogDestinationFailsStartupWithoutBlocking(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "service.log")
	if err := syscall.Mkfifo(path, 0600); err != nil {
		t.Fatal(err)
	}
	address := availableServeAddress(t)
	database := filepath.Join(dir, "apps.db")
	values := serveEnvironment(database, address, "http://localhost:8080")
	values["UNIAUTH_LOG_FILE"] = path
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	finished := make(chan error, 1)
	go func() {
		finished <- run(ctx, nil, io.Discard, io.Discard, func(name string) string { return values[name] })
	}()
	select {
	case err := <-finished:
		if !errors.Is(err, errLogNotRegular) {
			t.Fatalf("FIFO log destination accepted: %v", err)
		}
	case <-time.After(2 * time.Second):
		cancel()
		// A reader releases a regressed blocking writer before test cleanup.
		reader, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
		if err != nil {
			t.Fatal(err)
		}
		defer reader.Close()
		select {
		case <-finished:
		case <-time.After(time.Second):
			t.Fatal("FIFO startup retained a blocked writer")
		}
		t.Fatal("FIFO log destination blocked startup")
	}
	if _, err := os.Stat(database); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("FIFO rejection touched database: %v", err)
	}
	assertServeAddressReleased(t, address)
}
