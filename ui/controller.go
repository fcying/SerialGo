package ui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/fcying/SerialGo/communication/serialport"
	"github.com/fcying/SerialGo/config"
	"github.com/fcying/SerialGo/internal"
	"github.com/fcying/SerialGo/internal/i18n"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

type controller struct {
	settings           config.Settings
	path               string
	window             *mygo.Window
	ports              []serialport.Info
	portLabels         []string
	portChoice         string
	portMenuOpen       bool
	portMenuRefreshed  bool
	session            *internal.Session
	stopPump           chan struct{}
	closed             chan struct{}
	log                receiveLog
	saver              *settingsSaver
	settingsDirty      bool
	settingsRevision   uint64
	settingsSaveError  bool
	portSettingsOpen   bool
	optionsOpen        bool
	portDraft          serialport.Settings
	portSettingsError  localizedText
	portBaudRatesDraft []string
	baudPickers        [2]baudPickerState
	customBaudOpen     bool
	customBaudForPort  bool
	customBaudText     string
	customBaudError    localizedText
	baudDeleteOpen     bool
	baudDeleteForPort  bool
	baudDeleteValue    string
	baudDeleteError    localizedText

	status                                        localizedText
	statusError, alwaysOnTop, repeat, loopPresets bool
	tx, rx                                        uint64
	filePath, capturePath                         string
	dialogBusy                                    bool
	cancelSend                                    context.CancelFunc
	presetSplit                                   float32
	receiveSplit                                  float32
	presetList                                    ui.ListState
	presetGeneration                              int
	presetDeleteOpen                              bool
	presetDeleteIndex                             int
}

func Run(title string) error {
	settings, path, err := config.Load()
	localeErr := initializeLanguage(settings.Language)
	if localeErr != nil {
		return localeErr
	}
	initialPort := settings.Port
	initialBaudRates := settings.BaudRates
	s := &controller{settings: settings, path: path, presetSplit: 64, closed: make(chan struct{})}
	s.initializeBaudRates()
	s.configureLog()
	mygo.App.SetMenu(nil)
	mygo.App.WhenReady(func() {
		s.refreshPorts()
		if err != nil {
			s.fail("读取参数失败", "Failed to load settings", err)
		}
		s.window = mygo.NewWindow(mygo.WindowOptions{
			Title: title, Width: 800, Height: 600, MinWidth: 780, MinHeight: 420,
			Content: ui.View(s.view),
		})
		s.startSettingsSaver()
		if s.settings.Port != initialPort || !slices.Equal(s.settings.BaudRates, initialBaudRates) {
			s.settingsChanged()
		}
		iconErr := s.setWindowIcon()
		if iconErr != nil {
			s.fail("设置窗口图标失败", "Failed to set window icon", iconErr)
		}
		s.window.OnClosed(func() {
			close(s.closed)
		})
	})
	runErr := mygo.App.Run()
	s.stopSettingsSaver()
	if s.session != nil {
		s.disconnect()
	}
	if runErr != nil {
		return runErr
	}
	return s.flushSettings()
}

func (s *controller) fail(zh, en string, err error) {
	s.settingsSaveError = false
	s.status = localizedText{
		zh: zh + ": " + i18n.ErrorText(err, false),
		en: en + ": " + i18n.ErrorText(err, true),
	}
	s.statusError = true
}

func (s *controller) note(zh, en string) {
	if s.settingsSaveError {
		return
	}
	s.status, s.statusError = localizedText{zh: zh, en: en}, false
}

func (s *controller) statusText() string { return s.tr(s.status.zh, s.status.en) }

func (s *controller) applyAlwaysOnTop() { s.window.SetAlwaysOnTop(s.alwaysOnTop) }

func (s *controller) clearCounts() { s.tx, s.rx = 0, 0 }

func (s *controller) sendValue() *string {
	if s.settings.SendHex {
		return &s.settings.SendHexText
	}
	return &s.settings.SendText
}

func (s *controller) canSendFile() bool { return s.canSend() && s.filePath != "" }

func (s *controller) refreshPorts() {
	if s.session != nil {
		return
	}
	ports, err := serialport.List()
	if err != nil {
		s.setPorts(nil)
		s.fail("刷新串口失败", "Failed to refresh serial ports", err)
		return
	}
	s.setPorts(ports)
	if len(ports) == 0 {
		s.note("未找到串口", "No serial ports found")
	} else {
		s.note(fmt.Sprintf("找到 %d 个串口", len(ports)), fmt.Sprintf("Found %d serial ports", len(ports)))
	}
}

