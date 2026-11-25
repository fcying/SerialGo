package internal

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/fcying/SerialGo/communication/serialport"
	"github.com/fcying/SerialGo/config"
	"github.com/fcying/SerialGo/internal/i18n"
)

const (
	eventPumpInterval        = 10 * time.Millisecond
	fileSendProgressInterval = 200 * time.Millisecond
)

// View confines all application state and display updates to the frontend thread.
// Dispatch must post asynchronously and may discard callbacks after shutdown.
type View interface {
	Dispatch(func())
	AppendEvents([]Event)
}

type Text struct{ ZH, EN string }

type FileSendProgress struct {
	Name       string
	SentBytes  uint64
	TotalBytes uint64
	Status     Text
	Visible    bool
	Active     bool
	Error      bool
}

type captureTarget struct {
	path   string
	opened <-chan struct{}
	info   os.FileInfo
	file   interface{ Stat() (os.FileInfo, error) }
	state  atomic.Uint32 // 0: pending, 1: truncation committed, 2: canceled
}

type terminalError struct {
	err error
}

type sendResult struct {
	done    chan struct{}
	err     error
	label   Text
	file    bool
	failure *terminalError
}

// App owns serial operations, operation state and persisted preferences.
// Public state and commands are accessed only on the frontend thread.
type App struct {
	Settings              config.Settings
	SettingsPath          string
	LoadError             error
	SettingsDirty         bool
	SettingsRevision      uint64
	Status                Text
	StatusError           bool
	Tx, Rx                uint64
	FilePath, CapturePath string
	Repeat, LoopPresets   bool
	PortLabels            []string
	PortChoice            string
	PortBusy              bool
	RefreshingPorts       bool
	FileBusy              bool
	FileProgress          FileSendProgress

	view                   View
	ports                  []serialport.Info
	session                *Session
	closingSessions        map[*Session]struct{}
	stopPump               chan struct{}
	closed                 chan struct{}
	closing                bool
	jobs                   sync.WaitGroup
	pendingPortMu          sync.Mutex
	pendingPort            *serialport.Port
	captureMu              sync.Mutex
	fileIOMu               sync.Mutex
	captureTargets         map[*Session]*captureTarget
	terminalMu             sync.Mutex
	terminalErrors         map[*terminalError]struct{}
	protectedSettingsPath  string
	initialSettingsFile    *os.File
	savedSettingsFile      *os.File
	settingsStore          SettingsStore
	settingsStoreError     error
	settingsPathError      error
	saver                  *settingsSaver
	settingsSaveError      bool
	cancelSend             context.CancelFunc
	sendDone               *sendResult
	sendingFilePath        string
	sendingFileInfo        os.FileInfo
	sendOpenDone           <-chan struct{}
	fileProgressUpdatedAt  time.Time
	fileProgressGeneration uint64
	loadErrorReported      bool
	settingsWarning        Text
	curve                  curveEngine
}

func NewApp() *App {
	return newAppWithStore(config.OpenExecutableStore())
}

// NewAppWithSettingsStore constructs an app from already-loaded settings and an
// explicitly supplied store. The caller is responsible for its path policy.
// Shutdown closes the store after all pending settings writes have completed.
func NewAppWithSettingsStore(settings config.Settings, path string, store SettingsStore) *App {
	return &App{Settings: settings, SettingsPath: path, protectedSettingsPath: path, settingsStore: store}
}

func newAppWithStore(store *config.Store, err error) *App {
	if err != nil {
		s := newAppFromIdentity(config.DefaultSettings(), "", err, os.Executable, nil, true)
		s.settingsStoreError = err
		s.settingsPathError = err
		return s
	}
	settings, path, file, err := store.LoadWithIdentity()
	s := newAppFromIdentity(settings, path, err, os.Executable, file, true)
	s.settingsStore = store
	return s
}

func newAppFromLoad(settings config.Settings, path string, loadErr error, executable func() (string, error)) *App {
	return newAppFromIdentity(settings, path, loadErr, executable, nil, false)
}

func newAppFromIdentity(settings config.Settings, path string, loadErr error, executable func() (string, error), file *os.File, trusted bool) *App {
	protectedPath := path
	var pathErr error
	if protectedPath == "" {
		var exe string
		exe, pathErr = executable()
		if pathErr == nil {
			exe, pathErr = filepath.EvalSymlinks(exe)
		}
		if pathErr == nil {
			protectedPath = filepath.Join(filepath.Dir(exe), "config.toml")
		}
	}
	warning := normalizeStartupSettings(&settings)
	s := &App{Settings: settings, SettingsPath: path, LoadError: loadErr, settingsWarning: warning, protectedSettingsPath: protectedPath, settingsPathError: pathErr}
	s.initialSettingsFile = file
	if !trusted && pathErr == nil && protectedPath != "" {
		s.initialSettingsFile, pathErr = os.Open(protectedPath)
		if pathErr != nil && !errors.Is(pathErr, os.ErrNotExist) {
			s.settingsPathError = pathErr
		}
	}
	initialPort, initialRates := settings.Port, settings.BaudRates
	s.InitializeBaudRates()
	if s.Settings.Port != initialPort || !slices.Equal(s.Settings.BaudRates, initialRates) {
		s.SettingsChanged()
	}
	return s
}

