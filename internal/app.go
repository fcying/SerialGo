package internal

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/fcying/SerialGo/communication/serialport"
	"github.com/fcying/SerialGo/config"
	"github.com/fcying/SerialGo/internal/i18n"
)

// View confines all application state and display updates to the frontend thread.
// Dispatch must post asynchronously and may discard callbacks after shutdown.
type View interface {
	Dispatch(func())
	AppendEvents([]Event)
}

type Text struct{ ZH, EN string }

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

	view              View
	ports             []serialport.Info
	session           *Session
	stopPump          chan struct{}
	closed            chan struct{}
	closing           bool
	jobs              sync.WaitGroup
	pendingPortMu     sync.Mutex
	pendingPort       *serialport.Port
	saver             *settingsSaver
	settingsSaveError bool
	cancelSend        context.CancelFunc
	loadErrorReported bool
}

func NewApp() *App {
	settings, path, err := config.Load()
	s := &App{Settings: settings, SettingsPath: path, LoadError: err}
	initialPort, initialRates := settings.Port, settings.BaudRates
	s.InitializeBaudRates()
	if s.Settings.Port != initialPort || !slices.Equal(s.Settings.BaudRates, initialRates) {
		s.SettingsChanged()
	}
	return s
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
	if s.session != nil {
		p := s.session
		s.session = nil
		close(s.stopPump)
		_ = p.Close()
	}
	s.jobs.Wait()
	s.pendingPortMu.Lock()
	pendingPort := s.pendingPort
	s.pendingPort = nil
	s.pendingPortMu.Unlock()
	if pendingPort != nil {
		_ = pendingPort.Close()
	}
	return s.FlushSettings()
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

func (s *App) ClearCounts() { s.Tx, s.Rx = 0, 0 }

func (s *App) SendValue() *string {
	if s.Settings.SendHex {
		return &s.Settings.SendHexText
	}
	return &s.Settings.SendText
}

func (s *App) CanSendFile() bool { return s.CanSend() && s.FilePath != "" }

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
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-s.closed:
			return
		case <-stop:
			return
		case <-ticker.C:
			if len(p.Events()) != 0 {
				s.dispatch(func() {
					if s.session == p {
						s.drain(p)
					}
				})
			}
		}
	}
}

func (s *App) drain(p *Session) {
	ch := p.Events()
	var records []Event
	var readErr, captureErr error
	// Process a snapshot so an active producer cannot monopolize the UI.
	for range len(ch) {
		event := <-ch
		if event.Err != nil {
			if !event.ReadFailed {
				captureErr = event.Err
			} else {
				readErr = event.Err
			}
			continue
		}
		if event.Sent {
			s.Tx += uint64(len(event.Data))
		} else {
			s.Rx += uint64(len(event.Data))
		}
		records = append(records, event)
	}
	if s.view != nil {
		s.view.AppendEvents(records)
	}
	if captureErr != nil {
		s.CapturePath = ""
		s.Fail("已停止接收存文件", "Receive-to-file stopped", captureErr)
	}
	if readErr != nil {
		s.disconnect()
		s.Fail("串口已断开", "Serial port disconnected", readErr)
	}
}

func (s *App) CanSend() bool { return s.session != nil && s.cancelSend == nil }

func (s *App) startSending(label Text, send func(context.Context, *Session) error) {
	if !s.CanSend() {
		return
	}
	p := s.session
	ctx, cancel := context.WithCancel(context.Background())
	s.cancelSend = cancel
	s.Note(label.ZH+"中", label.EN+" in progress")
	s.SaveUsedSettings()
	s.jobs.Add(1)
	go func() {
		defer s.jobs.Done()
		err := send(ctx, p)
		cancel()
		s.dispatch(func() {
			if s.session != p {
				return
			}
			s.cancelSend = nil
			s.drain(p)
			if s.session != p {
				return
			}
			switch {
			case errors.Is(err, context.Canceled):
				s.Note("已停止发送", "Sending stopped")
			case err != nil:
				s.Fail(label.ZH+"失败", label.EN+" failed", err)
			default:
				s.Note(label.ZH+"完成", label.EN+" completed")
			}
		})
	}()
}

func (s *App) StopSending() {
	if s.cancelSend != nil {
		s.cancelSend()
	}
}

func (s *App) format(hex, crlf bool) Format {
	return Format{Hex: hex, CRLF: crlf, Encoding: s.Settings.Encoding}
}

