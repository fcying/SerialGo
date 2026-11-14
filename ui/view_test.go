package ui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/fcying/SerialGo/communication/serialport"
	"github.com/fcying/SerialGo/config"
	"github.com/fcying/SerialGo/internal"

	mygo "github.com/egoist/mygo"
	gui "github.com/egoist/mygo/ui"
)

func TestStatusBarKeepsLongTextOnOneLine(t *testing.T) {
	for _, language := range []struct {
		code, serialConfig, redundantStatus, statusLabel string
	}{
		{"zh-CN", "1000000 bps · 8N1", "COM1 已关闭  1000000 bps · 8N1", "操作状态"},
		{"en", "1000000 bps · 8N1", "COM1 Closed  1000000 bps · 8N1", "Operation status"},
	} {
		for _, width := range []int{780, 800, 1000} {
			for _, scale := range []float32{1, 1.25, 1.5, 1.6, 2} {
				t.Run(fmt.Sprintf("%s_width_%d_scale_%g", language.code, width, scale), func(t *testing.T) {
					s := newController(&internal.App{
						Settings: config.Settings{Language: language.code, Port: serialport.Settings{
							Name: "COM1", Baud: "1000000", DataBits: "8", StopBits: "1", Parity: "None",
						}},
						Tx: ^uint64(0), StatusError: true,
						Status: internal.Text{
							ZH: "打开串口失败: " + strings.Repeat("The port is unavailable. ", 10),
							EN: "Failed to open port: " + strings.Repeat("The port is unavailable. ", 10),
						},
					})
					s.configureLog()
					tt := gui.NewTester(s.view, width, 600)
					tt.SetScale(scale)
					line, found := tt.Find("R: 0")
					if !found {
						t.Fatal("receive counter is missing")
					}
					for _, text := range []string{
						"S: 18446744073709551615",
						language.serialConfig,
						language.statusLabel,
						s.tr("清零", "Reset"),
						s.tr("收发计数清零", "Reset counters"),
					} {
						bounds, found := tt.Find(text)
						if !found {
							t.Fatalf("status field %q is missing", text)
						}
						center := bounds.Y + bounds.H/2
						lineCenter := line.Y + line.H/2
						if center < lineCenter-1 || center > lineCenter+1 || bounds.Y < 0 || bounds.Y+bounds.H > 600 {
							t.Errorf("status field %q extends outside its footer line: field=%+v line=%+v", text, bounds, line)
						}
					}
					if language.code == "zh-CN" {
						if _, found := tt.Find("清计数"); found {
							t.Error("old reset caption remains visible")
						}
					}
					if _, found := tt.Find(language.redundantStatus); found {
						t.Errorf("status bar should not repeat the port name and connection state: %q", language.redundantStatus)
					}
					reset, _ := tt.Find(s.tr("收发计数清零", "Reset counters"))
					tx, _ := tt.Find("S: 18446744073709551615")
					rx, _ := tt.Find("R: 0")
					if tx.X+tx.W > rx.X || rx.X+rx.W > reset.X {
						t.Errorf("counters should precede Reset in S, R order: tx=%+v rx=%+v reset=%+v", tx, rx, reset)
					}
					serialConfig, _ := tt.Find(language.serialConfig)
					status, _ := tt.Find(language.statusLabel)
					if serialConfig.W > 160 || serialConfig.X+serialConfig.W > status.X {
						t.Errorf("serial summary wastes footer space or overlaps status: config=%+v status=%+v", serialConfig, status)
					}
					for _, label := range []string{
						s.tr("选项", "Options"),
						s.tr("清空发送区", "Clear send area"),
						s.tr("保存数据", "Save data"),
						s.tr("HEX 显示", "HEX display"),
						s.tr("HEX 发送", "HEX send"),
					} {
						bounds, found := tt.Find(label)
						if !found {
							t.Fatalf("control %q is missing", label)
						}
						if bounds.X < 0 || bounds.X+bounds.W > float32(width)+1 || bounds.Y < 0 || bounds.Y+bounds.H > line.Y+1 {
							t.Errorf("control %q is clipped or overlaps the footer: %+v", label, bounds)
						}
					}
					for _, pair := range [][2]string{
						{s.tr("文件延时", "File delay"), s.tr("文件分段延时 (ms)", "File chunk delay (ms)")},
					} {
						caption, captionFound := tt.Find(pair[0])
						field, fieldFound := tt.Find(pair[1])
						if !captionFound || !fieldFound {
							t.Fatalf("option group is missing: %q", pair)
						}
						if caption.Y < field.Y-1 || caption.Y+caption.H > field.Y+field.H+1 {
							t.Errorf("caption %q wrapped away from its field: caption=%+v field=%+v", pair[0], caption, field)
						}
					}
					if err := tt.Click(s.tr("收发计数清零", "Reset counters")); err != nil {
						t.Fatal(err)
					}
					if s.app.Tx != 0 || s.app.Rx != 0 {
						t.Fatal("relocated Reset button did not clear counters")
					}
					if err := tt.Click(s.tr("选项", "Options")); err != nil {
						t.Fatal(err)
					}
					for _, label := range []string{
						"语言 / Language",
						s.tr("文本编码", "Text encoding"),
						s.tr("时间戳分包超时 (ms)", "Timestamp packet gap (ms)"),
					} {
						bounds, found := tt.Find(label)
						if !found || bounds.X < 0 || bounds.X+bounds.W > float32(width)+1 || bounds.Y < 0 || bounds.Y+bounds.H > 600 {
							t.Errorf("option %q is missing or clipped: %+v", label, bounds)
						}
					}
					if _, found := tt.Find(s.tr("显示发送记录", "Show sent data")); found {
						t.Error("Show sent remains in the Options dialog")
					}
				})
			}
		}
	}
}