func normalizeStartupSettings(settings *config.Settings) Text {
	var warning Text
	if settings.Encoding != "UTF-8" && settings.Encoding != "GB18030" {
		settings.Encoding = "UTF-8"
		warning.ZH = "编码回退 UTF-8"
		warning.EN = "Encoding reset to UTF-8"
	}
	if _, err := Milliseconds(settings.PacketGap, "分包超时", 1); err != nil {
		settings.PacketGap = "20"
		if warning.ZH != "" {
			warning.ZH += "; "
			warning.EN += "; "
		}
		warning.ZH += "分包超时回退 20 ms"
		warning.EN += "packet gap reset to 20 ms"
	}
	return warning
}

func (s *App) Startup(view View) {
	s.view = view
	if s.closed == nil {
		s.closed = make(chan struct{})
	}
	s.startSettingsSaver()
}

// Shutdown runs after the frontend event loop has stopped accepting work.
func (s *App) Shutdown() error {
	if !s.closing {
		s.closing = true
		if s.closed != nil {
			close(s.closed)
		}
	}
	s.StopSending()
	var shutdownErr error
	if s.session != nil {
		p := s.session
		target := s.protectCapture(p, s.CapturePath, nil)
		target.cancel()
		if target != nil && target.opened != nil {
			<-target.opened
		}
		s.session = nil
		close(s.stopPump)
		shutdownErr = errors.Join(shutdownErr, p.Close())
		s.curveDisconnected()
	}
	s.jobs.Wait()
	s.closingSessions = nil
	// Workers retain terminal errors before posting their optional UI callbacks.
	// Only callbacks that actually report an error acknowledge it.
	s.terminalMu.Lock()
	for result := range s.terminalErrors {
		shutdownErr = errors.Join(shutdownErr, result.err)
	}
	s.terminalErrors = nil
	s.terminalMu.Unlock()
	s.captureMu.Lock()
	s.captureTargets = nil
	s.captureMu.Unlock()
	s.pendingPortMu.Lock()
	pendingPort := s.pendingPort
	s.pendingPort = nil
	s.pendingPortMu.Unlock()
	if pendingPort != nil {
		_ = pendingPort.Close()
	}
	shutdownErr = errors.Join(shutdownErr, s.FlushSettings())
	if s.initialSettingsFile != nil {
		shutdownErr = errors.Join(shutdownErr, s.initialSettingsFile.Close())
		s.initialSettingsFile = nil
	}
	if s.savedSettingsFile != nil {
		shutdownErr = errors.Join(shutdownErr, s.savedSettingsFile.Close())
		s.savedSettingsFile = nil
	}
	if s.settingsStore != nil {
		shutdownErr = errors.Join(shutdownErr, s.settingsStore.Close())
		s.settingsStore = nil
	}
	return shutdownErr
}

func (s *App) retainTerminalError(err error) *terminalError {
	if err == nil {
		return nil
	}
	result := &terminalError{err: err}
	s.terminalMu.Lock()
	if s.terminalErrors == nil {
		s.terminalErrors = make(map[*terminalError]struct{})
	}
	s.terminalErrors[result] = struct{}{}
	s.terminalMu.Unlock()
	return result
}

func (s *App) acknowledgeTerminalError(result *terminalError) {
	s.terminalMu.Lock()
	delete(s.terminalErrors, result)
	s.terminalMu.Unlock()
}

// Remove cancellation without discarding independent errors joined with it.
func captureOpenError(err error) error {
	if err == context.Canceled {
		return nil
	}
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		var result error
		for _, part := range joined.Unwrap() {
			result = errors.Join(result, captureOpenError(part))
		}
		return result
	}
	return err
}

// Capture reservations outlive CapturePath and are released only after the file
// worker has closed. A new open gets its own token so stale completions cannot
// release a later capture's destination.
func (s *App) protectCapture(p *Session, path string, opened <-chan struct{}) *captureTarget {
	s.captureMu.Lock()
	defer s.captureMu.Unlock()
	target := s.captureTargets[p]
	if opened != nil || target == nil && path != "" {
		target = &captureTarget{path: path, opened: opened}
		p.captureMu.Lock()
		if p.captureWorker != nil {
			target.file, _ = p.captureWorker.file.(interface{ Stat() (os.FileInfo, error) })
		}
		p.captureMu.Unlock()
		if s.captureTargets == nil {
			s.captureTargets = make(map[*Session]*captureTarget)
		}
		s.captureTargets[p] = target
	}
	return target
}

func (t *captureTarget) cancel() {
	if t == nil {
		return
	}
	t.state.CompareAndSwap(0, 2)
}

// Failure events can outlive a stopped capture and must not clear a replacement.
func (s *App) failedCaptureIsCurrent(p *Session, worker *captureWriter) bool {
	if worker == nil {
		return true
	}
	s.captureMu.Lock()
	target := s.captureTargets[p]
	s.captureMu.Unlock()
	if target != nil && target.opened != nil {
		select {
		case <-target.opened:
		default:
			return false
		}
	}
	p.captureMu.Lock()
	current := p.captureWorker == worker
	p.captureMu.Unlock()
	return current
}

func (s *App) releaseCapture(p *Session, target *captureTarget) {
	s.captureMu.Lock()
	if s.captureTargets[p] == target {
		delete(s.captureTargets, p)
	}
	s.captureMu.Unlock()
}

func (s *App) capturePaths() []string {
	s.captureMu.Lock()
	defer s.captureMu.Unlock()
	paths := make([]string, 0, len(s.captureTargets)+1)
	if s.CapturePath != "" {
		paths = append(paths, s.CapturePath)
	}
	for _, target := range s.captureTargets {
		paths = append(paths, target.path)
	}
	return paths
}

