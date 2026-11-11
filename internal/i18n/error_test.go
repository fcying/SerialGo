package i18n

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
)

func TestWrapErrorPreservesIdentityAndExternalText(t *testing.T) {
	cause := &os.PathError{Op: "open", Path: "用户.bin", Err: os.ErrNotExist}
	err := WrapError("打开失败", "Open failed", cause)
	if errors.Unwrap(err) != cause || !errors.Is(err, os.ErrNotExist) {
		t.Fatal("underlying error identity was lost")
	}
	var pathErr *os.PathError
	if !errors.As(err, &pathErr) || pathErr != cause {
		t.Fatal("underlying error type was lost")
	}
	if got, want := err.Error(), "打开失败: "+cause.Error(); got != want {
		t.Errorf("Error() = %q; want %q", got, want)
	}
	if got, want := ErrorText(err, true), "Open failed: "+cause.Error(); got != want {
		t.Errorf("English text = %q; want %q", got, want)
	}
	if got := ErrorText(cause, true); got != cause.Error() {
		t.Errorf("external error text changed: %q", got)
	}
}

func TestErrorTextTranslatesNestedAndJoinedErrors(t *testing.T) {
	inner := WrapError("发送失败", "Send failed", context.Canceled)
	outer := WrapError("任务失败", "Task failed", inner)
	if got, want := ErrorText(outer, true), "Task failed: Send failed: context canceled"; got != want {
		t.Errorf("nested text = %q; want %q", got, want)
	}
	if !errors.Is(outer, context.Canceled) {
		t.Error("nested cancellation identity was lost")
	}
	wrapped := fmt.Errorf("worker: %w", outer)
	if got, want := ErrorText(wrapped, true), "worker: Task failed: Send failed: context canceled"; got != want {
		t.Errorf("wrapped text = %q; want %q", got, want)
	}
	plain := errors.New("library error")
	joined := errors.Join(outer, plain)
	if got, want := ErrorText(joined, true), "Task failed: Send failed: context canceled\nlibrary error"; got != want {
		t.Errorf("joined text = %q; want %q", got, want)
	}
	multiple := fmt.Errorf("worker: %w plus %w", outer, plain)
	if got, want := ErrorText(multiple, true), "worker: Task failed: Send failed: context canceled plus library error"; got != want {
		t.Errorf("multiple-wrapper text = %q; want %q", got, want)
	}
	if got := ErrorText(joined, false); got != joined.Error() {
		t.Errorf("Chinese joined text changed: %q", got)
	}
}
