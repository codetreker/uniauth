package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLogOutputAppendsJSONAcrossRestarts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "service.log")
	var stderr bytes.Buffer
	for _, message := range []string{"first start", "second start"} {
		ctx, cancel := context.WithCancelCause(t.Context())
		output, err := newLogOutput(path, &stderr, cancel)
		if err != nil {
			t.Fatal(err)
		}
		output.Logger().InfoContext(ctx, message, "component", "server")
		if err := output.Close(); err != nil {
			t.Fatal(err)
		}
		cancel(nil)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(body, stderr.Bytes()) {
		t.Fatal("file and stderr logs differ")
	}
	scanner := bufio.NewScanner(bytes.NewReader(body))
	var messages []string
	for scanner.Scan() {
		var entry struct {
			Message   string `json:"msg"`
			Component string `json:"component"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &entry); err != nil || entry.Component != "server" {
			t.Fatalf("invalid JSON log: %v", err)
		}
		messages = append(messages, entry.Message)
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if strings.Join(messages, ",") != "first start,second start" {
		t.Fatalf("restart replaced prior logs: %v", messages)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("new log permissions: %v %v", info, err)
	}
}

func TestLogRotationRetainsBoundedBackups(t *testing.T) {
	path := filepath.Join(t.TempDir(), "service.log")
	file, err := openLogFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for generation := range 5 {
		body := bytes.Repeat([]byte{byte('0' + generation)}, logFileLimit)
		if n, err := file.Write(body); err != nil || n != len(body) {
			t.Fatalf("write generation %d: %d %v", generation, n, err)
		}
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	for _, expected := range []struct {
		suffix string
		marker byte
	}{{"", '4'}, {".1", '3'}, {".2", '2'}, {".3", '1'}} {
		body, err := os.ReadFile(path + expected.suffix)
		if err != nil || len(body) != logFileLimit || body[0] != expected.marker || body[len(body)-1] != expected.marker {
			t.Fatalf("retained generation %s: bytes=%d error=%v", expected.suffix, len(body), err)
		}
		info, err := os.Stat(path + expected.suffix)
		if err != nil || info.Mode().Perm() != 0600 {
			t.Fatalf("rotation permissions %s: %v %v", expected.suffix, info, err)
		}
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil || len(entries) != 4 {
		t.Fatalf("retention exceeded active file and three backups: %d %v", len(entries), err)
	}
}

func TestLogOutputFailureCancelsAndKeepsStderr(t *testing.T) {
	for _, destination := range []string{"file", "stderr"} {
		t.Run(destination, func(t *testing.T) {
			ctx, cancel := context.WithCancelCause(t.Context())
			defer cancel(nil)
			var stderr bytes.Buffer
			var writer io.Writer = &stderr
			if destination == "stderr" {
				writer = failedWriter{}
			}
			output, err := newLogOutput(filepath.Join(t.TempDir(), "service.log"), writer, cancel)
			if err != nil {
				t.Fatal(err)
			}
			want := io.ErrClosedPipe
			if destination == "file" {
				want = os.ErrClosed
				if err := output.file.file.Close(); err != nil {
					t.Fatal(err)
				}
			}
			output.Logger().InfoContext(ctx, "failed persistence")
			if !errors.Is(context.Cause(ctx), want) {
				t.Fatalf("log failure was discarded: %v", context.Cause(ctx))
			}
			output.Logger().InfoContext(ctx, "shutdown message")
			if destination == "file" && (!strings.Contains(stderr.String(), "failed persistence") || !strings.Contains(stderr.String(), "shutdown message")) {
				t.Fatal("file failure disabled stderr")
			}
			if err := output.Close(); !errors.Is(err, want) {
				t.Fatalf("close discarded log failure: %v", err)
			}
		})
	}
}

func TestOversizedLogEntryIsRejected(t *testing.T) {
	path := filepath.Join(t.TempDir(), "service.log")
	file, err := openLogFile(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if n, err := file.Write(make([]byte, logFileLimit+1)); err == nil || n != 0 {
		t.Fatalf("oversized entry accepted: %d %v", n, err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Size() != 0 {
		t.Fatalf("oversized entry modified log: %v %v", info, err)
	}
}

func TestLogDestinationRejectsDevices(t *testing.T) {
	file, err := openLogFile(os.DevNull)
	if err == nil || file != nil || !strings.Contains(err.Error(), "regular file") {
		t.Fatalf("nonpersistent destination accepted: %v %v", file, err)
	}
}

func TestRotationFailurePreservesFilesystemCause(t *testing.T) {
	for _, failure := range []string{"closed handle", "removed active file"} {
		t.Run(failure, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "service.log")
			file, err := openLogFile(path)
			if err != nil {
				t.Fatal(err)
			}
			want := os.ErrClosed
			if failure == "closed handle" {
				if err := file.file.Close(); err != nil {
					t.Fatal(err)
				}
			} else {
				want = os.ErrNotExist
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			}
			if err := file.rotate(); !errors.Is(err, want) {
				t.Fatalf("rotation cause lost: %v", err)
			}
			if err := file.Close(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