func (s *App) Connected() bool { return s.session != nil }
func (s *App) Sending() bool   { return s.cancelSend != nil }
func (s *App) dispatch(fn func()) {
	if s.view != nil {
		s.view.Dispatch(func() {
			if !s.closing {
				fn()
			}
		})
	}
}

func (s *App) Fail(zh, en string, err error) {
	operation := Text{
		ZH: zh + ": " + i18n.ErrorText(err, false),
		EN: en + ": " + i18n.ErrorText(err, true),
	}
	if s.settingsSaveError && s.saver != nil {
		s.saver.previousError = operation
		s.saver.hadPreviousError = true
		s.Status = Text{ZH: s.saver.saveError.ZH + "; " + operation.ZH, EN: s.saver.saveError.EN + "; " + operation.EN}
	} else {
		s.Status = operation
	}
	s.StatusError = true
}

func (s *App) Note(zh, en string) {
	if s.settingsSaveError {
		return
	}
	s.Status, s.StatusError = Text{ZH: zh, EN: en}, false
}

// Preserve both the visible status and the operation error underneath a pending
// settings-save failure, so a later successful save cannot erase this failure.
func (s *App) appendOperationError(zh, en string, err error) {
	previous := s.Status
	if s.settingsSaveError && s.saver != nil {
		previous = Text{}
		if s.saver.hadPreviousError {
			previous = s.saver.previousError
		}
	}
	operation := Text{ZH: zh + ": " + i18n.ErrorText(err, false), EN: en + ": " + i18n.ErrorText(err, true)}
	if previous.ZH != "" || previous.EN != "" {
		operation = Text{ZH: previous.ZH + "; " + operation.ZH, EN: previous.EN + "; " + operation.EN}
	}
	if s.settingsSaveError && s.saver != nil {
		s.saver.previousError = operation
		s.saver.hadPreviousError = true
		s.Status = Text{ZH: s.saver.saveError.ZH + "; " + operation.ZH, EN: s.saver.saveError.EN + "; " + operation.EN}
	} else {
		s.Status = operation
	}
	s.StatusError = true
}

func (s *App) showSettingsWarning() {
	if s.settingsWarning.ZH == "" && s.settingsWarning.EN == "" || s.StatusError {
		return
	}
	s.Status = s.settingsWarning
	s.StatusError = false
	s.settingsWarning = Text{}
}

// ClearReceiveLog resets the counters and discards pending display records.
func (s *App) ClearReceiveLog() {
	if s.session != nil {
		s.session.clearDisplay()
	}
	// Detached sessions keep their pending data until the final frontend drain.
	for p := range s.closingSessions {
		p.clearDisplay()
	}
	s.Tx, s.Rx = 0, 0
}

func (s *App) SendValue() *string {
	if s.Settings.SendHex {
		return &s.Settings.SendHexText
	}
	return &s.Settings.SendText
}

func (s *App) CanSendFile() bool {
	return s.CanSend() && !s.FileBusy && s.sendingFilePath == "" && s.FilePath != ""
}

func (s *App) SetPorts(ports []serialport.Info) {
	previous := s.Settings.Port.Name
	s.ports = ports
	clear(s.PortLabels)
	s.PortLabels = slices.Grow(s.PortLabels[:0], len(ports))
	selected := -1
	for i, port := range ports {
		label := port.Name
		if port.Description != "" {
			label += " " + port.Description
		}
		s.PortLabels = append(s.PortLabels, label)
		if port.Name == previous {
			selected = i
		}
	}
	if selected < 0 && len(ports) > 0 {
		selected = 0
	}
	s.Settings.Port.Name, s.PortChoice = "", ""
	if selected >= 0 {
		s.Settings.Port.Name = ports[selected].Name
		s.PortChoice = s.PortLabels[selected]
	}
	if s.Settings.Port.Name != previous {
		s.SettingsChanged()
	}
}

func (s *App) SelectPort() {
	if s.Connected() || s.PortBusy && !s.RefreshingPorts {
		return
	}
	for i, label := range s.PortLabels {
		if label != s.PortChoice {
			continue
		}
		name := s.ports[i].Name
		if name != s.Settings.Port.Name {
			s.Settings.Port.Name = name
			s.SettingsChanged()
		}
		return
	}
}

func (s *App) pump(p *Session, stop <-chan struct{}) {
	var dispatchPending atomic.Bool
	ticker := time.NewTicker(eventPumpInterval)
	defer ticker.Stop()
	for {
		select {
		case <-s.closed:
			return
		case <-stop:
			return
		case now := <-ticker.C:
			if len(p.Events()) == 0 && !p.hasDisplay() && !p.fileSendProgressDue(now) {
				continue
			}
			if !dispatchPending.CompareAndSwap(false, true) {
				continue
			}
			s.dispatch(func() {
				defer dispatchPending.Store(false)
				if s.session == p {
					s.drain(p)
				}
			})
		}
	}
}

func (s *App) drain(p *Session) {
	s.deliverDisplay(p)
	ch := p.Events()
	var readErr, captureErr error
	var captureWorkers []*captureWriter
	captureStopped := false
	// Process a snapshot so an active producer cannot monopolize the UI.
	for range len(ch) {
		event := <-ch
		if event.Err == nil {
			continue
		}
		if !event.ReadFailed {
			captureErr = errors.Join(captureErr, event.Err)
			captureWorkers = append(captureWorkers, event.capture)
			current := s.failedCaptureIsCurrent(p, event.capture)
			captureStopped = captureStopped || current
		} else {
			readErr = errors.Join(readErr, event.Err)
		}
	}
	if captureErr != nil {
		if captureStopped {
			s.releaseCapture(p, s.protectCapture(p, "", nil))
			s.CapturePath = ""
		}
		if readErr == nil {
			s.Fail("已停止接收存文件", "Receive-to-file stopped", captureErr)
		}
		for _, worker := range captureWorkers {
			p.acknowledgeCaptureError(worker)
		}
	}
	if readErr != nil {
		s.disconnect(errors.Join(readErr, captureErr))
		s.Fail("串口已断开", "Serial port disconnected", errors.Join(readErr, captureErr))
	}
}

