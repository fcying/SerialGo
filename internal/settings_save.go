package internal

import (
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

	pending chan *settingsSnapshot
	results chan settingsSaveResult
	done    chan struct{}
}

func writeSettings(path string, settings config.Settings) error {
	if path == "" {
		return i18n.NewError("配置不可写, 未保存", "Configuration is not writable; settings were not saved")
	}
	return config.Save(path, settings)
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
	saver = newSettingsSaver(s.SettingsPath, config.Save, func() {
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
				if s.StatusError && !s.settingsSaveError {
					continue
				}
				s.Fail("保存参数失败", "Failed to save settings", result.err)
				s.settingsSaveError = true
			} else if s.settingsSaveError {
				s.Status, s.StatusError, s.settingsSaveError = Text{}, false, false
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
	err := writeSettings(s.SettingsPath, s.Settings)
	if err != nil {
		return err
	}
	if revision == s.SettingsRevision {
		s.SettingsDirty = false
	}
	return nil
}