func (s *App) SendText(input string) {
	if !s.CanSend() {
		return
	}
	p := &s.Settings
	data, err := Encode(input, s.format(p.SendHex, p.CRLF))
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
	s.startSending(Text{ZH: "发送", EN: "Send"}, func(ctx context.Context, port *Session) error {
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
		data, err := Encode(preset.Data, s.format(preset.Hex, preset.CRLF))
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
	s.startSending(Text{ZH: "多条发送", EN: "Preset send"}, func(ctx context.Context, port *Session) error {
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
	s.startSending(Text{ZH: "发送文件", EN: "File send"}, func(ctx context.Context, p *Session) error {
		return p.SendFile(ctx, path, delay)
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
		s.dispatch(func() {
			s.PortBusy = false
			s.RefreshingPorts = false
			reportLoadError := !s.loadErrorReported && s.LoadError != nil
			s.loadErrorReported = true
			if err != nil {
				s.SetPorts(nil)
				if reportLoadError {
					s.Fail("读取参数失败", "Failed to load settings", s.LoadError)
				} else {
					s.Fail("刷新串口失败", "Failed to refresh serial ports", err)
				}
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
		})
	}()
}

func (s *App) TogglePort() {
	if s.PortBusy || s.closing {
		return
	}
	if s.session != nil {
		s.drain(s.session)
		if s.session != nil {
			s.disconnect()
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
			p := NewSession(port, baud)
			s.session, s.stopPump = p, make(chan struct{})
			s.Note("串口已打开", "Serial port opened")
			s.saveUsedSnapshot(snapshot)
			go s.pump(p, s.stopPump)
		})
	}()
}

func (s *App) disconnect() {
	p := s.session
	if p == nil {
		return
	}
	s.session = nil
	close(s.stopPump)
	s.StopSending()
	s.cancelSend = nil
	s.CapturePath = ""
	s.PortBusy = true
	status, statusError := s.Status, s.StatusError
	s.jobs.Add(1)
	go func() {
		defer s.jobs.Done()
		err := p.Close()
		s.dispatch(func() {
			s.PortBusy = false
			if s.Status != status || s.StatusError != statusError {
				return
			}
			if err != nil {
				s.Fail("关闭串口失败", "Failed to close serial port", err)
			} else {
				s.Note("串口已关闭", "Serial port closed")
			}
		})
	}()
}

func (s *App) SelectFile(path string) {
	s.FilePath = path
	s.Note("待发送文件已选择", "File selected for sending")
}

func (s *App) SaveLog(path, text string) {
	if s.FileBusy || s.closing {
		return
	}
	if path == s.CapturePath {
		s.Note("请勿覆盖正在接收的文件", "Do not overwrite the active receive file")
		return
	}
	snapshot := s.usedSettingsSnapshot()
	s.FileBusy = true
	s.jobs.Add(1)
	go func() {
		defer s.jobs.Done()
		file, err := os.Create(path)
		if err == nil {
			_, err = io.WriteString(file, text)
			err = errors.Join(err, file.Close())
		}
		s.dispatch(func() {
			s.FileBusy = false
			if err != nil {
				s.Fail("保存显示数据失败", "Failed to save displayed data", err)
			} else {
				s.Note("显示数据已保存", "Displayed data saved")
				s.saveUsedSnapshot(snapshot)
			}
		})
	}()
}

// startCapture ignores stale native-dialog results from an earlier connection.
func (s *App) startCapture(path string, connection *Session) {
	if s.FileBusy || s.closing || s.session == nil || s.session != connection || s.CapturePath != "" {
		return
	}
	snapshot := s.usedSettingsSnapshot()
	s.FileBusy = true
	// Reserve the path while its file is being opened, so display saving
	// cannot truncate the capture before the completion callback runs.
	s.CapturePath = path
	s.jobs.Add(1)
	go func() {
		defer s.jobs.Done()
		file, err := os.Create(path)
		if err == nil {
			err = connection.SetCapture(file)
			if err != nil {
				err = errors.Join(err, file.Close())
			}
		}
		s.dispatch(func() {
			s.FileBusy = false
			if s.session != connection {
				return
			}
			if err != nil {
				s.CapturePath = ""
				s.Fail("打开接收文件失败", "Failed to open receive file", err)
			} else {
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
	s.FileBusy = true
	s.jobs.Add(1)
	go func() {
		defer s.jobs.Done()
		err := p.SetCapture(nil)
		s.dispatch(func() {
			s.FileBusy = false
			if s.session != p {
				return
			}
			s.CapturePath = ""
			if err != nil {
				s.Fail("关闭接收文件失败", "Failed to close receive file", err)
			} else {
				s.Note("已停止接收存文件", "Receive-to-file stopped")
			}
		})
	}()
}
