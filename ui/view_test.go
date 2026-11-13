package ui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/fcying/SerialGo/communication/serialport"
	"github.com/fcying/SerialGo/config"
	"github.com/fcying/SerialGo/internal"

	gui "github.com/egoist/mygo/ui"
)

func TestStatusBarKeepsLongTextOnOneLine(t *testing.T) {
	for _, language := range []struct {
		code, serialConfig, redundantStatus, statusLabel string
	}{
		{"zh-CN", "1000000bps,8,1,None", "COM1 已关闭  1000000bps,8,1,None", "操作状态"},
		{"en", "1000000bps,8,1,None", "COM1 Closed  1000000bps,8,1,None", "Operation status"},
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
						s.tr("保存", "Save"),
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
						s.tr("显示发送记录", "Show sent data"),
						s.tr("文本编码", "Text encoding"),
						s.tr("时间戳分包超时 (ms)", "Timestamp packet gap (ms)"),
					} {
						bounds, found := tt.Find(label)
						if !found || bounds.X < 0 || bounds.X+bounds.W > float32(width)+1 || bounds.Y < 0 || bounds.Y+bounds.H > 600 {
							t.Errorf("option %q is missing or clipped: %+v", label, bounds)
						}
					}
				})
			}
		}
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
		for _, width := range []int{789, 800, 811, 1280} {
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
				var baudTextWidth, baudLabelWidth, configLabelWidth float32
				tt := gui.NewTester(func(c *gui.Context) {
					s.view(c)
					baudTextWidth, _ = c.MeasureText(0, gui.Span{Text: "1000000"})
					baudLabelWidth, _ = c.MeasureText(0, gui.Span{Text: s.tr("波特率", "Baud")})
					configLabelWidth, _ = c.MeasureText(0, gui.Span{Text: s.tr("配置", "Config")})
				}, width, 420)
				expectedPickerWidth := baudTextWidth + baudPickerChromeWidth
				expectedConnectionWidth := baudLabelWidth + 2*serialPanelRowGap + expectedPickerWidth + configLabelWidth + serialConfigButtonChromeWidth + serialPanelChromeWidth
				expectedSendWidth := float32(width) - 2*2 - 3*4 - expectedConnectionWidth - 156 - 184
				groups := []struct {
					panel         string
					expectedWidth float32
					controls      []string
				}{
					{s.tr("连接控制", "Connection panel"), expectedConnectionWidth, []string{
						s.tr("端口号", "Port"), s.tr("波特率", "Baud"), s.tr("波特率", "Baud rate"), s.tr("打开", "Open"), s.tr("串口配置", "Port configuration"),
					}},
					{s.tr("接收显示控制", "Receive display controls"), 156, []string{
						s.tr("HEX 显示", "HEX display"), s.tr("时间戳", "Timestamp"), s.tr("自动换行", "CRLF"),
						s.tr("自动滚动", "Auto scroll"), s.tr("清除接收窗口", "Clear receive log"), s.tr("选项", "Options"),
					}},
					{s.tr("文件操作", "File operations"), 184, []string{
						s.tr("打开文件", "Open file"), s.tr("发送文件", "Send file"), s.tr("保存数据", "Save data"), s.tr("录制数据", "Record data"),
						s.tr("文件分段延时 (ms)", "File chunk delay (ms)"), s.tr("停止", "Stop"),
					}},
					{s.tr("发送面板", "Send panel"), expectedSendWidth, []string{
						s.tr("HEX 发送", "HEX send"), s.tr("定时发送", "Repeat"), s.tr("定时发送间隔 (ms)", "Repeat interval (ms)"),
						s.tr("发送区", "Send area"), s.tr("多条发送面板", "Preset panel"), s.tr("发送", "Send"), s.tr("清空发送区", "Clear send area"),
					}},
				}
				var previousRight, firstY, firstHeight float32
				for i, group := range groups {
					panel, found := tt.Find(group.panel)
					if !found || panel.X < previousRight || panel.X+panel.W > float32(width)+1 || panel.H > 96 ||
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
				baudLabel, baudLabelFound := tt.Find(s.tr("波特率", "Baud"))
				baudPicker, baudPickerFound := tt.AccessByRole(gui.RolePopUpButton, s.tr("波特率", "Baud rate"))
				portPicker, portPickerFound := tt.AccessByRole(gui.RolePopUpButton, s.tr("端口号", "Port"))
				if !baudLabelFound || !baudPickerFound || !portPickerFound {
					t.Fatal("baud label, baud selector, or port selector is missing")
				}
				labelCenter := baudLabel.Y + baudLabel.H/2
				pickerCenter := baudPicker.Bounds.Y + baudPicker.Bounds.H/2
				if labelCenter < pickerCenter-1 || labelCenter > pickerCenter+1 ||
					baudLabel.X+baudLabel.W > baudPicker.Bounds.X || baudPicker.Bounds.W < baudTextWidth ||
					baudPicker.Bounds.X+baudPicker.Bounds.W > connectionPanel.X+connectionPanel.W {
					t.Errorf("baud label and selector do not fit together: label=%+v picker=%+v required=%v panel=%+v", baudLabel, baudPicker.Bounds, baudTextWidth, connectionPanel)
				}
				valueCenter := baudText.X + baudText.W/2
				selectorCenter := baudPicker.Bounds.X + (baudPicker.Bounds.W-baudPickerArrowSize-baudPickerGap)/2
				if valueCenter < selectorCenter-0.5 || valueCenter > selectorCenter+0.5 {
					t.Errorf("selected baud value is not centered: text=%+v picker=%+v", baudText, baudPicker.Bounds)
				}
				if baudPicker.Bounds.W < expectedPickerWidth-0.01 || baudPicker.Bounds.W > expectedPickerWidth+0.01 {
					t.Errorf("baud selector is not fitted to the seven-digit value: bounds=%+v expectedWidth=%.1f", baudPicker.Bounds, expectedPickerWidth)
				}
				configButton, configFound := tt.AccessByRole(gui.RoleButton, s.tr("串口配置", "Port configuration"))
				indicator, indicatorFound := tt.Find(s.tr("串口已关闭", "Serial port closed"))
				open, openFound := tt.AccessByRole(gui.RoleButton, s.tr("打开", "Open"))
				if !configFound || !indicatorFound || !openFound {
					t.Fatal("connection action row is missing Config, status light, or Open")
				}
				portRight := portPicker.Bounds.X + portPicker.Bounds.W
				configRight := configButton.Bounds.X + configButton.Bounds.W
				if portRight < configRight-0.01 || portRight > configRight+0.01 {
					t.Errorf("port selector right edge must align with Config: port=%+v config=%+v", portPicker.Bounds, configButton.Bounds)
				}
				baudCenter := baudPicker.Bounds.Y + baudPicker.Bounds.H/2
				configCenter := configButton.Bounds.Y + configButton.Bounds.H/2
				if configButton.Bounds.Y != baudPicker.Bounds.Y || configCenter < baudCenter-1 || configCenter > baudCenter+1 ||
					configButton.Bounds.X < baudPicker.Bounds.X+baudPicker.Bounds.W-0.01 ||
					configButton.Bounds.X+configButton.Bounds.W > connectionPanel.X+connectionPanel.W {
					t.Errorf("Config must fit after the baud selector on the same row: baud=%+v config=%+v panel=%+v", baudPicker.Bounds, configButton.Bounds, connectionPanel)
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
				interval, _ := tt.Find(s.tr("定时发送间隔 (ms)", "Repeat interval (ms)"))
				for _, label := range []string{s.tr("HEX 发送", "HEX send"), s.tr("定时发送", "Repeat"), s.tr("多条发送面板", "Preset panel")} {
					control, _ := tt.Find(label)
					distance := control.Y + control.H/2 - interval.Y - interval.H/2
					if distance < -1 || distance > 1 || control.Y+control.H > input.Y {
						t.Errorf("send header control %q wrapped or overlaps input: control=%+v interval=%+v input=%+v", label, control, interval, input)
					}
				}
				hex, hexFound := tt.AccessByRole(gui.RoleCheckBox, s.tr("HEX 发送", "HEX send"))
				repeat, repeatFound := tt.AccessByRole(gui.RoleCheckBox, s.tr("定时发送", "Repeat"))
				if !hexFound || !repeatFound {
					t.Fatal("send format controls are missing")
				}
				groupGap := repeat.Bounds.X - hex.Bounds.X - hex.Bounds.W
				if groupGap < 1 || groupGap > 11 {
					t.Errorf("send group gap exceeds its elastic range: gap=%v", groupGap)
				}
				presets, _ := tt.Find(s.tr("多条发送面板", "Preset panel"))
				if groupGap < 9 && presets.X-interval.X-interval.W > 24 {
					t.Errorf("send gap shrank despite available space before Presets: gap=%v interval=%+v presets=%+v", groupGap, interval, presets)
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
	crlf, crlfFound := tt.Find("CRLF")
	autoScroll, autoScrollFound := tt.Find("Auto scroll")
	clear, clearFound := tt.Find("Clear receive log")
	options, optionsFound := tt.Find("Options")
	if !hexFound || !timestampFound || !crlfFound || !autoScrollFound || !clearFound || !optionsFound {
		t.Fatalf("receive controls are missing: HEX=%+v timestamp=%+v CRLF=%+v auto-scroll=%+v clear=%+v options=%+v", hex, timestamp, crlf, autoScroll, clear, options)
	}
	hexCenterY := hex.Y + hex.H/2
	timestampCenterY := timestamp.Y + timestamp.H/2
	crlfCenterY := crlf.Y + crlf.H/2
	autoScrollCenterY := autoScroll.Y + autoScroll.H/2
	clearCenterY := clear.Y + clear.H/2
	optionsCenterY := options.Y + options.H/2
	if hexCenterY < timestampCenterY-1 || hexCenterY > timestampCenterY+1 || crlfCenterY < autoScrollCenterY-1 || crlfCenterY > autoScrollCenterY+1 || crlfCenterY <= hexCenterY+1 {
		t.Errorf("four checkboxes are not arranged as two rows: HEX=%+v timestamp=%+v CRLF=%+v auto-scroll=%+v", hex, timestamp, crlf, autoScroll)
	}
	if clearCenterY < optionsCenterY-1 || clearCenterY > optionsCenterY+1 || clearCenterY <= crlfCenterY+1 || clear.X >= options.X {
		t.Errorf("clear and options buttons do not share the row below the checkboxes: clear=%+v options=%+v CRLF=%+v", clear, options, crlf)
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
