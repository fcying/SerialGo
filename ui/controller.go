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

	"github.com/fcying/SerialGo/assets"
	"github.com/fcying/SerialGo/communication/serialport"
	"github.com/fcying/SerialGo/config"
	"github.com/fcying/SerialGo/internal"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

type controller struct {
	settings     config.Settings
	path         string
	window       *mygo.Window
	ports        []string
	session      *internal.Session
	stopPump     chan struct{}
	closed       chan struct{}
	log          receiveLog
	menuBindings []menuBinding

	status                                                  string
	statusError, advanced, alwaysOnTop, repeat, loopPresets bool
	tx, rx                                                  uint64
	filePath, capturePath                                   string
	dialogBusy                                              bool
	cancelSend                                              context.CancelFunc
	presetSplit                                             float32
}

func Run(title string) error {
	settings, path, err := config.Load()
	if !slices.Contains(baudRates, settings.Port.Baud) {
		settings.Port.Baud = "115200"
	}
	s := &controller{settings: settings, path: path, presetSplit: 64, closed: make(chan struct{})}
	s.configureLog()
	mygo.App.WhenReady(func() {
		s.refreshPorts()
		if err != nil {
			s.fail("读取参数失败", err)
		}
		s.window = mygo.NewWindow(mygo.WindowOptions{
			Title: title, Width: 800, Height: 600, MinWidth: 780, MinHeight: 420,
			Content: ui.View(s.view),
		})
		iconErr := s.window.SetIcon(assets.IconPNG)
		if iconErr != nil {
			s.fail("设置窗口图标失败", iconErr)
		}
		s.installMenu()
		s.window.OnClosed(func() { close(s.closed) })
	})
	runErr := mygo.App.Run()
	if s.session != nil {
		s.disconnect()
	}
	if runErr != nil {
		return runErr
	}
	return config.Save(s.path, s.settings)
}

func (s *controller) fail(action string, err error) {
	s.status, s.statusError = fmt.Sprintf("%s: %v", action, err), true
}

func (s *controller) note(text string) { s.status, s.statusError = text, false }

func (s *controller) applyAlwaysOnTop() { s.window.SetAlwaysOnTop(s.alwaysOnTop) }

func (s *controller) clearCounts() { s.tx, s.rx = 0, 0 }

func (s *controller) sendValue() *string {
	if s.settings.SendHex {
		return &s.settings.SendHexText
	}
	return &s.settings.SendText
}

func (s *controller) canSendFile() bool { return s.canSend() && s.filePath != "" }

func (s *controller) saveSettings() {
	err := config.Save(s.path, s.settings)
	if err != nil {
		s.fail("保存参数失败", err)
	} else if s.path == "" {
		s.note("配置不可写, 未保存")
	} else {
		s.note("参数已保存")
	}
}

func (s *controller) refreshPorts() {
	ports, err := serialport.List()
	if err != nil {
		s.ports = nil
		s.settings.Port.Name = ""
		s.fail("刷新串口失败", err)
		return
	}
	s.ports = ports
	if !slices.Contains(ports, s.settings.Port.Name) {
		s.settings.Port.Name = ""
		if len(ports) > 0 {
			s.settings.Port.Name = ports[0]
		}
	}
	if len(ports) == 0 {
		s.note("未找到串口")
	} else {
		s.note(fmt.Sprintf("找到 %d 个串口", len(ports)))
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
	if !slices.Contains(s.ports, s.settings.Port.Name) {
		s.note("请先选择可用串口")
		return
	}
	port, err := serialport.Open(s.settings.Port)
	if err != nil {
		s.fail("打开串口失败", err)
		return
	}
	baud, _ := strconv.Atoi(strings.TrimSpace(s.settings.Port.Baud))
	p := internal.NewSession(port, baud)
	s.session = p
	s.stopPump = make(chan struct{})
	s.note("串口已打开")
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
		s.fail("已停止接收存文件", captureErr)
	}
	if readErr != nil {
		s.disconnect()
		s.fail("串口已断开", readErr)
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
		s.fail("关闭串口失败", err)
	} else {
		s.note("串口已关闭")
	}
}

