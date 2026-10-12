package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func terminalPair(t *testing.T) (*os.File, *os.File) {
	t.Helper()
	master, err := os.OpenFile("/dev/ptmx", os.O_RDWR|unix.O_NOCTTY, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { master.Close() })
	if err := unix.IoctlSetPointerInt(int(master.Fd()), unix.TIOCSPTLCK, 0); err != nil {
		t.Fatal(err)
	}
	number, err := unix.IoctlGetInt(int(master.Fd()), unix.TIOCGPTN)
	if err != nil {
		t.Fatal(err)
	}
	slave, err := os.OpenFile(fmt.Sprintf("/dev/pts/%d", number), os.O_RDWR|unix.O_NOCTTY, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { slave.Close() })
	return master, slave
}

func TestTerminalPasswordIsHiddenAndTerminalRestored(t *testing.T) {
	master, slave := terminalPair(t)
	original, err := unix.IoctlGetTermios(int(slave.Fd()), unix.TCGETS)
	if err != nil {
		t.Fatal(err)
	}
	var prompt bytes.Buffer
	done := make(chan struct {
		password string
		err      error
	}, 1)
	go func() {
		password, err := readPassword(slave, &prompt)
		done <- struct {
			password string
			err      error
		}{password, err}
	}()
	deadline := time.Now().Add(time.Second)
	for {
		state, err := unix.IoctlGetTermios(int(slave.Fd()), unix.TCGETS)
		if err != nil {
			t.Fatal(err)
		}
		if state.Lflag&unix.ECHO == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("terminal echo was not disabled")
		}
		time.Sleep(time.Millisecond)
	}
	if _, err := master.Write([]byte("  terminal secret  \n")); err != nil {
		t.Fatal(err)
	}
	select {
	case result := <-done:
		if result.err != nil || result.password != "  terminal secret  " {
			t.Fatalf("terminal input: %q %v", result.password, result.err)
		}
	case <-time.After(time.Second):
		t.Fatal("terminal input did not complete")
	}
	restored, err := unix.IoctlGetTermios(int(slave.Fd()), unix.TCGETS)
	if err != nil {
		t.Fatal(err)
	}
	if *original != *restored {
		t.Fatal("terminal attributes were not restored")
	}
	if prompt.String() != "Administrator password: \n" {
		t.Fatalf("unexpected terminal prompt: %q", prompt.String())
	}
}

func TestTerminalPromptWriteFailure(t *testing.T) {
	_, slave := terminalPair(t)
	if _, err := readPassword(slave, failedWriter{}); err != io.ErrClosedPipe {
		t.Fatalf("prompt error lost: %v", err)
	}
}

type failNewlineWriter struct{}

func (failNewlineWriter) Write(body []byte) (int, error) {
	if string(body) == "\n" {
		return 0, io.ErrClosedPipe
	}
	return len(body), nil
}

func TestTerminalNewlineWriteFailure(t *testing.T) {
	master, slave := terminalPair(t)
	done := make(chan error, 1)
	go func() { _, err := readPassword(slave, failNewlineWriter{}); done <- err }()
	deadline := time.Now().Add(time.Second)
	for {
		state, err := unix.IoctlGetTermios(int(slave.Fd()), unix.TCGETS)
		if err != nil {
			t.Fatal(err)
		}
		if state.Lflag&unix.ECHO == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("terminal echo was not disabled")
		}
		time.Sleep(time.Millisecond)
	}
	if _, err := master.Write([]byte("secret\n")); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if !errors.Is(err, io.ErrClosedPipe) {
			t.Fatalf("newline error lost: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("terminal password read blocked")
	}
}
