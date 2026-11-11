package ui

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/fcying/SerialGo/config"

	"github.com/BurntSushi/toml"
)

func settingsSaveFixture(t *testing.T, path string, save func(string, config.Settings) error) (*controller, <-chan struct{}) {
	t.Helper()
	s := &controller{path: path, closed: make(chan struct{}), settings: config.Settings{Language: "en", Presets: []config.Preset{config.NewPreset()}}}
	notified := make(chan struct{}, 16)
	s.saver = newSettingsSaver(path, save, func() { notified <- struct{}{} })
	t.Cleanup(s.stopSettingsSaver)
	return s, notified
}

func awaitSettingsSignal(t *testing.T, signal <-chan struct{}) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(5 * time.Second):
		t.Fatal("settings writer did not reach the expected transition")
	}
}

func completeSettingsSave(t *testing.T, s *controller, notified <-chan struct{}) {
	t.Helper()
	awaitSettingsSignal(t, notified)
	s.collectSettingsResults(s.saver)
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

func draftSettings(s *controller, text string) {
	s.settings.SendText = text
	s.settingsChanged()
}

func TestSettingsDirtyMarksBeforeSaverAndEditsDoNotWrite(t *testing.T) {
	s := &controller{}
	s.settingsChanged()
	if !s.settingsDirty || s.settingsRevision != 1 || s.saver != nil {
		t.Fatal("an edit must mark dirty without starting persistence")
	}
	path := filepath.Join(t.TempDir(), "config.toml")
	s, notified := settingsSaveFixture(t, path, config.Save)
	draftSettings(s, "draft")
	s.settings.Presets[0].Data = "draft preset"
	s.settingsChanged()
	if !s.settingsDirty || s.settingsRevision != 2 || len(s.saver.pending) != 0 {
		t.Fatal("draft edits did not remain dirty and unqueued")
	}
	s.stopSettingsSaver()
	select {
	case <-notified:
		t.Fatal("editing invoked the settings writer")
	default:
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("editing created a configuration: %v", err)
	}
}

func TestSettingsUsedWritesOnceAndUnchangedExitDoesNotWrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	s, notified := settingsSaveFixture(t, path, config.Save)
	if err := s.flushSettings(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("clean initial exit wrote configuration: %v", err)
	}
	// Use a fresh saver after the initial clean-shutdown assertion.
	s, notified = settingsSaveFixture(t, path, config.Save)
	draftSettings(s, "used")
	s.saveUsedSettings()
	completeSettingsSave(t, s, notified)
	if s.settingsDirty || s.statusError {
		t.Fatal("successful current-revision save did not clear dirty")
	}
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	for range 10 {
		s.saveUsedSettings()
	}
	s.stopSettingsSaver()
	if err := s.flushSettings(); err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(path)
	if err != nil || !os.SameFile(before, after) {
		t.Fatalf("unchanged repeated use or exit rewrote configuration: %v", err)
	}
	select {
	case <-notified:
		t.Fatal("unchanged use queued another write")
	default:
	}
}

func TestSettingsSameRevisionUsesDeduplicateAndSnapshotSurvivesDrafts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	started := make(chan struct{}, 8)
	release := make(chan struct{})
	s, notified := settingsSaveFixture(t, path, func(path string, settings config.Settings) error {
		started <- struct{}{}
		<-release
		return config.Save(path, settings)
	})
	t.Cleanup(func() { close(release) })
	s.settings.Presets[0].Data = "used preset"
	s.settings.BaudRates = []string{"250000", "38400"}
	draftSettings(s, "used")
	s.saveUsedSettings()
	awaitSettingsSignal(t, started)
	for range 10 {
		s.saveUsedSettings()
	}
	if len(s.saver.pending) != 0 {
		t.Fatal("same queued revision was requested more than once")
	}
	s.settings.Presets[0].Data = "later draft preset"
	s.settings.BaudRates[0] = "500000"
	s.settings.BaudRates = append(s.settings.BaudRates, "750000")
	draftSettings(s, "later draft")
	release <- struct{}{}
	completeSettingsSave(t, s, notified)
	stored := readSavedSettings(t, path)
	if stored.SendText != "used" || stored.Presets[0].Data != "used preset" || !slices.Equal(stored.BaudRates, []string{"250000", "38400"}) {
		t.Fatal("later edits mutated the accepted operation snapshot")
	}
	if !s.settingsDirty {
		t.Fatal("old save success cleared a newer draft")
	}
	s.stopSettingsSaver()
	if err := s.flushSettings(); err != nil {
		t.Fatal(err)
	}
	if stored := readSavedSettings(t, path); stored.SendText != "later draft" || stored.Presets[0].Data != "later draft preset" || !slices.Equal(stored.BaudRates, []string{"500000", "38400", "750000"}) {
		t.Fatal("final flush did not save current drafts")
	}
	select {
	case <-started:
		t.Fatal("duplicate use reached the background writer")
	default:
	}
}