// deliverDisplay is also used after Close and the sending job have both settled.
// It must not process old errors or initiate another disconnect.
func (s *App) deliverDisplay(p *Session) {
	records, rx, tx := p.drainDisplay()
	s.Rx += rx
	s.Tx += tx
	if s.FileProgress.Active && p == s.session {
		now := time.Now()
		if now.Sub(s.fileProgressUpdatedAt) >= fileSendProgressInterval {
			s.FileProgress.SentBytes = p.takeFileSendProgress()
			s.fileProgressUpdatedAt = now
		}
	}
	if s.view != nil {
		s.view.AppendEvents(records)
	}
}

func (s *App) CanSend() bool { return s.session != nil && s.cancelSend == nil }

func (s *App) startSending(label Text, filePath string, send func(context.Context, *Session) error) {
	if !s.CanSend() || (filePath != "" && (s.FileBusy || s.sendingFilePath != "")) {
		return
	}
	p := s.session
	ctx, cancel := context.WithCancel(context.Background())
	done := &sendResult{done: make(chan struct{}), label: label, file: filePath != ""}
	s.sendDone = done
	s.cancelSend = cancel
	if filePath != "" {
		s.sendingFilePath = filePath
		s.captureMu.Lock()
		s.sendingFileInfo = nil
		s.captureMu.Unlock()
		s.FileProgress = FileSendProgress{
			Name:    filepath.Base(filePath),
			Status:  Text{ZH: "发送中", EN: "Sending"},
			Visible: true,
			Active:  true,
		}
		s.fileProgressUpdatedAt = time.Now()
		p.beginFileSendProgress()
	}
	s.Note(label.ZH+"中", label.EN+" in progress")
	s.SaveUsedSettings()
	s.jobs.Add(1)
	go func() {
		defer s.jobs.Done()
		done.err = send(ctx, p)
		if done.err != nil && !errors.Is(done.err, context.Canceled) {
			done.failure = s.retainTerminalError(i18n.WrapError(label.ZH+"失败", label.EN+" failed", done.err))
		}
		cancel()
		close(done.done)
		s.dispatch(func() {
			if s.sendDone != done {
				return
			}
			if s.session != p {
				return
			}
			s.cancelSend = nil
			s.drain(p)
			if s.session != p {
				return
			}
			s.sendDone = nil
			err := done.err
			switch {
			case errors.Is(err, context.Canceled):
				s.Note("已停止发送", "Sending stopped")
				if filePath != "" {
					s.finishFileSendProgress(p, true, Text{ZH: "已取消", EN: "Cancelled"}, false)
				}
			case err != nil:
				s.Fail(label.ZH+"失败", label.EN+" failed", err)
				s.acknowledgeTerminalError(done.failure)
				if filePath != "" {
					s.finishFileSendProgress(p, true, s.Status, true)
				}
			default:
				s.Note(label.ZH+"完成", label.EN+" completed")
				if filePath != "" {
					s.finishFileSendProgress(p, false, s.Status, false)
				}
			}
		})
	}()
}

func (s *App) finishFileSendProgress(p *Session, visible bool, status Text, failed bool) {
	s.FileProgress.SentBytes = p.fileSendProgressBytes()
	s.FileProgress.Visible = visible
	s.FileProgress.Active = false
	s.FileProgress.Status = status
	s.FileProgress.Error = failed
	p.endFileSendProgress()
	s.sendingFilePath = ""
}

func (s *App) StopSending() {
	if s.cancelSend != nil {
		s.cancelSend()
	}
}

func (s *App) format(hex, crlf bool) Format {
	return Format{Hex: hex, CRLF: crlf, Encoding: s.Settings.Encoding}
}

func (s *App) encodeSendData(input string, hex, crlf bool) ([]byte, error) {
	format := s.format(hex, crlf)
	data, err := encodePayload(input, format, s.Settings.ParseSendEscapes)
	if err != nil {
		return nil, err
	}
	if err := validateSendData(data, format.CRLF); err != nil {
		return nil, err
	}
	data, err = appendChecksum(data, s.Settings.ChecksumAlgorithm, s.Settings.ChecksumHeaderBytes, s.Settings.ChecksumTailBytes)
	if err != nil {
		return nil, err
	}
	if format.CRLF {
		data = append(data, '\r', '\n')
	}
	return data, nil
}

func (s *App) SendText(input string) {
	if !s.CanSend() {
		return
	}
	p := &s.Settings
	data, err := s.encodeSendData(input, p.SendHex, p.CRLF)
	if err != nil {
		s.Fail("发送失败", "Send failed", err)
		return
	}
	var delay time.Duration
	if s.Repeat {
		delay, err = Milliseconds(p.Interval, "定时发送间隔", 1)
		if err != nil {
			s.Fail("发送失败", "Send failed", err)
			return
		}
	}
	s.startSending(Text{ZH: "发送", EN: "Send"}, "", func(ctx context.Context, port *Session) error {
		for {
			err := port.Send(ctx, data)
			if err != nil || delay == 0 {
				return err
			}
			err = Wait(ctx, delay)
			if err != nil {
				return err
			}
		}
	})
}

