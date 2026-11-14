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
