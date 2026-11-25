package internal

import (
	"os"
	"slices"

	"github.com/fcying/SerialGo/config"
	"github.com/fcying/SerialGo/internal/i18n"
)

type settingsSnapshot struct {
	revision uint64
	settings config.Settings
}

type settingsSaveResult struct {
	revision uint64
	err      error
}

type settingsSaver struct {
	// Only the UI thread accesses request bookkeeping.
	requested      uint64
	pendingRequest bool
	stopped        bool

	// An unrelated error remains visible alongside a save error and is
	// restored if a later save succeeds while we still own the status.
	previousError    Text
	hadPreviousError bool
	saveError        Text

	pending chan *settingsSnapshot
	results chan settingsSaveResult
	done    chan struct{}
}

// SettingsStore saves settings and returns the opened saved file for identity protection.
type SettingsStore interface {
	SaveWithIdentity(string, config.Settings) (*os.File, error)
	OutputIdentity() (os.FileInfo, os.FileInfo, string, error)
	Close() error
}

// The caller holds fileIOMu across both the atomic save and identity pinning.
func (s *App) writeSettings(path string, settings config.Settings) error {
	if s.settingsStoreError != nil {
		return s.settingsStoreError
	}
	if path == "" || s.settingsStore == nil {
		return i18n.NewError("配置不可写, 未保存", "Configuration is not writable; settings were not saved")
	}
	file, err := s.settingsStore.SaveWithIdentity(path, settings)
	if err != nil {
		return err
	}
	s.rememberSavedSettings(file)
	return nil
}

func newSettingsSaver(path string, save func(string, config.Settings) error, notify func()) *settingsSaver {
	a := &settingsSaver{
		pending: make(chan *settingsSnapshot, 1),
		results: make(chan settingsSaveResult, 1),
		done:    make(chan struct{}),
	}
	go func() {
		defer close(a.done)
		for snapshot := range a.pending {
			var err error
			if path == "" {
				err = i18n.NewError("配置不可写, 未保存", "Configuration is not writable; settings were not saved")
			} else {
				err = save(path, snapshot.settings)
			}
			result := settingsSaveResult{revision: snapshot.revision, err: err}
			// Keep the latest acknowledgement even if shutdown drops the posted
			// UI callback. There is one result producer and one UI consumer.
			select {
			case a.results <- result:
			default:
				select {
				case <-a.results:
				default:
				}
				a.results <- result
			}
			notify()
		}
	}()
	return a
}

// The UI is the sole producer. A worker consumption between either select
// cannot block the drain or fill the queue again before the final send.
func (a *settingsSaver) enqueue(snapshot *settingsSnapshot) {
	select {
	case a.pending <- snapshot:
		return
	default:
	}
	select {
	case <-a.pending:
	default:
	}
	a.pending <- snapshot
}

func (s *App) startSettingsSaver() {
	if s.saver != nil {
		return
	}
	var saver *settingsSaver
	saver = newSettingsSaver(s.SettingsPath, func(path string, settings config.Settings) error {
		s.fileIOMu.Lock()
		defer s.fileIOMu.Unlock()
		return s.writeSettings(path, settings)
	}, func() {
		s.dispatch(func() { s.collectSettingsResults(saver) })
	})
	s.saver = saver
}

func (s *App) stopSettingsSaver() {
	a := s.saver
	if a == nil {
		return
	}
	if !a.stopped {
		a.stopped = true
		close(a.pending)
		<-a.done
	}
	// A successful worker write must prevent an unnecessary final write even
	// when its posted UI acknowledgement never ran before the window closed.
	s.collectSettingsResults(a)
}

func (s *App) SettingsChanged() {
	s.SettingsDirty = true
	s.SettingsRevision++
}

func (s *App) ClearSend() {
	value := s.SendValue()
	if *value == "" {
		return
	}
	*value = ""
	s.SettingsChanged()
}

func (s *App) SaveUsedSettings() {
	s.saveUsedSnapshot(s.usedSettingsSnapshot())
}

// Capture the exact preferences accepted by an operation before its I/O starts.
func (s *App) usedSettingsSnapshot() *settingsSnapshot {
	a := s.saver
	if a == nil || a.stopped {
		return nil
	}
	select {
	case <-s.closed:
		return nil
	default:
	}
	s.collectSettingsResults(a)
	if !s.SettingsDirty || a.pendingRequest && a.requested == s.SettingsRevision {
		return nil
	}
	settings := s.Settings
	settings.BaudRates = slices.Clone(settings.BaudRates)
	settings.Presets = slices.Clone(settings.Presets)
	settings.CurveProfiles = config.CloneCurveProfiles(settings.CurveProfiles)
	return &settingsSnapshot{revision: s.SettingsRevision, settings: settings}
}

func (s *App) saveUsedSnapshot(snapshot *settingsSnapshot) {
	a := s.saver
	if snapshot == nil || a == nil || a.stopped {
		return
	}
	select {
	case <-s.closed:
		return
	default:
	}
	s.collectSettingsResults(a)
	// Completion of an older background operation must never supersede a
	// newer accepted write, or clear dirty state for a later draft.
	if snapshot.revision < a.requested || a.pendingRequest && a.requested == snapshot.revision {
		return
	}
	if !s.SettingsDirty && snapshot.revision == s.SettingsRevision {
		return
	}
	a.requested, a.pendingRequest = snapshot.revision, true
	a.enqueue(snapshot)
}

func (s *App) collectSettingsResults(a *settingsSaver) {
	if s.saver != a {
		return
	}
	for {
		select {
		case result := <-a.results:
			if result.revision != a.requested {
				continue
			}
			a.pendingRequest = false
			if result.err == nil && result.revision == s.SettingsRevision {
				s.SettingsDirty = false
			}
			if a.stopped {
				continue
			}
			select {
			case <-s.closed:
				continue
			default:
			}
			if result.err != nil {
				if !s.settingsSaveError {
					a.previousError, a.hadPreviousError = Text{}, s.StatusError
					if s.StatusError {
						a.previousError = s.Status
					}
				}
				a.saveError = Text{
					ZH: "保存参数失败: " + i18n.ErrorText(result.err, false),
					EN: "Failed to save settings: " + i18n.ErrorText(result.err, true),
				}
				s.Status, s.StatusError = a.saveError, true
				if a.hadPreviousError {
					s.Status.ZH += "; " + a.previousError.ZH
					s.Status.EN += "; " + a.previousError.EN
				}
				s.settingsSaveError = true
			} else if s.settingsSaveError {
				s.Status, s.StatusError, s.settingsSaveError = a.previousError, a.hadPreviousError, false
				a.previousError, a.hadPreviousError, a.saveError = Text{}, false, Text{}
			}
		default:
			return
		}
	}
}

// Called after the event loop: serialize the final draft behind every used
// snapshot, and leave an unchanged configuration untouched.
func (s *App) FlushSettings() error {
	s.stopSettingsSaver()
	if !s.SettingsDirty {
		return nil
	}
	revision := s.SettingsRevision
	settings := s.Settings
	settings.BaudRates = slices.Clone(settings.BaudRates)
	settings.Presets = slices.Clone(settings.Presets)
	settings.CurveProfiles = config.CloneCurveProfiles(settings.CurveProfiles)
	s.fileIOMu.Lock()
	err := s.writeSettings(s.SettingsPath, settings)
	s.fileIOMu.Unlock()
	if err != nil {
		return err
	}
	if revision == s.SettingsRevision {
		s.SettingsDirty = false
	}
	return nil
}