func (s *App) SendPresets(index int) {
	if !s.CanSend() {
		return
	}
	type item struct {
		data  []byte
		delay time.Duration
	}
	var items []item
	loop := index < 0 && s.LoopPresets
	for i, preset := range s.Settings.Presets {
		if index >= 0 && i != index || index < 0 && !preset.Enabled {
			continue
		}
		data, err := s.encodeSendData(preset.Data, preset.Hex, preset.CRLF)
		if err != nil {
			s.Fail(fmt.Sprintf("第 %d 条发送失败", i+1), fmt.Sprintf("Failed to send preset %d", i+1), err)
			return
		}
		var delay time.Duration
		if index < 0 {
			delay, err = Milliseconds(preset.Interval, "多条发送间隔", 1)
			if err != nil {
				s.Fail(fmt.Sprintf("第 %d 条发送失败", i+1), fmt.Sprintf("Failed to send preset %d", i+1), err)
				return
			}
		}
		items = append(items, item{data, delay})
	}
	if len(items) == 0 {
		s.Note("请先勾选要发送的条目", "Select the presets to send first")
		return
	}
	s.startSending(Text{ZH: "多条发送", EN: "Preset send"}, "", func(ctx context.Context, port *Session) error {
		for {
			for i, item := range items {
				err := port.Send(ctx, item.data)
				if err != nil {
					return err
				}
				if loop || i < len(items)-1 {
					err = Wait(ctx, item.delay)
					if err != nil {
						return err
					}
				}
			}
			if !loop {
				return nil
			}
		}
	})
}

func (s *App) SendFile() {
	path := s.FilePath
	delay, err := Milliseconds(s.Settings.FileDelay, "文件分段延时", 0)
	if err != nil {
		s.Fail("发送文件失败", "File send failed", err)
		return
	}
	if !s.CanSendFile() {
		return
	}
	s.fileProgressGeneration++
	opened := make(chan struct{})
	s.sendOpenDone = opened
	progressGeneration := s.fileProgressGeneration
	s.startSending(Text{ZH: "发送文件", EN: "File send"}, path, func(ctx context.Context, p *Session) error {
		s.fileIOMu.Lock()
		file, err := os.OpenFile(path, os.O_RDONLY|outputNonblock, 0)
		if err == nil {
			var info os.FileInfo
			info, err = file.Stat()
			s.captureMu.Lock()
			s.sendingFileInfo = info
			s.captureMu.Unlock()
		}
		s.fileIOMu.Unlock()
		close(opened)
		if err != nil {
			if file != nil {
				_ = file.Close()
			}
			return err
		}
		defer file.Close()
		return sendOpenedFileWithProgress(ctx, file, delay, p, func(total int64) {
			s.dispatch(func() {
				if s.fileProgressGeneration != progressGeneration || !s.FileProgress.Active {
					return
				}
				s.FileProgress.TotalBytes = uint64(total)
			})
		})
	})
}

func (s *App) RefreshPorts() {
	if s.Connected() || s.PortBusy || s.closing {
		return
	}
	s.PortBusy = true
	s.RefreshingPorts = true
	s.jobs.Add(1)
	go func() {
		defer s.jobs.Done()
		ports, err := serialport.List()
		s.dispatch(func() { s.applyPortRefresh(ports, err) })
	}()
}

func (s *App) applyPortRefresh(ports []serialport.Info, err error) {
	s.PortBusy = false
	s.RefreshingPorts = false
	reportLoadError := !s.loadErrorReported && s.LoadError != nil
	s.loadErrorReported = true
	if err != nil {
		if reportLoadError {
			s.Fail("读取参数及刷新串口失败", "Failed to load settings and refresh serial ports", errors.Join(s.LoadError, err))
		} else {
			s.Fail("刷新串口失败", "Failed to refresh serial ports", err)
		}
		s.showSettingsWarning()
		return
	}
	s.SetPorts(ports)
	if len(ports) == 0 {
		s.Note("未找到串口", "No serial ports found")
	} else {
		s.Note(fmt.Sprintf("找到 %d 个串口", len(ports)), fmt.Sprintf("Found %d serial ports", len(ports)))
	}
	if reportLoadError {
		s.Fail("读取参数失败", "Failed to load settings", s.LoadError)
	}
	s.showSettingsWarning()
}

func (s *App) TogglePort() {
	if s.PortBusy || s.closing {
		return
	}
	if s.session != nil {
		s.drain(s.session)
		if s.session != nil {
			s.disconnect(nil)
		}
		return
	}
	if !slices.ContainsFunc(s.ports, func(p serialport.Info) bool { return p.Name == s.Settings.Port.Name }) {
		s.Note("请先选择可用串口", "Select an available serial port first")
		return
	}
	settings := s.Settings.Port
	snapshot := s.usedSettingsSnapshot()
	s.PortBusy = true
	s.jobs.Add(1)
	go func() {
		defer s.jobs.Done()
		port, err := serialport.Open(settings)
		// Keep ownership until the frontend accepts the result. Shutdown
		// also reclaims this port when the posted callback is discarded.
		s.pendingPortMu.Lock()
		s.pendingPort = port
		s.pendingPortMu.Unlock()
		s.dispatch(func() {
			s.pendingPortMu.Lock()
			s.pendingPort = nil
			s.pendingPortMu.Unlock()
			s.PortBusy = false
			if err != nil {
				s.Fail("打开串口失败", "Failed to open serial port", err)
				return
			}
			baud, _ := strconv.Atoi(strings.TrimSpace(settings.Baud))
			p := newSession(port, baud, s.curveReceiver())
			s.session, s.stopPump = p, make(chan struct{})
			s.Note("串口已打开", "Serial port opened")
			s.saveUsedSnapshot(snapshot)
			go s.pump(p, s.stopPump)
		})
	}()
}