func (s *controller) setPorts(ports []serialport.Info) {
	previous := s.settings.Port.Name
	s.ports = ports
	clear(s.portLabels)
	s.portLabels = slices.Grow(s.portLabels[:0], len(ports))
	selected := -1
	for i, port := range ports {
		label := port.Name
		if port.Description != "" {
			label += " " + port.Description
		}
		s.portLabels = append(s.portLabels, label)
		if port.Name == previous {
			selected = i
		}
	}
	if selected < 0 && len(ports) > 0 {
		selected = 0
	}
	s.settings.Port.Name, s.portChoice = "", ""
	if selected >= 0 {
		s.settings.Port.Name = ports[selected].Name
		s.portChoice = s.portLabels[selected]
	}
	if s.settings.Port.Name != previous {
		s.settingsChanged()
	}
}

func (s *controller) selectPort() {
	for i, label := range s.portLabels {
		if label != s.portChoice {
			continue
		}
		name := s.ports[i].Name
		if name != s.settings.Port.Name {
			s.settings.Port.Name = name
			s.settingsChanged()
		}
		return
	}
}

func (s *controller) togglePort() {
	if s.session != nil {
		s.drain(s.session)
		if s.session != nil {
			s.disconnect()
		}
		return
	}
	available := slices.ContainsFunc(s.ports, func(port serialport.Info) bool { return port.Name == s.settings.Port.Name })
	if !available {
		s.note("请先选择可用串口", "Select an available serial port first")
		return
	}
	port, err := serialport.Open(s.settings.Port)
	if err != nil {
		s.fail("打开串口失败", "Failed to open serial port", err)
		return
	}
	baud, _ := strconv.Atoi(strings.TrimSpace(s.settings.Port.Baud))
	p := internal.NewSession(port, baud)
	s.session = p
	s.stopPump = make(chan struct{})
	s.note("串口已打开", "Serial port opened")
	s.saveUsedSettings()
	go s.pump(p, s.stopPump)
}

func (s *controller) pump(p *internal.Session, stop <-chan struct{}) {
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
				s.window.Update(func() {
					if s.session == p {
						s.drain(p)
					}
				})
			}
		}
	}
}

func (s *controller) drain(p *internal.Session) {
	ch := p.Events()
	var records []internal.Event
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
			s.tx += uint64(len(event.Data))
		} else {
			s.rx += uint64(len(event.Data))
		}
		records = append(records, event)
	}
	s.log.append(records)
	if captureErr != nil {
		s.capturePath = ""
		s.fail("已停止接收存文件", "Receive-to-file stopped", captureErr)
	}
	if readErr != nil {
		s.disconnect()
		s.fail("串口已断开", "Serial port disconnected", readErr)
	}
}

func (s *controller) disconnect() {
	p := s.session
	s.session = nil
	close(s.stopPump)
	s.stopSending()
	s.cancelSend = nil
	s.capturePath = ""
	err := p.Close()
	if err != nil {
		s.fail("关闭串口失败", "Failed to close serial port", err)
	} else {
		s.note("串口已关闭", "Serial port closed")
	}
}

func (s *controller) configureLog() {
	p := &s.settings
	var gap time.Duration
	if p.Timestamp {
		gap, _ = internal.Milliseconds(p.PacketGap, "分包超时", 1)
		if gap == 0 {
			gap = 20 * time.Millisecond
		}
	}
	s.log.configure(logOptions{hex: p.ReceiveHex, timestamp: p.Timestamp, showSent: p.ShowSent, encoding: p.Encoding, gap: gap})
}

func (s *controller) canSend() bool { return s.session != nil && s.cancelSend == nil }

func (s *controller) startSending(label localizedText, send func(context.Context, *internal.Session) error) {
	if !s.canSend() {
		return
	}
	p := s.session
	ctx, cancel := context.WithCancel(context.Background())
	s.cancelSend = cancel
	s.note(label.zh+"中", label.en+" in progress")
	s.saveUsedSettings()
	go func() {
		err := send(ctx, p)
		cancel()
		s.window.Update(func() {
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
				s.note("已停止发送", "Sending stopped")
			case err != nil:
				s.fail(label.zh+"失败", label.en+" failed", err)
			default:
				s.note(label.zh+"完成", label.en+" completed")
			}
		})
	}()
}

func (s *controller) stopSending() {
	if s.cancelSend != nil {
		s.cancelSend()
	}
}

func (s *controller) format(hex, crlf bool) internal.Format {
	return internal.Format{Hex: hex, CRLF: crlf, Encoding: s.settings.Encoding}
}

func (s *controller) sendText(input string) {
	if !s.canSend() {
		return
	}
	p := &s.settings
	data, err := internal.Encode(input, s.format(p.SendHex, p.CRLF))
	if err != nil {
		s.fail("发送失败", "Send failed", err)
		return
	}
	var delay time.Duration
	if s.repeat {
		delay, err = internal.Milliseconds(p.Interval, "定时发送间隔", 1)
		if err != nil {
			s.fail("发送失败", "Send failed", err)
			return
		}
	}
	s.startSending(localizedText{zh: "发送", en: "Send"}, func(ctx context.Context, port *internal.Session) error {
		for {
			err := port.Send(ctx, data)
			if err != nil || delay == 0 {
				return err
			}
			err = internal.Wait(ctx, delay)
			if err != nil {
				return err
			}
		}
	})
}

