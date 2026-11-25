package internal

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fcying/SerialGo/config"

	"github.com/BurntSushi/toml"
)

type testSettingsStore struct {
	save     func(string, config.Settings) (*os.File, error)
	close    func() error
	identity func() (os.FileInfo, os.FileInfo, string, error)
}

func (s *testSettingsStore) SaveWithIdentity(path string, settings config.Settings) (*os.File, error) {
	if s.save != nil {
		return s.save(path, settings)
	}
	return config.SaveWithIdentity(path, settings)
}

func (s *testSettingsStore) OutputIdentity() (os.FileInfo, os.FileInfo, string, error) {
	if s.identity != nil {
		return s.identity()
	}
	return nil, nil, "", nil
}

func (s *testSettingsStore) Close() error {
	if s.close != nil {
		return s.close()
	}
	return nil
}

func settingsSaveFixture(t *testing.T, path string, save func(string, config.Settings) error) (*App, <-chan struct{}) {
	t.Helper()
	s := &App{SettingsPath: path, settingsStore: &testSettingsStore{}, closed: make(chan struct{}), Settings: config.Settings{Language: "en", Presets: []config.Preset{config.NewPreset()}}}
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

func completeSettingsSave(t *testing.T, s *App, notified <-chan struct{}) {
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

func draftSettings(s *App, text string) {
	s.Settings.SendText = text
	s.SettingsChanged()
}

func TestSettingsDirtyMarksBeforeSaverAndEditsDoNotWrite(t *testing.T) {
	s := &App{}
	s.SettingsChanged()
	if !s.SettingsDirty || s.SettingsRevision != 1 || s.saver != nil {
		t.Fatal("an edit must mark dirty without starting persistence")
	}
	path := filepath.Join(t.TempDir(), "config.toml")
	s, notified := settingsSaveFixture(t, path, config.Save)
	draftSettings(s, "draft")
	s.Settings.Presets[0].Data = "draft preset"
	s.SettingsChanged()
	if !s.SettingsDirty || s.SettingsRevision != 2 || len(s.saver.pending) != 0 {
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
	if err := s.FlushSettings(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("clean initial exit wrote configuration: %v", err)
	}
	// Use a fresh saver after the initial clean-shutdown assertion.
	s, notified = settingsSaveFixture(t, path, config.Save)
	draftSettings(s, "used")
	s.SaveUsedSettings()
	completeSettingsSave(t, s, notified)
	if s.SettingsDirty || s.StatusError {
		t.Fatal("successful current-revision save did not clear dirty")
	}
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	for range 10 {
		s.SaveUsedSettings()
	}
	s.stopSettingsSaver()
	if err := s.FlushSettings(); err != nil {
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
	s.Settings.Presets[0].Data = "used preset"
	s.Settings.BaudRates = []string{"250000", "38400"}
	draftSettings(s, "used")
	s.SaveUsedSettings()
	awaitSettingsSignal(t, started)
	for range 10 {
		s.SaveUsedSettings()
	}
	if len(s.saver.pending) != 0 {
		t.Fatal("same queued revision was requested more than once")
	}
	s.Settings.Presets[0].Data = "later draft preset"
	s.Settings.BaudRates[0] = "500000"
	s.Settings.BaudRates = append(s.Settings.BaudRates, "750000")
	draftSettings(s, "later draft")
	release <- struct{}{}
	completeSettingsSave(t, s, notified)
	stored := readSavedSettings(t, path)
	if stored.SendText != "used" || stored.Presets[0].Data != "used preset" || !slices.Equal(stored.BaudRates, []string{"250000", "38400"}) {
		t.Fatal("later edits mutated the accepted operation snapshot")
	}
	if !s.SettingsDirty {
		t.Fatal("old save success cleared a newer draft")
	}
	s.stopSettingsSaver()
	if err := s.FlushSettings(); err != nil {
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
	s.SaveUsedSettings()
	completeSettingsSave(t, s, notified)
	if !s.SettingsDirty || !s.StatusError || !s.settingsSaveError || s.saver.pendingRequest {
		t.Fatal("write failure did not retain dirty and enable retry")
	}
	failure := s.Status
	s.Note("发送完成", "Send completed")
	if s.Status != failure || !s.StatusError {
		t.Fatal("later operation success hid a settings failure")
	}
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	revision := s.SettingsRevision
	s.SaveUsedSettings()
	completeSettingsSave(t, s, notified)
	if s.SettingsRevision != revision || s.SettingsDirty || s.StatusError || s.settingsSaveError {
		t.Fatal("same-revision retry did not recover the owned error")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(dir); err != nil {
		t.Fatal(err)
	}
	draftSettings(s, "another use")
	s.Fail("串口错误", "Serial error", os.ErrPermission)
	operation := s.Status
	s.SaveUsedSettings()
	completeSettingsSave(t, s, notified)
	if !s.SettingsDirty || !s.StatusError || !s.settingsSaveError ||
		!strings.Contains(s.Status.EN, "Failed to save settings") || !strings.Contains(s.Status.EN, operation.EN) ||
		!strings.Contains(s.Status.ZH, "保存参数失败") || !strings.Contains(s.Status.ZH, operation.ZH) {
		t.Fatalf("settings and operation failures should both be visible: %+v", s.Status)
	}
	s.SaveUsedSettings()
	completeSettingsSave(t, s, notified)
	if strings.Count(s.Status.EN, "Failed to save settings") != 1 || strings.Count(s.Status.EN, operation.EN) != 1 {
		t.Fatalf("repeated failure duplicated status: %+v", s.Status)
	}
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	s.SaveUsedSettings()
	completeSettingsSave(t, s, notified)
	if s.SettingsDirty || !s.StatusError || s.Status != operation {
		t.Fatal("settings recovery erased an unrelated operation error")
	}
}

func TestSettingsRecoveryDoesNotEraseNewerOperationFailure(t *testing.T) {
	fail := true
	s, notified := settingsSaveFixture(t, "settings.toml", func(string, config.Settings) error {
		if fail {
			return errors.New("save failed")
		}
		return nil
	})
	draftSettings(s, "used")
	s.SaveUsedSettings()
	completeSettingsSave(t, s, notified)
	if !s.SettingsDirty || !strings.Contains(s.Status.EN, "save failed") {
		t.Fatalf("save failure not visible: %+v", s.Status)
	}
	s.Fail("串口错误", "Serial error", errors.New("operation failed"))
	operation := s.saver.previousError
	if !s.settingsSaveError || !strings.Contains(s.Status.EN, "save failed") || !strings.Contains(s.Status.EN, "operation failed") {
		t.Fatalf("later operation error hid save failure: %+v", s.Status)
	}
	s.Note("发送完成", "Send completed")
	if !strings.Contains(s.Status.EN, "save failed") || !strings.Contains(s.Status.EN, "operation failed") {
		t.Fatalf("later success hid unresolved errors: %+v", s.Status)
	}
	fail = false
	s.SaveUsedSettings()
	completeSettingsSave(t, s, notified)
	if s.SettingsDirty || !s.StatusError || s.Status != operation {
		t.Fatalf("save recovery changed newer operation failure: %+v", s.Status)
	}
}

func TestSettingsEmptyPathNeverFallsBackAndRemainsDirty(t *testing.T) {
	called := make(chan struct{}, 1)
	s, notified := settingsSaveFixture(t, "", func(path string, settings config.Settings) error {
		called <- struct{}{}
		return config.Save(path, settings)
	})
	draftSettings(s, "protected defaults")
	s.SaveUsedSettings()
	completeSettingsSave(t, s, notified)
	if !s.SettingsDirty || !s.settingsSaveError || !s.StatusError {
		t.Fatal("unavailable binary-adjacent configuration silently succeeded")
	}
	if err := s.FlushSettings(); err == nil || !s.SettingsDirty {
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
	s.SaveUsedSettings()
	awaitSettingsSignal(t, started)
	draftSettings(s, "superseded")
	s.SaveUsedSettings()
	draftSettings(s, "latest used")
	s.SaveUsedSettings()
	if len(s.saver.pending) != 1 {
		t.Fatal("pending used snapshots are not bounded to the latest one")
	}
	draftSettings(s, "final unused draft")
	flushed := make(chan error, 1)
	go func() { flushed <- s.FlushSettings() }()
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
	if stored := readSavedSettings(t, path); stored.SendText != "final unused draft" || s.SettingsDirty {
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
	s.SaveUsedSettings()
	awaitSettingsSignal(t, notified)
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	close(s.closed)
	s.stopSettingsSaver()
	if s.SettingsDirty {
		t.Fatal("shutdown lost a successful acknowledgement when UI posting stopped")
	}
	if err := s.FlushSettings(); err != nil {
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
	s.Settings.ReceiveHex = true
	s.SettingsChanged()
	if !s.SettingsDirty || len(s.saver.pending) != 0 {
		t.Fatal("text display edit must configure and mark without saving")
	}
	s.Settings.Timestamp = true
	s.SettingsChanged()
	s.SaveUsedSettings()
	completeSettingsSave(t, s, notified)
	stored := readSavedSettings(t, path)
	if s.SettingsDirty || !stored.ReceiveHex || !stored.Timestamp {
		t.Fatal("immediate display use did not persist current settings")
	}
}

func TestUnchangedClearAndPortSettingsDoNotWrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	s, _ := settingsSaveFixture(t, path, config.Save)
	s.ClearSend()
	draft, rates := s.PortSettingsDraft()
	if err := s.ApplyPortSettings(draft, rates); err != nil {
		t.Fatal(err)
	}
	s.SaveUsedSettings()
	err := s.FlushSettings()
	if err != nil {
		t.Fatal(err)
	}
	if s.SettingsDirty {
		t.Fatal("unchanged controls marked settings dirty")
	}
	_, err = os.Stat(path)
	if !os.IsNotExist(err) {
		t.Fatalf("unchanged controls created a configuration: %v", err)
	}
}

func TestShutdownDrainsSettingsBeforeClosingStore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	started := make(chan string, 2)
	release := make(chan struct{})
	closed := make(chan struct{})
	count := 0
	store := &testSettingsStore{
		save: func(path string, settings config.Settings) (*os.File, error) {
			count++
			started <- settings.SendText
			if count == 1 {
				<-release
			}
			return config.SaveWithIdentity(path, settings)
		},
		close: func() error { close(closed); return nil },
	}
	view := &appTestView{callbacks: make(chan func(), 8)}
	s := &App{SettingsPath: path, settingsStore: store, Settings: config.Settings{Language: "en"}}
	s.Startup(view)
	draftSettings(s, "used")
	s.SaveUsedSettings()
	select {
	case got := <-started:
		if got != "used" {
			t.Fatalf("first snapshot = %q", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("background save did not start")
	}
	draftSettings(s, "final")
	done := make(chan error, 1)
	go func() { done <- s.Shutdown() }()
	select {
	case <-closed:
		t.Fatal("store closed before in-flight save completed")
	default:
	}
	close(release)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("shutdown did not drain settings")
	}
	select {
	case got := <-started:
		if got != "final" {
			t.Fatalf("final snapshot = %q", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("final save did not start")
	}
	select {
	case <-closed:
	default:
		t.Fatal("shutdown did not close store")
	}
	if got := readSavedSettings(t, path).SendText; got != "final" {
		t.Fatalf("saved final draft = %q", got)
	}
}

func TestFailedStoreCreationDisablesPersistenceAndOutput(t *testing.T) {
	failure := errors.New("cannot open executable directory")
	s := newAppWithStore(nil, failure)
	if !errors.Is(s.LoadError, failure) || !errors.Is(s.settingsPathError, failure) {
		t.Fatalf("store failure was not retained: %v", s.LoadError)
	}
	s.SettingsDirty = true
	if err := s.Shutdown(); !errors.Is(err, failure) || !s.SettingsDirty {
		t.Fatalf("shutdown did not preserve failed save: %v", err)
	}
	_, _, _, err := s.createProtectedOutput(filepath.Join(t.TempDir(), "log.txt"), "", "", nil)
	if !errors.Is(err, failure) {
		t.Fatalf("output did not fail closed: %v", err)
	}
}
