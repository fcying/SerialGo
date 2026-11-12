package internal

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
)

type fileSendTestContext struct {
	context.Context
	errChecks int
}

func (c *fileSendTestContext) Err() error {
	c.errChecks++
	return nil
}

type fileSendTestSender struct {
	data []byte
}

func (s *fileSendTestSender) Send(_ context.Context, data []byte) error {
	s.data = append(s.data, data...)
	return nil
}

func TestSendFileWaitsOnlyBetweenChunks(t *testing.T) {
	want := bytes.Repeat([]byte{0x5A}, 257)
	path := filepath.Join(t.TempDir(), "payload.bin")
	if err := os.WriteFile(path, want, 0600); err != nil {
		t.Fatal(err)
	}
	ctx := &fileSendTestContext{Context: context.Background()}
	sender := &fileSendTestSender{}
	if err := sendFile(ctx, path, 0, sender); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(sender.data, want) {
		t.Fatalf("sent data = % x, want % x", sender.data, want)
	}
	if ctx.errChecks != 1 {
		t.Fatalf("inter-chunk cancellation checks = %d, want 1", ctx.errChecks)
	}
}