func (s *App) disconnect(readErr error) {
	p := s.session
	if p == nil {
		return
	}
	target := s.protectCapture(p, s.CapturePath, nil)
	target.cancel()
	sendDone := s.sendDone
	if s.closingSessions == nil {
		s.closingSessions = make(map[*Session]struct{})
	}
	s.closingSessions[p] = struct{}{}
	s.session = nil
	close(s.stopPump)
	s.StopSending()
	s.cancelSend = nil
	s.sendDone = nil
	s.CapturePath = ""
	s.PortBusy = true
	status, statusError := s.Status, s.StatusError
	s.jobs.Add(1)
	go func() {
		defer s.jobs.Done()
		if target != nil && target.opened != nil {
			<-target.opened
		}
		err := p.Close()
		s.curveDisconnected()
		if sendDone != nil {
			<-sendDone.done
		}
		var sendFailure *terminalError
		if sendDone != nil {
			sendFailure = sendDone.failure
		}
		s.releaseCapture(p, target)
		result := s.retainTerminalError(err)
		s.dispatch(func() {
			s.deliverDisplay(p)
			if s.view != nil {
				s.view.AppendEvents([]Event{{SessionEnd: true}})
			}
			delete(s.closingSessions, p)
			if s.session != nil {
				return
			}
			if s.FileProgress.Active {
				switch {
				case sendDone != nil && sendDone.err == nil:
					s.finishFileSendProgress(p, false, Text{ZH: "发送文件完成", EN: "File send completed"}, false)
				case readErr != nil:
					status := Text{
						ZH: "串口已断开: " + i18n.ErrorText(readErr, false),
						EN: "Serial port disconnected: " + i18n.ErrorText(readErr, true),
					}
					s.finishFileSendProgress(p, true, status, true)
				case sendDone != nil && !errors.Is(sendDone.err, context.Canceled):
					status := Text{
						ZH: "发送文件失败: " + i18n.ErrorText(sendDone.err, false),
						EN: "File send failed: " + i18n.ErrorText(sendDone.err, true),
					}
					s.finishFileSendProgress(p, true, status, true)
				default:
					s.finishFileSendProgress(p, true, Text{ZH: "已取消", EN: "Cancelled"}, false)
				}
			}
			s.sendingFilePath = ""
			s.PortBusy = false
			if sendFailure != nil {
				failure := sendFailure.err
				if readErr != nil {
					failure = errors.Join(failure, i18n.WrapError("串口已断开", "Serial port disconnected", readErr))
				}
				if err != nil {
					failure = errors.Join(failure, i18n.WrapError("关闭串口失败", "Failed to close serial port", err))
					s.acknowledgeTerminalError(result)
				}
				if s.Status != status || s.StatusError != statusError {
					s.appendOperationError("发送失败", "Send failed", failure)
				} else {
					s.Fail("发送失败", "Send failed", failure)
				}
				s.acknowledgeTerminalError(sendFailure)
				return
			}
			if s.Status != status || s.StatusError != statusError {
				if err != nil {
					s.appendOperationError("关闭串口失败", "Failed to close serial port", err)
					s.acknowledgeTerminalError(result)
				}
				return
			}
			if err != nil {
				s.Fail("关闭串口失败", "Failed to close serial port", err)
				s.acknowledgeTerminalError(result)
			} else {
				s.Note("串口已关闭", "Serial port closed")
			}
		})
	}()
}

func (s *App) SelectFile(path string) {
	if !s.FileProgress.Active {
		s.FileProgress = FileSendProgress{}
	}
	s.FilePath = path
	s.Note("待发送文件已选择", "File selected for sending")
}

func (s *App) SaveLog(path, text string) {
	if s.FileBusy || s.closing {
		return
	}
	sendPath := s.sendingFilePath
	capturePath := s.CapturePath
	sendOpened := s.sendOpenDone
	snapshot := s.usedSettingsSnapshot()
	s.FileBusy = true
	s.jobs.Add(1)
	go func() {
		defer s.jobs.Done()
		if sendPath != "" && sendOpened != nil {
			<-sendOpened
		}
		file, sameSendSource, sameCapture, err := s.createProtectedOutput(path, sendPath, capturePath, nil)
		if err == nil && file != nil {
			err = writeAndCloseLog(file, text)
		}
		s.completeLogSave(err, sameSendSource, sameCapture, snapshot)
	}()
}

func (s *App) completeLogSave(err error, sameSendSource, sameCapture bool, snapshot *settingsSnapshot) {
	var result *terminalError
	if err != nil {
		result = s.retainTerminalError(i18n.WrapError("保存显示数据失败", "Failed to save displayed data", err))
	}
	s.dispatch(func() {
		s.FileBusy = false
		if err != nil {
			s.Fail("保存显示数据失败", "Failed to save displayed data", err)
			s.acknowledgeTerminalError(result)
			return
		}
		if sameSendSource {
			s.Note("请勿覆盖正在发送的文件", "Do not overwrite the file currently being sent")
			return
		}
		if sameCapture {
			s.Note("请勿覆盖正在接收的文件", "Do not overwrite the active receive file")
			return
		}
		s.Note("显示数据已保存", "Displayed data saved")
		s.saveUsedSnapshot(snapshot)
	})
}

