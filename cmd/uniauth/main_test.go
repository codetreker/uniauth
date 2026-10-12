package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMainHelp(t *testing.T) {
	originalArgs, originalOut := os.Args, os.Stdout
	output, err := os.Create(filepath.Join(t.TempDir(), "help.out"))
	if err != nil {
		t.Fatal(err)
	}
	defer output.Close()
	os.Args = []string{"uniauth", "help"}
	os.Stdout = output
	defer func() { os.Args = originalArgs; os.Stdout = originalOut }()
	main()
	if err := output.Close(); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(output.Name())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "Usage:") {
		t.Fatalf("main did not dispatch help: %s", body)
	}
}

func TestProcessOutcomeStatusAndDiagnostic(t *testing.T) {
	var out bytes.Buffer
	if code := reportResult(nil, &out); code != 0 || out.Len() != 0 {
		t.Fatalf("successful command: status %d, output %q", code, out.String())
	}
	cause := errors.New("configuration is invalid")
	if code := reportResult(cause, &out); code != 1 || out.String() != "configuration is invalid\n" {
		t.Fatalf("failed command: status %d, output %q", code, out.String())
	}
	if code := reportResult(cause, failedWriter{}); code != 1 {
		t.Fatalf("failed diagnostic write reported success: %d", code)
	}
}
