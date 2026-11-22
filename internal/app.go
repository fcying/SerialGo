package internal

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
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
}

type terminalError struct {
	err error
}

type sendResult struct {
	done chan struct{}
	err  error
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
	captureTargets         map[*Session]*captureTarget
	terminalMu             sync.Mutex
	terminalErrors         map[*terminalError]struct{}
	saver                  *settingsSaver
	settingsSaveError      bool
	cancelSend             context.CancelFunc
	sendDone               *sendResult
	sendingFilePath        string
	fileProgressUpdatedAt  time.Time
	fileProgressGeneration uint64
	loadErrorReported      bool
	settingsWarning        Text
	curve                  curveEngine
}

func NewApp() *App {
	settings, path, err := config.Load()
	warning := normalizeStartupSettings(&settings)
	s := &App{Settings: settings, SettingsPath: path, LoadError: err, settingsWarning: warning}
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
	return errors.Join(shutdownErr, s.FlushSettings())
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

// Capture reservations outlive CapturePath and are released only after the file
// worker has closed. A new open gets its own token so stale completions cannot
// release a later capture's destination.
func (s *App) protectCapture(p *Session, path string, opened <-chan struct{}) *captureTarget {
	s.captureMu.Lock()
	defer s.captureMu.Unlock()
	target := s.captureTargets[p]
	if opened != nil || target == nil && path != "" {
		target = &captureTarget{path: path, opened: opened}
		if s.captureTargets == nil {
			s.captureTargets = make(map[*Session]*captureTarget)
		}
		s.captureTargets[p] = target
	}
	return target
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
	s.settingsSaveError = false
	s.Status = Text{
		ZH: zh + ": " + i18n.ErrorText(err, false),
		EN: en + ": " + i18n.ErrorText(err, true),
	}
	s.StatusError = true
}

func (s *App) Note(zh, en string) {
	if s.settingsSaveError {
		return
	}
	s.Status, s.StatusError = Text{ZH: zh, EN: en}, false
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
			readErr = event.Err
		}
	}
	if captureErr != nil {
		if captureStopped {
			s.releaseCapture(p, s.protectCapture(p, "", nil))
			s.CapturePath = ""
		}
		s.Fail("已停止接收存文件", "Receive-to-file stopped", captureErr)
		for _, worker := range captureWorkers {
			p.acknowledgeCaptureError(worker)
		}
	}
	if readErr != nil {
		s.disconnect(readErr)
		s.Fail("串口已断开", "Serial port disconnected", readErr)
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
	done := &sendResult{done: make(chan struct{})}
	s.sendDone = done
	s.cancelSend = cancel
	if filePath != "" {
		s.sendingFilePath = filePath
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
	progressGeneration := s.fileProgressGeneration
	s.startSending(Text{ZH: "发送文件", EN: "File send"}, path, func(ctx context.Context, p *Session) error {
		return sendFileWithProgress(ctx, path, delay, p, func(total int64) {
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
			s.Fail("读取参数失败", "Failed to load settings", s.LoadError)
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
		err := p.Close()
		s.curveDisconnected()
		if target != nil && target.opened != nil {
			<-target.opened
		}
		if sendDone != nil {
			<-sendDone.done
		}
		s.releaseCapture(p, target)
		result := s.retainTerminalError(err)
		s.dispatch(func() {
			s.deliverDisplay(p)
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
			if s.Status != status || s.StatusError != statusError {
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
	capturePaths := s.capturePaths()
	sendPath := s.sendingFilePath
	snapshot := s.usedSettingsSnapshot()
	s.FileBusy = true
	s.jobs.Add(1)
	go func() {
		defer s.jobs.Done()
		sameCapture := slices.ContainsFunc(capturePaths, func(other string) bool { return sameFilePath(path, other) })
		sameSendSource := sendPath != "" && sameFilePath(path, sendPath)
		var err error
		if !sameCapture && !sameSendSource {
			var file *os.File
			file, err = os.Create(path)
			if err == nil {
				_, err = io.WriteString(file, text)
				err = errors.Join(err, file.Close())
			}
		}
		s.dispatch(func() {
			s.FileBusy = false
			if sameSendSource {
				s.Note("请勿覆盖正在发送的文件", "Do not overwrite the file currently being sent")
				return
			}
			if sameCapture {
				s.Note("请勿覆盖正在接收的文件", "Do not overwrite the active receive file")
				return
			}
			if err != nil {
				s.Fail("保存显示数据失败", "Failed to save displayed data", err)
			} else {
				s.Note("显示数据已保存", "Displayed data saved")
				s.saveUsedSnapshot(snapshot)
			}
		})
	}()
}

func sameFilePath(path, other string) bool {
	if path == other && path != "" {
		return true
	}
	pathInfo, err := os.Stat(path)
	if err != nil {
		return false
	}
	otherInfo, err := os.Stat(other)
	if err != nil {
		return false
	}
	return os.SameFile(pathInfo, otherInfo)
}

// startCapture ignores stale native-dialog results from an earlier connection.
func (s *App) startCapture(path string, connection *Session) {
	if s.FileBusy || s.closing || s.session == nil || s.session != connection || s.CapturePath != "" {
		return
	}
	snapshot := s.usedSettingsSnapshot()
	sendPath := s.sendingFilePath
	s.FileBusy = true
	// Reserve the path while its file is being opened, so display saving
	// cannot truncate the capture before the completion callback runs.
	s.CapturePath = path
	opened := make(chan struct{})
	target := s.protectCapture(connection, path, opened)
	s.jobs.Add(1)
	go func() {
		defer s.jobs.Done()
		sameSendSource := sendPath != "" && sameFilePath(path, sendPath)
		var file *os.File
		var err error
		if !sameSendSource {
			file, err = os.Create(path)
			if err == nil {
				err = connection.SetCapture(file)
				if err != nil {
					err = errors.Join(err, file.Close())
				}
			}
		}
		if sameSendSource || err != nil {
			s.releaseCapture(connection, target)
		}
		close(opened)
		s.dispatch(func() {
			s.FileBusy = false
			if s.session != connection {
				return
			}
			if sameSendSource {
				s.CapturePath = ""
				s.Note("请勿覆盖正在发送的文件", "Do not overwrite the file currently being sent")
				return
			}
			if err != nil {
				s.CapturePath = ""
				s.Fail("打开接收文件失败", "Failed to open receive file", err)
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
