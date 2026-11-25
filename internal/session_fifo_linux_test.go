//go:build linux

package internal

import (
	"context"
	"errors"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestSendFileRejectsFIFOWithoutBlocking(t *testing.T) {
	path := filepath.Join(t.TempDir(), "stream")
	if err := syscall.Mkfifo(path, 0600); err != nil {
		t.Fatal(err)
	}

	sender := &fileSendTestSender{}
	done := make(chan error, 1)
	go func() {
		done <- sendFile(context.Background(), path, 0, sender)
	}()

	select {
	case err := <-done:
		if !errors.Is(err, errFileNotRegular) {
			t.Fatalf("sendFile() error = %v, want %v", err, errFileNotRegular)
		}
	case <-time.After(time.Second):
		t.Fatal("sendFile() blocked while opening FIFO")
	}
	if len(sender.data) != 0 {
		t.Fatalf("sent %d bytes from FIFO, want none", len(sender.data))
	}
}

func TestOutputOperationsRejectFIFOWithoutBlocking(t *testing.T) {
	for _, capture := range []bool{false, true} {
		path := filepath.Join(t.TempDir(), "stream")
		if err := syscall.Mkfifo(path, 0600); err != nil {
			t.Fatal(err)
		}
		view := &appTestView{callbacks: make(chan func(), 1)}
		p := &Session{ctx: context.Background()}
		s := &App{view: view, session: p}
		if capture {
			s.CaptureTarget()(path)
		} else {
			s.SaveLog(path, "data")
		}
		select {
		case fn := <-view.callbacks:
			fn()
		case <-time.After(time.Second):
			t.Fatalf("capture=%t: opening FIFO blocked", capture)
		}
		if !s.StatusError || s.FileBusy || s.CapturePath != "" {
			t.Fatalf("capture=%t: FIFO was not rejected: status=%+v busy=%t path=%q", capture, s.Status, s.FileBusy, s.CapturePath)
		}
		s.jobs.Wait()
	}
}