func writeAndCloseLog(file io.WriteCloser, text string) error {
	_, err := io.WriteString(file, text)
	return errors.Join(err, file.Close())
}

// fileIOMu must be held. Retain the actual save descriptor, so an external
// replacement cannot hide its identity or allow its inode to be reused.
func (s *App) rememberSavedSettings(file *os.File) {
	if s.savedSettingsFile != nil {
		_ = s.savedSettingsFile.Close()
	}
	s.savedSettingsFile = file
}

// The mutex serializes source opening and destination validation/truncation.
// Comparisons use the opened descriptors, not paths that can be replaced.
func (s *App) createProtectedOutput(path, sendPath, capturePath string, own *captureTarget) (*os.File, bool, bool, error) {
	s.fileIOMu.Lock()
	defer s.fileIOMu.Unlock()
	if own != nil && own.state.Load() == 2 {
		return nil, false, false, context.Canceled
	}
	if s.settingsPathError != nil {
		return nil, false, false, s.settingsPathError
	}
	settingsPath := s.protectedSettingsPath
	if settingsPath == "" {
		settingsPath = s.SettingsPath
	}
	var settingsDir, currentSettings os.FileInfo
	var settingsName string
	if s.settingsStore != nil {
		var identityErr error
		settingsDir, currentSettings, settingsName, identityErr = s.settingsStore.OutputIdentity()
		if identityErr != nil {
			return nil, false, false, identityErr
		}
		if settingsDir != nil {
			settingsPath = ""
		}
	}
	outputPath, err := resolvedOutputPath(path)
	if err != nil {
		return nil, false, false, err
	}
	configPath := ""
	if settingsPath != "" {
		configPath, err = resolvedOutputPath(settingsPath)
		if err != nil {
			return nil, false, false, err
		}
		if sameOutputName(outputPath, configPath) {
			return nil, false, false, i18n.NewError("不能覆盖配置文件", "Cannot overwrite the configuration file")
		}
	}
	// Never create through the supplied path: a dangling link could be changed
	// after validation to point at a missing configuration file.
	file, err := os.OpenFile(path, os.O_WRONLY|outputNonblock, 0)
	if errors.Is(err, os.ErrNotExist) {
		parent, openErr := os.OpenRoot(filepath.Dir(outputPath))
		if openErr != nil {
			return nil, false, false, openErr
		}
		defer parent.Close()
		if settingsDir != nil && (sameOutputName(filepath.Base(outputPath), "config.toml") || sameOutputName(filepath.Base(outputPath), settingsName)) {
			outputDir, statErr := parent.Stat(".")
			if statErr != nil {
				return nil, false, false, statErr
			}
			if os.SameFile(outputDir, settingsDir) {
				return nil, false, false, i18n.NewError("不能覆盖配置文件", "Cannot overwrite the configuration file")
			}
		}
		if configPath != "" && sameOutputName(filepath.Base(outputPath), filepath.Base(configPath)) {
			outputDir, outputErr := parent.Stat(".")
			configDir, configErr := os.Stat(filepath.Dir(configPath))
			if outputErr != nil || configErr != nil {
				return nil, false, false, errors.Join(outputErr, configErr)
			}
			if os.SameFile(outputDir, configDir) {
				return nil, false, false, i18n.NewError("不能覆盖配置文件", "Cannot overwrite the configuration file")
			}
		}
		file, err = parent.OpenFile(filepath.Base(outputPath), os.O_WRONLY|os.O_CREATE|os.O_EXCL|outputNonblock, 0666)
	}
	if err != nil {
		return nil, false, false, err
	}
	info, err := file.Stat()
	if err == nil && !info.Mode().IsRegular() {
		err = i18n.NewError("输出文件必须是普通文件", "Output file must be a regular file")
	}
	sameSend, sameCapture := false, false
	s.captureMu.Lock()
	source := s.sendingFileInfo
	type captureSnapshot struct {
		path string
		info os.FileInfo
		file interface{ Stat() (os.FileInfo, error) }
	}
	targets := make([]captureSnapshot, 0, len(s.captureTargets))
	for _, target := range s.captureTargets {
		if target != own {
			targets = append(targets, captureSnapshot{target.path, target.info, target.file})
		}
	}
	s.captureMu.Unlock()
	if err == nil {
		if settingsDir != nil {
			_, currentSettings, _, err = s.settingsStore.OutputIdentity()
		}
		if currentSettings != nil && os.SameFile(info, currentSettings) {
			err = i18n.NewError("不能覆盖配置文件", "Cannot overwrite the configuration file")
		}
		if settingsPath != "" {
			settings, statErr := os.Stat(settingsPath)
			if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
				err = statErr
			} else if settings != nil && os.SameFile(info, settings) {
				err = i18n.NewError("不能覆盖配置文件", "Cannot overwrite the configuration file")
			}
		}
		for _, protected := range []*os.File{s.initialSettingsFile, s.savedSettingsFile} {
			if err != nil || protected == nil {
				continue
			}
			settings, statErr := protected.Stat()
			if statErr != nil {
				err = statErr
			} else if os.SameFile(info, settings) {
				err = i18n.NewError("不能覆盖配置文件", "Cannot overwrite the configuration file")
			}
		}
		if sendPath != "" {
			// A custom startSending callback may not register an open descriptor.
			if source == nil {
				source, _ = os.Stat(sendPath)
			}
			sameSend = source != nil && os.SameFile(info, source)
		}
		if capturePath != "" && len(targets) == 0 && own == nil {
			capture, _ := os.Stat(capturePath)
			sameCapture = capture != nil && os.SameFile(info, capture)
		}
		for _, target := range targets {
			capture := target.info
			if capture == nil && target.file != nil {
				capture, _ = target.file.Stat()
			}
			if capture == nil {
				capture, _ = os.Stat(target.path)
			}
			if capture != nil && os.SameFile(info, capture) {
				sameCapture = true
				break
			}
		}
		if err == nil && !sameSend && !sameCapture {
			if own != nil && !own.state.CompareAndSwap(0, 1) {
				err = context.Canceled
			}
			if err == nil {
				err = file.Truncate(0)
			}
			if err == nil && own != nil {
				s.captureMu.Lock()
				own.info = info
				s.captureMu.Unlock()
			}
		}
	}
	if err != nil || sameSend || sameCapture {
		return nil, sameSend, sameCapture, errors.Join(err, file.Close())
	}
	return file, false, false, nil
}

