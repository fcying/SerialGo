package ui

import (
	"time"

	"github.com/fcying/SerialGo/communication/serialport"
	"github.com/fcying/SerialGo/config"
	"github.com/fcying/SerialGo/internal"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

type controller struct {
	app                                                               *internal.App
	settings                                                          *config.Settings
	window                                                            *mygo.Window
	log                                                               receiveLog
	portMenuOpen, portMenuRefreshed                                   bool
	portSettingsOpen, optionsOpen                                     bool
	portDraft                                                         serialport.Settings
	portBaudRatesDraft                                                []string
	portSettingsError                                                 localizedText
	baudPickers                                                       [2]baudPickerState
	connectionContentWidth, connectionBaudWidth                       float32
	connectionLanguage                                                string
	customBaudOpen, customBaudForPort                                 bool
	customBaudText                                                    string
	customBaudError                                                   localizedText
	baudDeleteOpen, baudDeleteForPort                                 bool
	baudDeleteValue                                                   string
	baudDeleteError                                                   localizedText
	receiveFontDialogOpen                                             bool
	receiveFontNameDraft, receiveFontSizeDraft, receiveFontColorDraft string
	receiveFontColorChoice                                            string
	packetGapDraft                                                    string
	packetGapError                                                    localizedText
	receiveFontError                                                  localizedText
	autoScroll, alwaysOnTop, dialogBusy                               bool
	presetSplit, receiveSplit                                         float32
	receiveSplitAdjusted                                              bool
	presetList                                                        ui.ListState
	presetGeneration                                                  int
	presetDeleteOpen                                                  bool
	presetDeleteIndex                                                 int
}

func newController(app *internal.App) *controller {
	return &controller{
		app: app, settings: &app.Settings, portDraft: app.Settings.Port,
		presetSplit: 64, autoScroll: true,
	}
}

func Run(app *internal.App, title string) error {
	if err := initializeLanguage(app.Settings.Language); err != nil {
		return err
	}
	s := newController(app)
	s.configureLog()
	mygo.App.SetMenu(nil)
	mygo.App.WhenReady(func() {
		s.window = mygo.NewWindow(mygo.WindowOptions{
			Title: title, Width: 800, Height: 600, MinWidth: 800, MinHeight: 420,
			Content: ui.View(s.view),
		})
		app.Startup(s)
		app.RefreshPorts()
		if err := s.setWindowIcon(); err != nil {
			app.Fail("设置窗口图标失败", "Failed to set window icon", err)
		}
	})
	runErr := mygo.App.Run()
	shutdownErr := app.Shutdown()
	if runErr != nil {
		return runErr
	}
	return shutdownErr
}

func (s *controller) Dispatch(fn func())                   { s.window.Update(fn) }
func (s *controller) AppendEvents(events []internal.Event) { s.log.append(events) }
func (s *controller) statusText() string                   { return s.tr(s.app.Status.ZH, s.app.Status.EN) }
func (s *controller) applyAlwaysOnTop()                    { s.window.SetAlwaysOnTop(s.alwaysOnTop) }

func (s *controller) autoScrollChanged() {
	s.log.autoScrollDisabled = !s.autoScroll
	if s.autoScroll {
		s.log.preserveScroll = false
		s.log.scrollToEnd = true
		return
	}
	s.log.scrollToEnd = false
	s.log.captureScrollAnchor()
}

func (s *controller) configureLog() {
	p := s.settings
	var gap time.Duration
	if p.Timestamp {
		gap, _ = internal.Milliseconds(p.PacketGap, "分包超时", 1)
		if gap == 0 {
			gap = 20 * time.Millisecond
		}
	}
	s.log.configure(logOptions{hex: p.ReceiveHex, timestamp: p.Timestamp, showSent: p.ShowSent, encoding: p.Encoding, gap: gap, limit: p.DisplayBufferLimit})
}

func (s *controller) displaySettingsChanged() {
	s.configureLog()
	s.app.SettingsChanged()
}

func (s *controller) applyDisplaySettings() {
	s.displaySettingsChanged()
	s.app.SaveUsedSettings()
}

func (s *controller) openOptions() {
	s.packetGapDraft = s.settings.PacketGap
	s.packetGapError = localizedText{}
	s.optionsOpen = true
}

func (s *controller) packetGapChanged() {
	if _, err := internal.Milliseconds(s.packetGapDraft, "分包超时", 1); err != nil {
		s.packetGapError = localizedText{
			zh: "时间戳分包超时必须为 1 到 86400000 ms 的整数",
			en: "Timestamp packet gap must be an integer from 1 to 86400000 ms",
		}
		return
	}
	s.packetGapError = localizedText{}
	if s.settings.PacketGap == s.packetGapDraft {
		return
	}
	s.settings.PacketGap = s.packetGapDraft
	s.displaySettingsChanged()
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
				s.app.Fail("打开文件对话框失败", "Failed to open file dialog", err)
			} else if len(paths) != 0 {
				s.app.SelectFile(paths[0])
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
				s.app.Fail("保存文件对话框失败", "Failed to open save dialog", err)
			} else if path != "" {
				accept(path)
			}
		})
	}()
}

func (s *controller) saveLog() {
	if s.app.FileBusy {
		return
	}
	s.saveDialog(s.tr("导出显示日志", "Export displayed log"), "SerialGo.txt", func(path string) {
		s.app.SaveLog(path, s.log.text.String())
	})
}

func (s *controller) startCapture() {
	if s.app.FileBusy || !s.app.Connected() || s.app.CapturePath != "" {
		return
	}
	accept := s.app.CaptureTarget()
	s.saveDialog(s.tr("录制原始接收数据", "Record raw received data"), "SerialGo-rx.dat", accept)
}