func (s *controller) setLines(previousDTR, previousRTS bool) {
	if s.session == nil {
		return
	}
	err := s.session.SetLines(s.settings.Port.DTR, s.settings.Port.RTS)
	if err != nil {
		s.settings.Port.DTR, s.settings.Port.RTS = previousDTR, previousRTS
		s.fail("设置控制线失败", err)
	}
}

func (s *controller) configureLog() {
	p := &s.settings
	var gap time.Duration
	if p.Packet {
		gap, _ = internal.Milliseconds(p.PacketGap, "分包超时", 1)
		if gap == 0 {
			gap = 20 * time.Millisecond
		}
	}
	s.log.configure(logOptions{hex: p.ReceiveHex, timestamp: p.Timestamp, showSent: p.ShowSent, encoding: p.Encoding, gap: gap})
}

func (s *controller) canSend() bool { return s.session != nil && s.cancelSend == nil }

func (s *controller) startSending(label string, send func(context.Context, *internal.Session) error) {
	if !s.canSend() {
		return
	}
	p := s.session
	ctx, cancel := context.WithCancel(context.Background())
	s.cancelSend = cancel
	s.note(label + "中")
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
				s.note("已停止发送")
			case err != nil:
				s.fail(label+"失败", err)
			default:
				s.note(label + "完成")
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
	p := &s.settings
	return internal.Format{Hex: hex, CRLF: crlf, Encoding: p.Encoding, Checksum: p.Checksum, From: p.CheckFrom, EndSkip: p.CheckEnd}
}

func (s *controller) sendText(input string) {
	if !s.canSend() {
		return
	}
	p := &s.settings
	data, err := internal.Encode(input, s.format(p.SendHex, p.CRLF))
	if err != nil {
		s.fail("发送失败", err)
		return
	}
	var delay time.Duration
	if s.repeat {
		delay, err = internal.Milliseconds(p.Interval, "定时发送间隔", 1)
		if err != nil {
			s.fail("发送失败", err)
			return
		}
	}
	s.startSending("发送", func(ctx context.Context, port *internal.Session) error {
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
			s.fail(fmt.Sprintf("第 %d 条发送失败", i+1), err)
			return
		}
		var delay time.Duration
		if index < 0 {
			delay, err = internal.Milliseconds(preset.Interval, "多条发送间隔", 1)
			if err != nil {
				s.fail(fmt.Sprintf("第 %d 条发送失败", i+1), err)
				return
			}
		}
		items = append(items, item{data, delay})
	}
	if len(items) == 0 {
		s.note("请先勾选要发送的条目")
		return
	}
	s.startSending("多条发送", func(ctx context.Context, port *internal.Session) error {
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
		s.fail("发送文件失败", err)
		return
	}
	s.startSending("发送文件", func(ctx context.Context, p *internal.Session) error {
		return p.SendFile(ctx, path, delay)
	})
}

func (s *controller) chooseFile() {
	if s.dialogBusy {
		return
	}
	s.dialogBusy = true
	go func() {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{Parent: s.window, Title: "打开待发送文件"})
		s.window.Update(func() {
			s.dialogBusy = false
			if err != nil {
				s.fail("打开文件对话框失败", err)
			} else if len(paths) != 0 {
				s.filePath = paths[0]
				s.note("待发送文件已选择")
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
				s.fail("保存文件对话框失败", err)
			} else if path != "" {
				accept(path)
			}
		})
	}()
}

func (s *controller) saveLog() {
	s.saveDialog("保存显示数据", "SerialGo.txt", func(path string) {
		if path == s.capturePath {
			s.note("请勿覆盖正在接收的文件")
			return
		}
		file, err := os.Create(path)
		if err == nil {
			_, err = s.log.text.WriteTo(file)
			err = errors.Join(err, file.Close())
		}
		if err != nil {
			s.fail("保存显示数据失败", err)
		} else {
			s.note("显示数据已保存")
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
			s.fail("关闭接收文件失败", err)
		} else {
			s.note("已停止接收存文件")
		}
		return
	}
	s.saveDialog("接收原始字节到文件", "SerialGo-rx.bin", func(path string) {
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
			s.fail("打开接收文件失败", err)
		} else {
			s.capturePath = path
			s.note("接收数据将写入 " + path)
		}
	})
}
