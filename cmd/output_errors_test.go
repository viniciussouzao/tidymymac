package cmd

import (
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/viniciussouzao/tidymymac/internal/commands"
)

var errOutputUnavailable = errors.New("output unavailable")
var errCloseUnavailable = errors.New("close unavailable")

type failingWriter struct {
	err error
}

func (w failingWriter) Write([]byte) (int, error) {
	return 0, w.err
}

type failingReadCloser struct {
	io.Reader
	closeErr error
}

func (r failingReadCloser) Close() error {
	return r.closeErr
}

type failingWriteCloser struct {
	writeErr error
	closeErr error
}

func (w failingWriteCloser) Write(p []byte) (int, error) {
	if w.writeErr != nil {
		return 0, w.writeErr
	}
	return len(p), nil
}

func (w failingWriteCloser) Close() error {
	return w.closeErr
}

func TestCommandsReturnOutputErrors(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("SUDO_USER", "")

	tests := []struct {
		name string
		cmd  interface {
			SetOut(io.Writer)
		}
		run func() error
	}{
		{
			name: "list categories",
			cmd:  listCategoriesCmd,
			run: func() error {
				return listCategoriesCmd.RunE(listCategoriesCmd, nil)
			},
		},
		{
			name: "history",
			cmd:  historyCmd,
			run: func() error {
				return historyCmd.RunE(historyCmd, nil)
			},
		},
		{
			name: "stats",
			cmd:  statsCmd,
			run: func() error {
				return statsCmd.RunE(statsCmd, nil)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.cmd.SetOut(failingWriter{err: errOutputUnavailable})
			t.Cleanup(func() { tt.cmd.SetOut(nil) })

			err := tt.run()
			if !errors.Is(err, errOutputUnavailable) {
				t.Fatalf("error = %v, want wrapped %v", err, errOutputUnavailable)
			}
		})
	}
}

func TestLoadScanResultReaderReturnsCloseError(t *testing.T) {
	_, err := loadScanResultReader(failingReadCloser{
		Reader:   strings.NewReader(`{}`),
		closeErr: errCloseUnavailable,
	})
	if !errors.Is(err, errCloseUnavailable) {
		t.Fatalf("error = %v, want wrapped %v", err, errCloseUnavailable)
	}
}

func TestLoadScanResultReaderPreservesReadError(t *testing.T) {
	_, err := loadScanResultReader(failingReadCloser{
		Reader:   strings.NewReader(`not json`),
		closeErr: errCloseUnavailable,
	})
	if errors.Is(err, errCloseUnavailable) {
		t.Fatalf("error = %v, should preserve the read error", err)
	}
}

func TestWriteScanOutputFileReturnsCloseError(t *testing.T) {
	err := writeScanOutputFile(failingWriteCloser{closeErr: errCloseUnavailable}, commands.ScanResult{}, "json", false)
	if !errors.Is(err, errCloseUnavailable) {
		t.Fatalf("error = %v, want wrapped %v", err, errCloseUnavailable)
	}
}

func TestWriteScanOutputFilePreservesWriteError(t *testing.T) {
	err := writeScanOutputFile(failingWriteCloser{
		writeErr: errOutputUnavailable,
		closeErr: errCloseUnavailable,
	}, commands.ScanResult{}, "json", false)
	if !errors.Is(err, errOutputUnavailable) {
		t.Fatalf("error = %v, want wrapped %v", err, errOutputUnavailable)
	}
	if errors.Is(err, errCloseUnavailable) {
		t.Fatalf("error = %v, should preserve the write error", err)
	}
}