func TestFormatSerialConfig(t *testing.T) {
	tests := []struct {
		name, baud, dataBits, parity, stopBits, want string
	}{
		{"none", "1000000", "8", "None", "1", "1000000 bps · 8N1"},
		{"odd", "115200", "7", "Odd", "1", "115200 bps · 7O1"},
		{"even", "9600", "7", "Even", "2", "9600 bps · 7E2"},
		{"mark", "38400", "8", "Mark", "1", "38400 bps · 8M1"},
		{"space with fractional stop bit", "19200", "8", "Space", "1.5", "19200 bps · 8S1.5"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := formatSerialConfig(test.baud, test.dataBits, test.parity, test.stopBits)
			if got != test.want {
				t.Fatalf("formatSerialConfig() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestStatusCountersGrowWithContent(t *testing.T) {
	app := &internal.App{Settings: config.Settings{Language: "en", Port: serialport.Settings{Baud: "1000000", DataBits: "8", StopBits: "1", Parity: "None"}}}
	s := newController(app)
	s.configureLog()
	tt := gui.NewTester(s.view, 780, 420)
	smallTx, _ := tt.Find("S: 0")
	smallRx, _ := tt.Find("R: 0")
	smallStatus, _ := tt.Find("Operation status")
	app.Tx, app.Rx = 123456789, 987654321
	tt.Frame()
	largeTx, txFound := tt.Find("S: 123456789")
	largeRx, rxFound := tt.Find("R: 987654321")
	largeStatus, statusFound := tt.Find("Operation status")
	if !txFound || !rxFound || !statusFound {
		t.Fatal("status fields missing after counters grow")
	}
	if largeTx.W <= smallTx.W || largeRx.W <= smallRx.W || largeRx.X <= smallRx.X || largeStatus.W >= smallStatus.W {
		t.Errorf("counters did not grow into remaining status space: tx=%+v -> %+v rx=%+v -> %+v status=%+v -> %+v", smallTx, largeTx, smallRx, largeRx, smallStatus, largeStatus)
	}
}

func TestSendAreaResizePreservesDraftAndVisibleActions(t *testing.T) {
	for _, height := range []int{420, 600, 1000, 1400} {
		t.Run(fmt.Sprint(height), func(t *testing.T) {
			s := newController(&internal.App{Settings: config.Settings{Language: "en", SendText: "draft\nsecond line"}})
			s.configureLog()
			tt := gui.NewTester(s.view, 800, height)
			before, found := tt.Find("Send area")
			if !found || before.H < 40 || before.H > 140 {
				t.Fatalf("default send area does not fit the action column: %+v", before)
			}
			serialPanel, found := tt.Find("Connection panel")
			if !found {
				t.Fatal("serial panel is missing")
			}
			sendPanel, found := tt.Find("Send panel")
			if !found || serialPanel.Y != sendPanel.Y || serialPanel.H != sendPanel.H {
				t.Fatalf("panel borders are not aligned: serial=%+v send=%+v", serialPanel, sendPanel)
			}
			receive, found := tt.Find("Receive log")
			if !found {
				t.Fatal("receive area is missing")
			}
			x, y := receive.X+receive.W/2, receive.Y+receive.H+0.5
			tt.Press(x, y)
			tt.Move(x, y-60)
			tt.Release(x, y-60)
			tt.Frame()
			after, _ := tt.Find("Send area")
			if after.H < before.H+59 || after.H > before.H+61 {
				t.Fatalf("dragging 60px did not resize input by 60px: before=%+v after=%+v", before, after)
			}
			receive, _ = tt.Find("Receive log")
			y = receive.Y + receive.H + 0.5
			tt.Press(x, y)
			tt.Move(x, float32(height*2))
			tt.Release(x, float32(height*2))
			tt.Frame()
			input, inputFound := tt.Find("Send area")
			actions, actionsFound := tt.Find("Send")
			panel, panelFound := tt.Find("Send panel")
			if !inputFound || input.H < 40 || !actionsFound || !panelFound || actions.X < input.X+input.W-1 || actions.Y < panel.Y || actions.Y+actions.H > panel.Y+panel.H {
				t.Fatalf("resizing hid or misplaced the input or action column: input=%+v actions=%+v panel=%+v", input, actions, panel)
			}
			if s.settings.SendText != "draft\nsecond line" {
				t.Fatal("resizing changed the send draft")
			}
			if err := tt.Click("Clear send area"); err != nil {
				t.Fatal(err)
			}
			if s.settings.SendText != "" {
				t.Fatal("compact clear button did not clear the send draft")
			}
		})
	}
}

func TestCompactControlsStayInsidePanels(t *testing.T) {
	for _, language := range []string{"en", "zh-CN"} {
		for _, width := range []int{minWindowWidth, minWindowWidth + 20, minWindowWidth + 40, 1280} {
			t.Run(fmt.Sprintf("%s_%d", language, width), func(t *testing.T) {
				app := &internal.App{Settings: config.Settings{
					Language:  language,
					Port:      serialport.Settings{Name: "COM1", Baud: "1000000", DataBits: "8", Parity: "Space", StopBits: "1.5"},
					BaudRates: []string{"9600", "1000000"},
					FileDelay: "99999",
					Interval:  "10000",
				}}
				s := newController(app)
				s.configureLog()
				var baudTextWidth, settingsLabelWidth, autoScrollTextWidth float32
				var expectedReceiveOptionsWidth float32
				tt := gui.NewTester(func(c *gui.Context) {
					s.view(c)
					baudTextWidth, _ = c.MeasureText(0, gui.Span{Text: "1000000"})
					settingsLabelWidth, _ = c.MeasureText(0, gui.Span{Text: s.tr("设置", "Settings")})
					autoScrollTextWidth, _ = c.MeasureText(0, gui.Span{Text: s.tr("自动滚动", "Auto scroll")})
					autoScrollCheckboxWidth := autoScrollTextWidth + c.Theme().Space(4) + c.Theme().Space(2)
					expectedReceiveOptionsWidth = receiveOptionsFirstColumnWidth + controlGap + autoScrollCheckboxWidth + panelChromeWidth
				}, width, 420)
				expectedPickerWidth := baudTextWidth + baudPickerChromeWidth
				expectedConnectionWidth := controlGap + expectedPickerWidth + settingsLabelWidth + serialConfigButtonChromeWidth + panelChromeWidth
				expectedSendWidth := float32(width) - 2*2 - 3*4 - expectedConnectionWidth - expectedReceiveOptionsWidth - 184
				groups := []struct {
					panel         string
					expectedWidth float32
					controls      []string
				}{
					{s.tr("连接控制", "Connection panel"), expectedConnectionWidth, []string{
						s.tr("端口号", "Port"), s.tr("波特率", "Baud rate"), s.tr("打开", "Open"), s.tr("设置", "Settings"),
					}},
					{s.tr("接收显示控制", "Receive display controls"), expectedReceiveOptionsWidth, []string{
						s.tr("HEX 显示", "HEX display"), s.tr("时间戳", "Timestamp"), s.tr("显示发送", "Show sent"),
						s.tr("自动滚动", "Auto scroll"), s.tr("清除接收窗口", "Clear receive log"), s.tr("选项", "Options"),
					}},
					{s.tr("文件操作", "File operations"), 184, []string{
						s.tr("打开文件", "Open file"), s.tr("发送文件", "Send file"), s.tr("保存数据", "Save data"), s.tr("录制数据", "Record data"),
						s.tr("文件分段延时 (ms)", "File chunk delay (ms)"), s.tr("停止", "Stop"),
					}},
					{s.tr("发送面板", "Send panel"), expectedSendWidth, []string{
						s.tr("HEX 发送", "HEX send"), "CRLF", s.tr("定时发送", "Repeat"), s.tr("定时发送间隔 (ms)", "Repeat interval (ms)"),
						s.tr("发送区", "Send area"), s.tr("多条发送面板", "Preset panel"), s.tr("发送", "Send"), s.tr("清空发送区", "Clear send area"),
					}},
				}
				var previousRight, firstY, firstHeight float32
				for i, group := range groups {
					panel, found := tt.Find(group.panel)
					if !found || panel.X < previousRight || panel.X+panel.W > float32(width)+1 || panel.H > float32(minSendWorkspaceHeight)+1 ||
						(group.expectedWidth > 0 && (panel.W < group.expectedWidth-0.01 || panel.W > group.expectedWidth+0.01)) {
						t.Fatalf("panel %q is missing, clipped, overlapping, or unexpectedly sized: bounds=%+v expectedWidth=%.1f", group.panel, panel, group.expectedWidth)
					}
					if i == 0 {
						firstY, firstHeight = panel.Y, panel.H
					} else if panel.Y != firstY || panel.H != firstHeight {
						t.Errorf("panel %q is not aligned: %+v", group.panel, panel)
					}
					previousRight = panel.X + panel.W
					for _, label := range group.controls {
						control, found := tt.Find(label)
						if !found || control.W <= 0 || control.H <= 0 || control.X < panel.X || control.Y < panel.Y || control.X+control.W > panel.X+panel.W || control.Y+control.H > panel.Y+panel.H {
							t.Errorf("control %q is missing or extends outside its panel: control=%+v panel=%+v", label, control, panel)
						}
					}
				}
				connectionPanel, panelFound := tt.Find(s.tr("连接控制", "Connection panel"))
				if !panelFound {
					t.Fatal("connection panel is missing")
				}
				baudText, found := tt.Find("1000000")
				if !found || baudText.W+0.01 < baudTextWidth {
					t.Errorf("seven-digit baud rate is clipped: bounds=%+v requiredWidth=%v", baudText, baudTextWidth)
				}
				baudPicker, baudPickerFound := tt.AccessByRole(gui.RolePopUpButton, s.tr("波特率", "Baud rate"))
				portPicker, portPickerFound := tt.AccessByRole(gui.RolePopUpButton, s.tr("端口号", "Port"))
				if !baudPickerFound || !portPickerFound {
					t.Fatal("baud selector or port selector is missing")
				}
				valueCenter := baudText.X + baudText.W/2
				textAreaLeft := baudPicker.Bounds.X + baudPickerBorderWidth + serialSelectorPaddingX
				textAreaRight := baudPicker.Bounds.X + baudPicker.Bounds.W - baudPickerBorderWidth - 2*serialSelectorPaddingX - baudPickerArrowSize - microGap
				selectorCenter := (textAreaLeft + textAreaRight) / 2
				if valueCenter < selectorCenter-0.5 || valueCenter > selectorCenter+0.5 {
					t.Errorf("selected baud value is not centered: text=%+v picker=%+v", baudText, baudPicker.Bounds)
				}
				if baudPicker.Bounds.W < expectedPickerWidth-0.01 || baudPicker.Bounds.W > expectedPickerWidth+0.01 {
					t.Errorf("baud selector is not fitted to the seven-digit value: bounds=%+v expectedWidth=%.1f", baudPicker.Bounds, expectedPickerWidth)
				}
				settingsButton, settingsFound := tt.AccessByRole(gui.RoleButton, s.tr("串口设置", "Port settings"))
				indicator, indicatorFound := tt.Find(s.tr("串口已关闭", "Serial port closed"))
				open, openFound := tt.AccessByRole(gui.RoleButton, s.tr("打开", "Open"))
				if !settingsFound || !indicatorFound || !openFound {
					t.Fatal("connection action row is missing Settings, status light, or Open")
				}
				portRight := portPicker.Bounds.X + portPicker.Bounds.W
				settingsRight := settingsButton.Bounds.X + settingsButton.Bounds.W
				if portRight < settingsRight-0.01 || portRight > settingsRight+0.01 {
					t.Errorf("port selector right edge must align with Settings: port=%+v settings=%+v", portPicker.Bounds, settingsButton.Bounds)
				}
				baudCenter := baudPicker.Bounds.Y + baudPicker.Bounds.H/2
				settingsCenter := settingsButton.Bounds.Y + settingsButton.Bounds.H/2
				if settingsButton.Bounds.Y != baudPicker.Bounds.Y || settingsCenter < baudCenter-1 || settingsCenter > baudCenter+1 ||
					settingsButton.Bounds.X < baudPicker.Bounds.X+baudPicker.Bounds.W-0.01 ||
					settingsButton.Bounds.X+settingsButton.Bounds.W > connectionPanel.X+connectionPanel.W {
					t.Errorf("Settings must fit after the baud selector on the same row: baud=%+v settings=%+v panel=%+v", baudPicker.Bounds, settingsButton.Bounds, connectionPanel)
				}
				indicatorCenter := indicator.Y + indicator.H/2
				openCenter := open.Bounds.Y + open.Bounds.H/2
				if indicatorCenter < openCenter-1 || indicatorCenter > openCenter+1 ||
					indicator.X+indicator.W > open.Bounds.X || open.Bounds.X+open.Bounds.W > connectionPanel.X+connectionPanel.W {
					t.Errorf("status light must sit immediately before Open: indicator=%+v open=%+v panel=%+v", indicator, open.Bounds, connectionPanel)
				}
				if open.Bounds.Y <= baudPicker.Bounds.Y {
					t.Errorf("Open row must follow the baud selector row: baud=%+v open=%+v", baudPicker.Bounds, open.Bounds)
				}
				for _, label := range []string{s.tr("数据位", "Data bits"), s.tr("校验位", "Parity"), s.tr("停止位", "Stop bits")} {
					if _, found := tt.AccessByRole(gui.RolePopUpButton, label); found {
						t.Errorf("framing control %q should be in port configuration, not the main panel", label)
					}
				}
				timestamp, _ := tt.Find(s.tr("时间戳", "Timestamp"))
				autoScroll, _ := tt.Find(s.tr("自动滚动", "Auto scroll"))
				columnOffset := timestamp.X - autoScroll.X
				if columnOffset < -1 || columnOffset > 1 {
					t.Errorf("receive checkboxes do not share the second column: timestamp=%+v auto-scroll=%+v", timestamp, autoScroll)
				}
				receivePanel, receivePanelFound := tt.Find(s.tr("接收显示控制", "Receive display controls"))
				autoScrollCheckbox, autoScrollCheckboxFound := tt.AccessByRole(gui.RoleCheckBox, s.tr("自动滚动", "Auto scroll"))
				if !receivePanelFound || !autoScrollCheckboxFound {
					t.Fatal("receive panel or auto-scroll checkbox is missing")
				}
				autoScrollRightInset := receivePanel.X + receivePanel.W - autoScrollCheckbox.Bounds.X - autoScrollCheckbox.Bounds.W
				expectedRightInset := panelInsetX
				if autoScrollRightInset < expectedRightInset-0.01 || autoScrollRightInset > expectedRightInset+0.01 {
					t.Errorf("receive panel right padding must align with Auto scroll: panel=%+v auto-scroll=%+v rightInset=%v", receivePanel, autoScrollCheckbox.Bounds, autoScrollRightInset)
				}
				receiveClear, receiveClearFound := tt.AccessByRole(gui.RoleButton, s.tr("清除接收窗口", "Clear receive log"))
				receiveOptionsButton, receiveOptionsFound := tt.AccessByRole(gui.RoleButton, s.tr("选项", "Options"))
				if !receiveClearFound || !receiveOptionsFound {
					t.Fatal("receive action buttons are missing")
				}
				expectedActionButtonWidth := (expectedReceiveOptionsWidth - panelChromeWidth - controlGap) / 2
				if receiveClear.Bounds.W < expectedActionButtonWidth-0.01 || receiveClear.Bounds.W > expectedActionButtonWidth+0.01 ||
					receiveOptionsButton.Bounds.W < expectedActionButtonWidth-0.01 || receiveOptionsButton.Bounds.W > expectedActionButtonWidth+0.01 {
					t.Errorf("receive action buttons do not fit the compact panel: clear=%+v options=%+v expectedWidth=%v", receiveClear.Bounds, receiveOptionsButton.Bounds, expectedActionButtonWidth)
				}
				for _, pair := range [][2]string{
					{s.tr("波特率", "Baud rate"), s.tr("打开", "Open")},
					{s.tr("自动滚动", "Auto scroll"), s.tr("清除接收窗口", "Clear receive log")},
				} {
					above, _ := tt.Find(pair[0])
					below, _ := tt.Find(pair[1])
					// Find may return a text label instead of the full control box.
					distance := below.Y + below.H/2 - above.Y - above.H/2
					if distance < 24 || distance > 32 {
						t.Errorf("controls %q retain an empty row or overlap: above=%+v below=%+v", pair, above, below)
					}
				}
				input, _ := tt.Find(s.tr("发送区", "Send area"))
				clear, _ := tt.Find(s.tr("清空发送区", "Clear send area"))
				send, _ := tt.Find(s.tr("发送", "Send"))
				if input.H < 40 || input.H > 60 || send.X < input.X+input.W || clear.X < input.X+input.W || clear.Y < send.Y+send.H || clear.Y+clear.H > input.Y+input.H {
					t.Errorf("three-row input or action column is clipped: input=%+v send=%+v clear=%+v", input, send, clear)
				}
				interval, intervalFound := tt.Find(s.tr("定时发送间隔 (ms)", "Repeat interval (ms)"))
				hex, hexFound := tt.Find(s.tr("HEX 发送", "HEX send"))
				crlf, crlfFound := tt.Find("CRLF")
				repeat, repeatFound := tt.Find(s.tr("定时发送", "Repeat"))
				presets, presetsFound := tt.Find(s.tr("多条发送面板", "Preset panel"))
				if !intervalFound || !hexFound || !crlfFound || !repeatFound || !presetsFound {
					t.Fatal("send header controls are missing")
				}
				hexCenterY := hex.Y + hex.H/2
				crlfCenterY := crlf.Y + crlf.H/2
				repeatCenterY := repeat.Y + repeat.H/2
				presetsCenterY := presets.Y + presets.H/2
				intervalCenterY := interval.Y + interval.H/2
				if hexCenterY < crlfCenterY-1 || hexCenterY > crlfCenterY+1 || hexCenterY < repeatCenterY-1 || hexCenterY > repeatCenterY+1 || hexCenterY < presetsCenterY-1 || hexCenterY > presetsCenterY+1 || hexCenterY < intervalCenterY-1 || hexCenterY > intervalCenterY+1 {
					t.Errorf("HEX, CRLF, Repeat, interval, and Presets do not share one header row: HEX=%+v CRLF=%+v Repeat=%+v interval=%+v Presets=%+v", hex, crlf, repeat, interval, presets)
				}
				if crlf.X <= hex.X || repeat.X <= crlf.X || presets.X <= repeat.X || interval.X <= repeat.X+repeat.W || interval.X+interval.W > presets.X || interval.Y+interval.H > input.Y+1 {
					t.Errorf("send header controls are out of order or overlap the editor: HEX=%+v CRLF=%+v Repeat=%+v interval=%+v Presets=%+v input=%+v", hex, crlf, repeat, interval, presets, input)
				}
				rightOffset := presets.X + presets.W - clear.X - clear.W
				if rightOffset < -1 || rightOffset > 1 {
					t.Errorf("preset toggle is not aligned to the right edge: presets=%+v clear=%+v", presets, clear)
				}
				delay, _ := tt.Find(s.tr("文件分段延时 (ms)", "File chunk delay (ms)"))
				stopSend, _ := tt.Find(s.tr("停止", "Stop"))
				distance := stopSend.Y + stopSend.H/2 - delay.Y - delay.H/2
				if distance < -1 || distance > 1 || stopSend.X < delay.X+delay.W {
					t.Errorf("file delay and stop do not share the third row: delay=%+v stop=%+v", delay, stopSend)
				}
				if delay.W != interval.W {
					t.Errorf("file delay and Repeat interval fields must have equal widths: delay=%+v interval=%+v", delay, interval)
				}
				openFile, openFileFound := tt.AccessByRole(gui.RoleButton, s.tr("打开文件", "Open file"))
				sendFile, sendFileFound := tt.AccessByRole(gui.RoleButton, s.tr("发送文件", "Send file"))
				save, saveFound := tt.AccessByRole(gui.RoleButton, s.tr("保存数据", "Save data"))
				record, recordFound := tt.AccessByRole(gui.RoleButton, s.tr("录制数据", "Record data"))
				if !openFileFound || !sendFileFound || !saveFound || !recordFound {
					t.Fatal("file action buttons are missing")
				}
				saveStartsAtOpen := save.Bounds.X >= openFile.Bounds.X-1 && save.Bounds.X <= openFile.Bounds.X+1
				saveWidthMatchesOpen := save.Bounds.W >= openFile.Bounds.W-1 && save.Bounds.W <= openFile.Bounds.W+1
				if !saveStartsAtOpen || !saveWidthMatchesOpen {
					t.Errorf("save must match the open-file column, leaving the second slot empty: save=%+v open=%+v", save.Bounds, openFile.Bounds)
				}
				recordStartsAfterSave := record.Bounds.X >= save.Bounds.X+save.Bounds.W-1 && record.Bounds.X <= save.Bounds.X+save.Bounds.W+5
				if !recordStartsAfterSave || record.Bounds.W < sendFile.Bounds.W-1 || record.Bounds.W > sendFile.Bounds.W+1 {
					t.Errorf("Record data must align with the Send file column: record=%+v send=%+v", record.Bounds, sendFile.Bounds)
				}
				if !record.Disabled {
					t.Error("Record data should be disabled without a connection")
				}
				app.CapturePath = "recording.dat"
				tt.Frame()
				filePanel, _ := tt.Find(s.tr("文件操作", "File operations"))
				activeSave, activeSaveFound := tt.AccessByRole(gui.RoleButton, s.tr("保存数据", "Save data"))
				stopCapture, stopFound := tt.AccessByRole(gui.RoleButton, s.tr("停止录制", "Stop recording"))
				if !activeSaveFound || !stopFound {
					t.Fatal("active recording controls are missing")
				}
				activeSaveMatches := activeSave.Bounds.X >= openFile.Bounds.X-1 && activeSave.Bounds.X <= openFile.Bounds.X+1 &&
					activeSave.Bounds.W >= openFile.Bounds.W-1 && activeSave.Bounds.W <= openFile.Bounds.W+1
				stopMatchesSend := stopCapture.Bounds.X >= sendFile.Bounds.X-1 && stopCapture.Bounds.X <= sendFile.Bounds.X+1 &&
					stopCapture.Bounds.W >= sendFile.Bounds.W-1 && stopCapture.Bounds.W <= sendFile.Bounds.W+1
				stopInsidePanel := stopCapture.Bounds.X+stopCapture.Bounds.W <= filePanel.X+filePanel.W
				if !activeSaveMatches || !stopMatchesSend || !stopInsidePanel {
					t.Errorf("active recording must align Save data and Stop recording with the two file-action columns: save=%+v stop=%+v open=%+v send=%+v panel=%+v", activeSave.Bounds, stopCapture.Bounds, openFile.Bounds, sendFile.Bounds, filePanel)
				}
			})
		}
	}
}

func TestReceiveControlsUseTwoRowsAndActionsShareLine(t *testing.T) {
	app := &internal.App{Settings: config.Settings{
		Language:  "en",
		Port:      serialport.Settings{Name: "COM1", Baud: "115200", DataBits: "8", Parity: "None", StopBits: "1"},
		BaudRates: []string{"9600", "115200"},
		FileDelay: "1",
	}}
	s := newController(app)
	s.configureLog()
	tt := gui.NewTester(s.view, 1280, 640)

	hex, hexFound := tt.Find("HEX display")
	timestamp, timestampFound := tt.Find("Timestamp")
	showSent, showSentFound := tt.Find("Show sent")
	autoScroll, autoScrollFound := tt.Find("Auto scroll")
	clear, clearFound := tt.Find("Clear receive log")
	options, optionsFound := tt.Find("Options")
	if !hexFound || !timestampFound || !showSentFound || !autoScrollFound || !clearFound || !optionsFound {
		t.Fatalf("receive controls are missing: HEX=%+v timestamp=%+v Show-sent=%+v auto-scroll=%+v clear=%+v options=%+v", hex, timestamp, showSent, autoScroll, clear, options)
	}
	hexCenterY := hex.Y + hex.H/2
	timestampCenterY := timestamp.Y + timestamp.H/2
	showSentCenterY := showSent.Y + showSent.H/2
	autoScrollCenterY := autoScroll.Y + autoScroll.H/2
	clearCenterY := clear.Y + clear.H/2
	optionsCenterY := options.Y + options.H/2
	if hexCenterY < timestampCenterY-1 || hexCenterY > timestampCenterY+1 || showSentCenterY < autoScrollCenterY-1 || showSentCenterY > autoScrollCenterY+1 || showSentCenterY <= hexCenterY+1 {
		t.Errorf("receive checkboxes are not arranged as two rows: HEX=%+v timestamp=%+v Show-sent=%+v auto-scroll=%+v", hex, timestamp, showSent, autoScroll)
	}
	if clearCenterY < optionsCenterY-1 || clearCenterY > optionsCenterY+1 || clearCenterY <= showSentCenterY+1 || clear.X >= options.X {
		t.Errorf("clear and options buttons do not share the row below the checkboxes: clear=%+v options=%+v Show-sent=%+v", clear, options, showSent)
	}
	if err := tt.Click("Show sent"); err != nil {
		t.Fatal(err)
	}
	if !app.Settings.ShowSent {
		t.Fatal("Show sent did not enable outgoing records")
	}
}

func TestSendControlsShareOneHeaderRow(t *testing.T) {
	for _, language := range []string{"en", "zh-CN"} {
		t.Run(language, func(t *testing.T) {
			app := &internal.App{Settings: config.Settings{Language: language}}
			s := newController(app)
			s.configureLog()
			tt := gui.NewTester(s.view, minWindowWidth, 600)

			hex, hexFound := tt.Find(s.tr("HEX 发送", "HEX send"))
			crlf, crlfFound := tt.Find("CRLF")
			repeat, repeatFound := tt.Find(s.tr("定时", "Repeat"))
			presets, presetsFound := tt.Find(s.tr("多条发送面板", "Preset panel"))
			presetButton, presetButtonFound := tt.AccessByRole(gui.RoleButton, s.tr("多条发送面板", "Preset panel"))
			if !hexFound || !crlfFound || !repeatFound || !presetsFound || !presetButtonFound {
				t.Fatalf("send controls are missing: HEX=%+v CRLF=%+v Repeat=%+v Presets=%+v Toggle=%+v", hex, crlf, repeat, presets, presetButton)
			}
			if presetButton.Bounds.W != presetToggleWidth {
				t.Errorf("collapsed preset toggle width=%v, want %v", presetButton.Bounds.W, presetToggleWidth)
			}
			if _, found := tt.Find("+"); !found {
				t.Error("collapsed preset toggle does not show +")
			}
			expandTooltip := s.tr("展开多条发送", "Expand preset panel")
			tt.Move(presetButton.Bounds.X+presetButton.Bounds.W/2, presetButton.Bounds.Y+presetButton.Bounds.H/2)
			tt.Advance(time.Second)
			if !tt.HasText(expandTooltip) {
				t.Fatalf("hovering collapsed preset toggle did not show tooltip %q: %q", expandTooltip, tt.Texts())
			}
			expandedApp := &internal.App{Settings: config.Settings{Language: language, ShowPresets: true}}
			expandedController := newController(expandedApp)
			expandedController.configureLog()
			expandedTester := gui.NewTester(expandedController.view, minWindowWidth, 600)
			expandedToggle, expandedToggleFound := expandedTester.AccessByRole(gui.RoleButton, expandedController.tr("多条发送面板", "Preset panel"))
			if _, found := expandedTester.Find("-"); !found || !expandedToggleFound {
				t.Fatal("expanded preset toggle or its button is missing")
			}
			if expandedToggle.Bounds.W != presetToggleWidth {
				t.Errorf("expanded preset toggle width=%v, want %v", expandedToggle.Bounds.W, presetToggleWidth)
			}
			collapseTooltip := expandedController.tr("收起多条发送", "Collapse preset panel")
			expandedTester.Move(expandedToggle.Bounds.X+expandedToggle.Bounds.W/2, expandedToggle.Bounds.Y+expandedToggle.Bounds.H/2)
			expandedTester.Advance(time.Second)
			if !expandedTester.HasText(collapseTooltip) {
				t.Fatalf("hovering expanded preset toggle did not show tooltip %q: %q", collapseTooltip, expandedTester.Texts())
			}
			hexControl, hexControlFound := tt.AccessByRole(gui.RoleCheckBox, s.tr("HEX 发送", "HEX send"))
			crlfControl, crlfControlFound := tt.AccessByRole(gui.RoleCheckBox, "CRLF")
			repeatControl, repeatControlFound := tt.AccessByRole(gui.RoleCheckBox, s.tr("定时发送", "Repeat"))
			if !hexControlFound || !crlfControlFound || !repeatControlFound {
				t.Fatal("HEX, CRLF, and Repeat checkboxes are missing")
			}
			hexCRLFGap := crlfControl.Bounds.X - hexControl.Bounds.X - hexControl.Bounds.W
			if hexCRLFGap != controlGap {
				t.Errorf("HEX/CRLF gap=%v, want %v", hexCRLFGap, controlGap)
			}
			crlfRepeatGap := repeatControl.Bounds.X - crlfControl.Bounds.X - crlfControl.Bounds.W
			if crlfRepeatGap != controlGap {
				t.Errorf("CRLF/Repeat gap=%v, want %v", crlfRepeatGap, controlGap)
			}
			hexCenterY := hex.Y + hex.H/2
			crlfCenterY := crlf.Y + crlf.H/2
			repeatCenterY := repeat.Y + repeat.H/2
			presetsCenterY := presets.Y + presets.H/2
			if hexCenterY < crlfCenterY-1 || hexCenterY > crlfCenterY+1 || hexCenterY < repeatCenterY-1 || hexCenterY > repeatCenterY+1 || hexCenterY < presetsCenterY-1 || hexCenterY > presetsCenterY+1 || crlf.X <= hex.X || repeat.X <= crlf.X {
				t.Errorf("send controls do not share one row in HEX/CRLF/Repeat/Presets order: HEX=%+v CRLF=%+v Repeat=%+v Presets=%+v", hex, crlf, repeat, presets)
			}
			if err := tt.Click("CRLF"); err != nil {
				t.Fatal(err)
			}
			if !app.Settings.CRLF {
				t.Fatal("CRLF did not enable after clicking its send control")
			}
		})
	}
}

func TestPendingFilePathIsNotDisplayedSeparately(t *testing.T) {
	app := &internal.App{FilePath: `C:\Users\test\Downloads\payload.bin`, Settings: config.Settings{Language: "en"}}
	s := newController(app)
	s.configureLog()
	tt := gui.NewTester(s.view, 1280, 640)
	if _, found := tt.Find("File to send"); found {
		t.Fatal("pending file path is still displayed in a separate text field")
	}
	if _, found := tt.Find("Send file"); !found {
		t.Fatal("send-file button is missing")
	}
}

func TestFileActionsAndCaptureStateAreTransient(t *testing.T) {
	for _, language := range []string{"en", "zh-CN"} {
		t.Run(language, func(t *testing.T) {
			settings := config.Settings{Language: language}
			app := &internal.App{Settings: settings}
			s := newController(app)
			s.configureLog()
			tt := gui.NewTester(s.view, 780, 420)
			save := s.tr("保存数据", "Save data")
			record := s.tr("录制数据", "Record data")
			stop := s.tr("停止录制", "Stop recording")
			saveButton, saveFound := tt.AccessByRole(gui.RoleButton, save)
			recordButton, recordFound := tt.AccessByRole(gui.RoleButton, record)
			if !saveFound || !recordFound {
				t.Fatal("Save data and Record data buttons are missing")
			}
			if _, found := tt.AccessByRole(gui.RoleMenuButton, save); found {
				t.Fatal("Save data should export directly without a dropdown menu")
			}
			if saveButton.Disabled || !recordButton.Disabled {
				t.Errorf("save/record availability is incorrect without a connection: save=%+v record=%+v", saveButton, recordButton)
			}
			if _, found := tt.Find(stop); found {
				t.Fatal("Stop recording is visible without active capture")
			}

			app.CapturePath = "recording.dat"
			tt.Frame()
			if _, found := tt.Find(stop); !found {
				t.Fatal("active capture did not switch Record data to Stop recording")
			}
			if _, found := tt.Find(record); found {
				t.Fatal("Record data remained visible during active capture")
			}

			restartedApp := &internal.App{Settings: app.Settings}
			restarted := newController(restartedApp)
			restarted.configureLog()
			restartedTT := gui.NewTester(restarted.view, 780, 420)
			if _, found := restartedTT.Find(record); !found {
				t.Fatal("fresh app state did not restore Record data after restart")
			}
			if _, found := restartedTT.Find(stop); found {
				t.Fatal("capture state was persisted across app restart")
			}
		})
	}
}

func TestFileSendStopTooltipShowsOnHover(t *testing.T) {
	for _, language := range []string{"en", "zh-CN"} {
		t.Run(language, func(t *testing.T) {
			app := &internal.App{Settings: config.Settings{Language: language}}
			s := newController(app)
			s.configureLog()
			tt := gui.NewTester(s.view, 780, 420)
			button, found := tt.AccessByRole(gui.RoleButton, s.tr("停止", "Stop"))
			if !found {
				t.Fatal("file-send Stop button is missing")
			}
			tooltip := s.tr("停止文件发送", "Stop file sending")
			tt.Move(button.Bounds.X+button.Bounds.W/2, button.Bounds.Y+button.Bounds.H/2)
			tt.Advance(time.Second)
			if !tt.HasText(tooltip) {
				t.Fatalf("hovering file-send Stop did not show tooltip %q: %q", tooltip, tt.Texts())
			}
		})
	}
}

func TestStatusTooltipShowsCurrentFullMessage(t *testing.T) {
	for _, language := range []string{"zh-CN", "en"} {
		t.Run(language, func(t *testing.T) {
			app := &internal.App{Settings: config.Settings{Language: language}, StatusError: true}
			s := newController(app)
			s.configureLog()
			tt := gui.NewTester(s.view, 760, 420)
			detail := strings.Repeat("COM123456789: permission denied; native_error=5. ", 4) + "ERROR_ACCESS_DENIED"
			messages := []internal.Text{
				{ZH: "打开串口失败: " + detail, EN: "Failed to open port: " + detail},
				{ZH: "发送失败: " + detail, EN: "Send failed: " + detail},
			}
			for i, message := range messages {
				app.Status = message
				expected := message.EN
				if language == "zh-CN" {
					expected = message.ZH
				}
				tt.Frame()
				status, found := tt.Find(s.tr("操作状态", "Operation status"))
				if !found {
					t.Fatal("operation status is missing")
				}
				if i == 0 {
					// Rest in the cell's right padding, outside the text element.
					tt.Move(status.X+status.W+1, status.Y+status.H/2)
					tt.Advance(time.Second)
				}
				tip, found := tt.Find(expected)
				if !found {
					t.Fatalf("hovering the status cell did not show the current full message: %q", expected)
				}
				if tip.H <= status.H || tip.Y+tip.H > status.Y || tip.X < 0 || tip.X+tip.W > 760 {
					t.Errorf("long status tooltip is not fully visible above the footer: tip=%+v status=%+v", tip, status)
				}
			}
		})
	}
}

func TestTimestampedFileName(t *testing.T) {
	now := time.Date(2025, time.March, 4, 5, 6, 7, 0, time.UTC)
	for _, test := range []struct {
		extension string
		want      string
	}{
		{extension: ".txt", want: "SerialGo_20250304_050607.txt"},
		{extension: ".dat", want: "SerialGo_20250304_050607.dat"},
	} {
		t.Run(test.extension, func(t *testing.T) {
			if got := timestampedFileName(now, test.extension); got != test.want {
				t.Fatalf("timestamped filename = %q, want %q", got, test.want)
			}
		})
	}
}

func TestWindowOptionsUseStoredGeometry(t *testing.T) {
	want := config.WindowSettings{X: 720, Y: 169, Width: 1000, Height: 700, Maximized: true, PositionSet: true}
	got := windowOptions(want)
	if got.X != want.X || got.Y != want.Y || got.Width != want.Width || got.Height != want.Height || got.Maximized != want.Maximized || got.StateKey != mainWindowStateKey {
		t.Fatalf("window options = %+v, want stored geometry %+v with state key %q", got, want, mainWindowStateKey)
	}
	if got.MinWidth != minWindowWidth || got.MinHeight != minWindowHeight {
		t.Fatalf("window minimum size = %dx%d, want %dx%d", got.MinWidth, got.MinHeight, minWindowWidth, minWindowHeight)
	}
}

func TestWindowOptionsEnforceMinimumSize(t *testing.T) {
	got := windowOptions(config.WindowSettings{X: 20, Y: 30, Width: 600, Height: 300, PositionSet: true})
	if got.X != 20 || got.Y != 30 || got.Width != minWindowWidth || got.Height != minWindowHeight {
		t.Fatalf("window options did not preserve position and clamp size: %+v", got)
	}
}

func TestWindowOptionsKeepExplicitOriginForStateRestore(t *testing.T) {
	got := windowOptions(config.WindowSettings{Width: 800, Height: 600, PositionSet: true})
	if got.X != 0 || got.Y != 0 || got.StateKey != mainWindowStateKey {
		t.Fatalf("explicit origin options = %+v, want origin with state key %q", got, mainWindowStateKey)
	}
}

func TestMaximizedWindowKeepsNormalBounds(t *testing.T) {
	geometry := config.WindowSettings{X: 720, Y: 169, Width: 1000, Height: 700, PositionSet: true}
	updateWindowGeometry(&geometry, mygo.Rectangle{X: 0, Y: 0, Width: 1920, Height: 1080}, true)
	want := config.WindowSettings{X: 720, Y: 169, Width: 1000, Height: 700, Maximized: true, PositionSet: true}
	if geometry != want {
		t.Fatalf("maximized geometry = %+v, want preserved normal bounds %+v", geometry, want)
	}
	updateWindowGeometry(&geometry, mygo.Rectangle{X: 100, Y: 80, Width: 900, Height: 650}, false)
	want = config.WindowSettings{X: 100, Y: 80, Width: 900, Height: 650, PositionSet: true}
	if geometry != want {
		t.Fatalf("restored geometry = %+v, want %+v", geometry, want)
	}
}

func TestPersistWindowGeometryMarksOnlyChanges(t *testing.T) {
	initial := config.WindowSettings{X: 20, Y: 30, Width: 900, Height: 600, PositionSet: true}
	app := &internal.App{Settings: config.Settings{Window: initial}}
	s := newController(app)
	s.persistWindowGeometry()
	if app.SettingsDirty || app.SettingsRevision != 0 {
		t.Fatal("unchanged geometry marked settings dirty")
	}
	updated := config.WindowSettings{X: 40, Y: 50, Width: 1000, Height: 700, Maximized: true, PositionSet: true}
	s.windowGeometry = updated
	s.persistWindowGeometry()
	if app.Settings.Window != updated || !app.SettingsDirty || app.SettingsRevision != 1 {
		t.Fatalf("updated geometry was not marked for shutdown save: settings=%+v dirty=%v revision=%d", app.Settings.Window, app.SettingsDirty, app.SettingsRevision)
	}
	s.persistWindowGeometry()
	if app.SettingsRevision != 1 {
		t.Fatal("unchanged geometry created another settings revision")
	}
}
