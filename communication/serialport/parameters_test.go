package serialport

import (
	"errors"
	"os"
	"testing"

	"github.com/fcying/SerialGo/internal/i18n"
)

func TestSetParametersOnClosedPort(t *testing.T) {
	var p Port
	if err := p.SetParameters(Settings{}, Settings{}); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("SetParameters() error = %v; want os.ErrClosed", err)
	}
}

func TestApplyWithRestore(t *testing.T) {
	applyFailed := errors.New("driver rejected new settings")
	restoreFailed := errors.New("driver rejected old settings")
	for _, tc := range []struct {
		name           string
		apply, restore error
		english        string
	}{
		{name: "success"},
		{"restored", applyFailed, nil, "apply serial port parameters: driver rejected new settings"},
		{"restore failure", applyFailed, restoreFailed, "apply serial port parameters: driver rejected new settings\nrestore previous serial port parameters: driver rejected old settings"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var restored bool
			err := applyWithRestore(func() error { return tc.apply }, func() error {
				restored = true
				return tc.restore
			})
			if restored != (tc.apply != nil) || !errors.Is(err, tc.apply) {
				t.Fatalf("restore attempted = %v, error = %v", restored, err)
			}
			var restoreErr *ParametersRestoreError
			if errors.As(err, &restoreErr) != (tc.restore != nil) || tc.restore != nil && !errors.Is(err, tc.restore) {
				t.Fatalf("restore error did not match %v: %v", tc.restore, err)
			}
			if got := i18n.ErrorText(err, true); got != tc.english {
				t.Errorf("English error = %q; want %q", got, tc.english)
			}
		})
	}
}
