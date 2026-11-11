package ui

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

func (s *controller) startSettingsSaver() {
	if s.saver != nil {
		return
	}
	window := s.window
	var saver *settingsSaver
	saver = newSettingsSaver(s.path, config.Save, func() {
		// Update posts asynchronously; shutdown never waits on the UI loop.
		window.Update(func() { s.collectSettingsResults(saver) })
	})
	s.saver = saver
}

func (s *controller) stopSettingsSaver() {
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

func (s *controller) settingsChanged() {
	s.settingsDirty = true
	s.settingsRevision++
}

func (s *controller) displaySettingsChanged() {
	s.configureLog()
	s.settingsChanged()
}

func (s *controller) applyDisplaySettings() {
	s.displaySettingsChanged()
	s.saveUsedSettings()
}

func (s *controller) clearSend() {
	value := s.sendValue()
	if *value == "" {
		return
	}
	*value = ""
	s.settingsChanged()
}

func (s *controller) saveUsedSettings() {
	a := s.saver
	if a == nil || a.stopped {
		return
	}
	select {
	case <-s.closed:
		return
	default:
	}
	s.collectSettingsResults(a)
	if !s.settingsDirty || a.pendingRequest && a.requested == s.settingsRevision {
		return
	}
	a.requested, a.pendingRequest = s.settingsRevision, true
	// Lists must not share backing arrays with later edits.
	settings := s.settings
	settings.BaudRates = slices.Clone(s.settings.BaudRates)
	settings.Presets = slices.Clone(s.settings.Presets)
	a.enqueue(&settingsSnapshot{revision: s.settingsRevision, settings: settings})
}

func (s *controller) collectSettingsResults(a *settingsSaver) {
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
			if result.err == nil && result.revision == s.settingsRevision {
				s.settingsDirty = false
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
				if s.statusError && !s.settingsSaveError {
					continue
				}
				s.fail("保存参数失败", "Failed to save settings", result.err)
				s.settingsSaveError = true
			} else if s.settingsSaveError {
				s.status, s.statusError, s.settingsSaveError = localizedText{}, false, false
			}
		default:
			return
		}
	}
}

// Called after the event loop: serialize the final draft behind every used
// snapshot, and leave an unchanged configuration untouched.
func (s *controller) flushSettings() error {
	s.stopSettingsSaver()
	if !s.settingsDirty {
		return nil
	}
	revision := s.settingsRevision
	err := writeSettings(s.path, s.settings)
	if err != nil {
		return err
	}
	if revision == s.settingsRevision {
		s.settingsDirty = false
	}
	return nil
}
