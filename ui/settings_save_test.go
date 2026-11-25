package ui

import (
	"os"
	"testing"
	"time"

	"github.com/fcying/SerialGo/config"
	"github.com/fcying/SerialGo/internal"

	"github.com/BurntSushi/toml"
)

type settingsTestView struct{ callbacks chan func() }

func (v *settingsTestView) Dispatch(fn func())            { v.callbacks <- fn }
func (v *settingsTestView) AppendEvents([]internal.Event) {}

// UI tests exercise real writes to their own temporary configuration paths.
type settingsTestStore struct{}

func (settingsTestStore) SaveWithIdentity(path string, settings config.Settings) (*os.File, error) {
	return config.SaveWithIdentity(path, settings)
}

func (settingsTestStore) OutputIdentity() (os.FileInfo, os.FileInfo, string, error) {
	return nil, nil, "", nil
}

func (settingsTestStore) Close() error { return nil }

func settingsSaveFixture(t *testing.T, path string) (*controller, <-chan func()) {
	t.Helper()
	app := internal.NewAppWithSettingsStore(config.Settings{
		Language: "en", DisplayBufferLimit: config.NormalizeDisplayBufferLimit(0),
		ReceiveHexBytesPerLine: config.DefaultReceiveHexBytesPerLine,
		ReceiveFont:            config.DefaultReceiveFont, ReceiveFontSize: config.DefaultReceiveFontSize, ReceiveFontColor: config.DefaultReceiveFontColor,
		CurveSidebarWidth: config.DefaultCurveSidebarWidth,
		Presets:           []config.Preset{config.NewPreset()},
	}, path, settingsTestStore{})
	s := newController(app)
	view := &settingsTestView{callbacks: make(chan func(), 32)}
	app.Startup(view)
	t.Cleanup(func() { _ = app.Shutdown() })
	return s, view.callbacks
}

func completeSettingsSave(t *testing.T, s *controller, notified <-chan func()) {
	t.Helper()
	select {
	case fn := <-notified:
		fn()
	case <-time.After(5 * time.Second):
		t.Fatal("settings writer did not complete")
	}
}

func readSavedSettings(t *testing.T, path string) config.Settings {
	t.Helper()
	var settings config.Settings
	stored := struct {
		*config.Settings
		Presets []config.Preset
	}{Settings: &settings}
	if _, err := toml.DecodeFile(path, &stored); err != nil {
		t.Fatal(err)
	}
	settings.Presets = stored.Presets
	return settings
}

func draftSettings(s *controller, text string) { s.settings.SendText = text; s.app.SettingsChanged() }

func TestSettingsDisplayUseBoundaries(t *testing.T) {
	path := t.TempDir() + "/config.toml"
	s, notified := settingsSaveFixture(t, path)
	s.settings.ReceiveHex = true
	s.displaySettingsChanged()
	if !s.app.SettingsDirty || !s.log.options.hex {
		t.Fatal("display edit must configure and mark dirty")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("draft display edit wrote preferences")
	}
	s.settings.Timestamp = true
	s.applyDisplaySettings()
	completeSettingsSave(t, s, notified)
	stored := readSavedSettings(t, path)
	if s.app.SettingsDirty || !stored.ReceiveHex || !stored.Timestamp {
		t.Fatal("display use did not persist preferences")
	}
}
