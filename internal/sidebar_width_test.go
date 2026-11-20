package internal

import (
	"errors"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/fcying/SerialGo/config"
)

func TestSetCurveSidebarWidthOnlyMarksChangedPreferencesDirty(t *testing.T) {
	for _, test := range []struct {
		name                string
		current, next, want float32
		changed             bool
	}{
		{name: "unchanged fraction", current: 317.25, next: 317.25, want: 317.25},
		{name: "missing current equals default", current: 0, next: 260, want: 260},
		{name: "invalid current equals normalized input", current: float32(math.NaN()), next: -1, want: 260},
		{name: "infinite input equals default", current: 260, next: float32(math.Inf(1)), want: 260},
		{name: "fraction", current: 260, next: 317.25, want: 317.25, changed: true},
		{name: "invalid input becomes default", current: 317.25, next: float32(math.NaN()), want: 260, changed: true},
		{name: "unclamped preference", current: 260, next: 1800.5, want: 1800.5, changed: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.toml")
			s, notified := settingsSaveFixture(t, path, config.Save)
			s.Settings.CurveSidebarWidth = test.current
			s.Settings.Window.Width = 400
			changed := s.SetCurveSidebarWidth(test.next)
			if changed != test.changed {
				t.Fatalf("SetCurveSidebarWidth changed = %v, want %v", changed, test.changed)
			}
			width := config.NormalizeCurveSidebarWidth(s.Settings.CurveSidebarWidth)
			if width != test.want {
				t.Fatalf("preferred sidebar width = %v, want %v", width, test.want)
			}
			if s.SettingsDirty != test.changed {
				t.Fatalf("dirty = %v, want %v", s.SettingsDirty, test.changed)
			}
			var wantRevision uint64
			if test.changed {
				wantRevision = 1
			}
			if s.SettingsRevision != wantRevision {
				t.Fatalf("revision = %v, want %v", s.SettingsRevision, wantRevision)
			}
			s.stopSettingsSaver()
			select {
			case <-notified:
				t.Fatal("setting sidebar width invoked the settings writer")
			default:
			}
			if !test.changed {
				err := s.FlushSettings()
				if err != nil {
					t.Fatal(err)
				}
			}
			_, err := os.Stat(path)
			if !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("editing or unchanged exit wrote configuration: %v", err)
			}
		})
	}
}

func TestCurveSidebarWidthUsedSnapshotAndFinalFlush(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	s, notified := settingsSaveFixture(t, path, func(path string, settings config.Settings) error {
		started <- struct{}{}
		<-release
		return config.Save(path, settings)
	})
	t.Cleanup(func() { close(release) })
	changed := s.SetCurveSidebarWidth(333.625)
	if !changed {
		t.Fatal("new width was not accepted")
	}
	s.SaveUsedSettings()
	awaitSettingsSignal(t, started)
	changed = s.SetCurveSidebarWidth(444.25)
	if !changed {
		t.Fatal("later draft width was not accepted")
	}
	release <- struct{}{}
	completeSettingsSave(t, s, notified)
	width := readSavedSettings(t, path).CurveSidebarWidth
	if width != 333.625 {
		t.Fatalf("background snapshot width = %v, want 333.625", width)
	}
	if !s.SettingsDirty || s.StatusError {
		t.Fatal("saving an older width must leave the latest preference dirty without an error")
	}
	err := s.FlushSettings()
	if err != nil {
		t.Fatal(err)
	}
	width = readSavedSettings(t, path).CurveSidebarWidth
	if width != 444.25 {
		t.Fatalf("final flush width = %v, want 444.25", width)
	}
	if s.SettingsDirty {
		t.Fatal("successful final flush left the preference dirty")
	}
}
