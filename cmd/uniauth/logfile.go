package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strconv"
	"sync"
)

const logFileLimit = 10 << 20
const logFileBackups = 3

var errLogNotRegular = errors.New("log destination must be a regular file")

type rotatingLogFile struct {
	path string
	file *os.File
	size int64
}

func openLogFile(path string) (*rotatingLogFile, error) {
	info, err := os.Stat(path)
	if err == nil && !info.Mode().IsRegular() {
		return nil, errLogNotRegular
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return nil, err
	}
	info, err = file.Stat()
	if err != nil {
		return nil, errors.Join(err, file.Close())
	}
	if !info.Mode().IsRegular() {
		return nil, errors.Join(errLogNotRegular, file.Close())
	}
	return &rotatingLogFile{path: path, file: file, size: info.Size()}, nil
}

func (f *rotatingLogFile) Write(body []byte) (int, error) {
	if len(body) > logFileLimit {
		return 0, errors.New("log entry exceeds the file size limit")
	}
	if f.size+int64(len(body)) > logFileLimit {
		if err := f.rotate(); err != nil {
			return 0, err
		}
	}
	n, err := f.file.Write(body)
	f.size += int64(n)
	return n, err
}

func (f *rotatingLogFile) rotate() error {
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Remove(f.path + "." + strconv.Itoa(logFileBackups)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	for backup := logFileBackups - 1; backup > 0; backup-- {
		old := f.path + "." + strconv.Itoa(backup)
		if err := os.Rename(old, f.path+"."+strconv.Itoa(backup+1)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	if err := os.Rename(f.path, f.path+".1"); err != nil {
		return err
	}
	file, err := openLogFile(f.path)
	if err != nil {
		return err
	}
	*f = *file
	return nil
}

func (f *rotatingLogFile) Close() error {
	if f.file == nil {
		return nil
	}
	err := f.file.Close()
	f.file = nil
	return err
}

type logOutput struct {
	mu      sync.Mutex
	stderr  io.Writer
	file    *rotatingLogFile
	cancel  context.CancelCauseFunc
	failure error
}

func newLogOutput(path string, stderr io.Writer, cancel context.CancelCauseFunc) (*logOutput, error) {
	output := &logOutput{stderr: stderr, cancel: cancel}
	if path != "" {
		file, err := openLogFile(path)
		if err != nil {
			return nil, err
		}
		output.file = file
	}
	return output, nil
}

func (w *logOutput) Logger() *slog.Logger {
	return slog.New(slog.NewJSONHandler(w, nil))
}

func (w *logOutput) Write(body []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	n, stderrErr := w.stderr.Write(body)
	if stderrErr == nil && n != len(body) {
		stderrErr = io.ErrShortWrite
	}
	var fileErr error
	if w.file != nil && w.failure == nil {
		written, err := w.file.Write(body)
		if err == nil && written != len(body) {
			err = io.ErrShortWrite
		}
		if err != nil {
			fileErr = fmt.Errorf("write log file: %w", err)
		}
	}
	err := errors.Join(stderrErr, fileErr)
	if err != nil && w.failure == nil {
		// slog discards Handler errors, so retain the cause and stop serving.
		w.failure = err
		w.cancel(err)
	}
	return n, err
}

func (w *logOutput) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	var closeErr error
	if w.file != nil {
		closeErr = w.file.Close()
	}
	return errors.Join(w.failure, closeErr)
}