// Resolve symlinked parents and dangling final symlinks without creating them.
func resolvedOutputPath(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	for range 255 {
		parent, err := filepath.EvalSymlinks(filepath.Dir(abs))
		if err != nil {
			return "", err
		}
		abs = filepath.Join(parent, filepath.Base(abs))
		info, err := os.Lstat(abs)
		if errors.Is(err, os.ErrNotExist) {
			return abs, nil
		}
		if err != nil {
			return "", err
		}
		if info.Mode()&os.ModeSymlink == 0 {
			return abs, nil
		}
		target, err := os.Readlink(abs)
		if err != nil {
			return "", err
		}
		if filepath.IsAbs(target) {
			abs = target
		} else {
			abs = filepath.Join(parent, target)
		}
	}
	return "", errors.New("too many output symlinks")
}

func sameOutputName(a, b string) bool {
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

// startCapture ignores stale native-dialog results from an earlier connection.
func (s *App) startCapture(path string, connection *Session) {
	if s.FileBusy || s.closing || s.session == nil || s.session != connection || s.CapturePath != "" {
		return
	}
	snapshot := s.usedSettingsSnapshot()
	sendPath := s.sendingFilePath
	sendOpened := s.sendOpenDone
	s.FileBusy = true
	// Reserve the path while its file is being opened, so display saving
	// cannot truncate the capture before the completion callback runs.
	s.CapturePath = path
	opened := make(chan struct{})
	target := s.protectCapture(connection, path, opened)
	s.jobs.Add(1)
	go func() {
		defer s.jobs.Done()
		if sendPath != "" && sendOpened != nil {
			<-sendOpened
		}
		file, sameSendSource, sameCapture, err := s.createProtectedOutput(path, sendPath, "", target)
		if err == nil && file != nil {
			err = connection.SetCapture(file)
			if err != nil {
				err = errors.Join(err, file.Close())
			}
		}
		if sameSendSource || sameCapture || err != nil {
			s.releaseCapture(connection, target)
		}
		err = captureOpenError(err)
		result := s.retainTerminalError(err)
		close(opened)
		s.dispatch(func() {
			s.FileBusy = false
			if s.session != connection {
				if result != nil {
					s.appendOperationError("打开接收文件失败", "Failed to open receive file", err)
					s.acknowledgeTerminalError(result)
				}
				return
			}
			if err != nil {
				s.CapturePath = ""
				s.appendOperationError("打开接收文件失败", "Failed to open receive file", err)
				s.acknowledgeTerminalError(result)
				return
			}
			if sameSendSource {
				s.CapturePath = ""
				s.Note("请勿覆盖正在发送的文件", "Do not overwrite the file currently being sent")
				return
			}
			if sameCapture {
				s.CapturePath = ""
				s.Note("请勿覆盖正在接收的文件", "Do not overwrite the active receive file")
				return
			}
			if target.state.Load() == 2 {
				s.CapturePath = ""
			} else {
				connection.captureMu.Lock()
				active := connection.capture != nil
				connection.captureMu.Unlock()
				if !active {
					return
				}
				s.Note("接收数据将写入 "+path, "Received data will be written to "+path)
				s.saveUsedSnapshot(snapshot)
			}
		})
	}()
}

// CaptureTarget returns an opaque connection-bound accept callback for a dialog.
func (s *App) CaptureTarget() func(string) {
	connection := s.session
	return func(path string) { s.startCapture(path, connection) }
}

func (s *App) StopCapture() {
	if s.FileBusy || s.closing || s.session == nil || s.CapturePath == "" {
		return
	}
	p := s.session
	target := s.protectCapture(p, s.CapturePath, nil)
	s.FileBusy = true
	s.jobs.Add(1)
	go func() {
		defer s.jobs.Done()
		err := p.SetCapture(nil)
		s.releaseCapture(p, target)
		result := s.retainTerminalError(err)
		s.dispatch(func() {
			s.FileBusy = false
			if s.session != p {
				if err != nil {
					s.appendOperationError("关闭接收文件失败", "Failed to close receive file", err)
					s.acknowledgeTerminalError(result)
				}
				return
			}
			s.CapturePath = ""
			if err != nil {
				s.Fail("关闭接收文件失败", "Failed to close receive file", err)
				s.acknowledgeTerminalError(result)
			} else {
				s.Note("已停止接收存文件", "Receive-to-file stopped")
			}
		})
	}()
}