func TestSettingsFailureRetriesSameRevisionAndOwnsOnlyItsError(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "missing")
	path := filepath.Join(dir, "config.toml")
	s, notified := settingsSaveFixture(t, path, config.Save)
	draftSettings(s, "used")
	s.saveUsedSettings()
	completeSettingsSave(t, s, notified)
	if !s.settingsDirty || !s.statusError || !s.settingsSaveError || s.saver.pendingRequest {
		t.Fatal("write failure did not retain dirty and enable retry")
	}
	failure := s.status
	s.note("发送完成", "Send completed")
	if s.status != failure || !s.statusError {
		t.Fatal("later operation success hid a settings failure")
	}
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	revision := s.settingsRevision
	s.saveUsedSettings()
	completeSettingsSave(t, s, notified)
	if s.settingsRevision != revision || s.settingsDirty || s.statusError || s.settingsSaveError {
		t.Fatal("same-revision retry did not recover the owned error")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(dir); err != nil {
		t.Fatal(err)
	}
	draftSettings(s, "another use")
	s.fail("串口错误", "Serial error", os.ErrPermission)
	operation := s.status
	s.saveUsedSettings()
	completeSettingsSave(t, s, notified)
	if !s.settingsDirty || s.status != operation || s.settingsSaveError {
		t.Fatal("settings failure replaced an unrelated operation error")
	}
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	s.saveUsedSettings()
	completeSettingsSave(t, s, notified)
	if s.settingsDirty || !s.statusError || s.status != operation {
		t.Fatal("settings recovery erased an unrelated operation error")
	}
}

func TestSettingsEmptyPathNeverFallsBackAndRemainsDirty(t *testing.T) {
	called := make(chan struct{}, 1)
	s, notified := settingsSaveFixture(t, "", func(path string, settings config.Settings) error {
		called <- struct{}{}
		return config.Save(path, settings)
	})
	draftSettings(s, "protected defaults")
	s.saveUsedSettings()
	completeSettingsSave(t, s, notified)
	if !s.settingsDirty || !s.settingsSaveError || !s.statusError {
		t.Fatal("unavailable binary-adjacent configuration silently succeeded")
	}
	if err := s.flushSettings(); err == nil || !s.settingsDirty {
		t.Fatal("final flush silently accepted an unavailable path")
	}
	select {
	case <-called:
		t.Fatal("empty path reached the writer")
	default:
	}
}

func TestSettingsSerializedLatestWinsAndFinalFlushDrainsDrafts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	started := make(chan struct{}, 8)
	release := make(chan struct{})
	s, notified := settingsSaveFixture(t, path, func(path string, settings config.Settings) error {
		started <- struct{}{}
		<-release
		return config.Save(path, settings)
	})
	t.Cleanup(func() { close(release) })
	draftSettings(s, "in flight")
	s.saveUsedSettings()
	awaitSettingsSignal(t, started)
	draftSettings(s, "superseded")
	s.saveUsedSettings()
	draftSettings(s, "latest used")
	s.saveUsedSettings()
	if len(s.saver.pending) != 1 {
		t.Fatal("pending used snapshots are not bounded to the latest one")
	}
	draftSettings(s, "final unused draft")
	flushed := make(chan error, 1)
	go func() { flushed <- s.flushSettings() }()
	release <- struct{}{}
	awaitSettingsSignal(t, notified)
	awaitSettingsSignal(t, started)
	if stored := readSavedSettings(t, path); stored.SendText != "in flight" {
		t.Fatal("writer ordering or immutable snapshot was lost")
	}
	select {
	case err := <-flushed:
		t.Fatalf("final flush overtook a blocked used snapshot: %v", err)
	default:
	}
	release <- struct{}{}
	awaitSettingsSignal(t, notified)
	select {
	case err := <-flushed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("final flush did not drain")
	}
	if stored := readSavedSettings(t, path); stored.SendText != "final unused draft" || s.settingsDirty {
		t.Fatal("final flush did not win after draining used settings")
	}
	select {
	case <-started:
		t.Fatal("superseded used snapshot reached disk")
	default:
	}
}

func TestSettingsShutdownCollectsUnpostedAcknowledgement(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	s, notified := settingsSaveFixture(t, path, config.Save)
	draftSettings(s, "used")
	s.saveUsedSettings()
	awaitSettingsSignal(t, notified)
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	close(s.closed)
	s.stopSettingsSaver()
	if s.settingsDirty {
		t.Fatal("shutdown lost a successful acknowledgement when UI posting stopped")
	}
	if err := s.flushSettings(); err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(path)
	if err != nil || !os.SameFile(before, after) {
		t.Fatalf("acknowledged settings were unnecessarily rewritten on exit: %v", err)
	}
}

func TestSettingsDisplayUseBoundaries(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	s, notified := settingsSaveFixture(t, path, config.Save)
	s.settings.ReceiveHex = true
	s.displaySettingsChanged()
	if !s.settingsDirty || !s.log.options.hex || len(s.saver.pending) != 0 {
		t.Fatal("text display edit must configure and mark without saving")
	}
	s.settings.Timestamp = true
	s.applyDisplaySettings()
	completeSettingsSave(t, s, notified)
	stored := readSavedSettings(t, path)
	if s.settingsDirty || !stored.ReceiveHex || !stored.Timestamp {
		t.Fatal("immediate display use did not persist current settings")
	}
}

func TestUnchangedClearAndPortSettingsDoNotWrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	s, _ := settingsSaveFixture(t, path, config.Save)
	s.clearSend()
	s.openPortSettings()
	s.applyPortSettings()
	s.saveUsedSettings()
	err := s.flushSettings()
	if err != nil {
		t.Fatal(err)
	}
	if s.settingsDirty {
		t.Fatal("unchanged controls marked settings dirty")
	}
	_, err = os.Stat(path)
	if !os.IsNotExist(err) {
		t.Fatalf("unchanged controls created a configuration: %v", err)
	}
}