func (s *controller) sendPresets(index int) {
	if !s.canSend() {
		return
	}
	type item struct {
		data  []byte
		delay time.Duration
	}
	var items []item
	loop := index < 0 && s.loopPresets
	for i, preset := range s.settings.Presets {
		if index >= 0 && i != index || index < 0 && !preset.Enabled {
			continue
		}
		data, err := internal.Encode(preset.Data, s.format(preset.Hex, preset.CRLF))
		if err != nil {
			s.fail(fmt.Sprintf("第 %d 条发送失败", i+1), fmt.Sprintf("Failed to send preset %d", i+1), err)
			return
		}
		var delay time.Duration
		if index < 0 {
			delay, err = internal.Milliseconds(preset.Interval, "多条发送间隔", 1)
			if err != nil {
				s.fail(fmt.Sprintf("第 %d 条发送失败", i+1), fmt.Sprintf("Failed to send preset %d", i+1), err)
				return
			}
		}
		items = append(items, item{data, delay})
	}
	if len(items) == 0 {
		s.note("请先勾选要发送的条目", "Select the presets to send first")
		return
	}
	s.startSending(localizedText{zh: "多条发送", en: "Preset send"}, func(ctx context.Context, port *internal.Session) error {
		for {
			for i, item := range items {
				err := port.Send(ctx, item.data)
				if err != nil {
					return err
				}
				if loop || i < len(items)-1 {
					err = internal.Wait(ctx, item.delay)
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

func (s *controller) sendFile() {
	path := s.filePath
	delay, err := internal.Milliseconds(s.settings.FileDelay, "文件分段延时", 0)
	if err != nil {
		s.fail("发送文件失败", "File send failed", err)
		return
	}
	s.startSending(localizedText{zh: "发送文件", en: "File send"}, func(ctx context.Context, p *internal.Session) error {
		return p.SendFile(ctx, path, delay)
	})
}

func (s *controller) chooseFile() {
	if s.dialogBusy {
		return
	}
	s.dialogBusy = true
	title := s.tr("打开待发送文件", "Open file to send")
	go func() {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{Parent: s.window, Title: title})
		s.window.Update(func() {
			s.dialogBusy = false
			if err != nil {
				s.fail("打开文件对话框失败", "Failed to open file dialog", err)
			} else if len(paths) != 0 {
				s.filePath = paths[0]
				s.note("待发送文件已选择", "File selected for sending")
			}
		})
	}()
}

func (s *controller) saveDialog(title, defaultPath string, accept func(string)) {
	if s.dialogBusy {
		return
	}
	s.dialogBusy = true
	go func() {
		path, err := mygo.Dialog.Save(mygo.SaveDialogOptions{Parent: s.window, Title: title, DefaultPath: defaultPath})
		s.window.Update(func() {
			s.dialogBusy = false
			if err != nil {
				s.fail("保存文件对话框失败", "Failed to open save dialog", err)
			} else if path != "" {
				accept(path)
			}
		})
	}()
}

func (s *controller) saveLog() {
	s.saveDialog(s.tr("保存显示数据", "Save displayed data"), "SerialGo.txt", func(path string) {
		if path == s.capturePath {
			s.note("请勿覆盖正在接收的文件", "Do not overwrite the active receive file")
			return
		}
		file, err := os.Create(path)
		if err == nil {
			_, err = s.log.text.WriteTo(file)
			err = errors.Join(err, file.Close())
		}
		if err != nil {
			s.fail("保存显示数据失败", "Failed to save displayed data", err)
		} else {
			s.note("显示数据已保存", "Displayed data saved")
			s.saveUsedSettings()
		}
	})
}

func (s *controller) toggleCapture() {
	p := s.session
	if p == nil {
		return
	}
	if s.capturePath != "" {
		err := p.SetCapture(nil)
		s.capturePath = ""
		if err != nil {
			s.fail("关闭接收文件失败", "Failed to close receive file", err)
		} else {
			s.note("已停止接收存文件", "Receive-to-file stopped")
		}
		return
	}
	s.saveDialog(s.tr("接收原始字节到文件", "Receive raw bytes to file"), "SerialGo-rx.bin", func(path string) {
		if s.session != p {
			return
		}
		file, err := os.Create(path)
		if err == nil {
			err = p.SetCapture(file)
			if err != nil {
				err = errors.Join(err, file.Close())
			}
		}
		if err != nil {
			s.fail("打开接收文件失败", "Failed to open receive file", err)
		} else {
			s.capturePath = path
			s.note("接收数据将写入 "+path, "Received data will be written to "+path)
			s.saveUsedSettings()
		}
	})
}
