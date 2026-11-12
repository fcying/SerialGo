package ui

import (
	"fmt"
	"math"
	"runtime"
	"strconv"

	"github.com/fcying/SerialGo/config"

	"github.com/egoist/mygo/ui"
)

var (
	dataBits   = []string{"5", "6", "7", "8"}
	parities   = []string{"None", "Odd", "Even", "Mark", "Space"}
	stopBits   = []string{"1", "1.5", "2"}
	encodings  = []string{"UTF-8", "GB18030"}
	lightTheme = compactTheme()
)

const (
	controlHeight = 22
	// Leave enough vertical room for a complete line of text at this height.
	controlPadY            = 2
	defaultReceiveSplit    = 85
	minReceiveHeight       = 100
	minSendWorkspaceHeight = 128
)

func compactTheme() *ui.Theme {
	theme := ui.LightTheme()
	theme.Background = ui.Hex("#F0F0F0")
	theme.Border = ui.Hex("#B8B8B8")
	theme.Radius = 2
	theme.Spacing = 3
	theme.FontSize = 12
	if runtime.GOOS == "windows" {
		theme.Font = "Microsoft YaHei UI"
		theme.FontSize = 13
	}
	return theme
}

func (s *controller) view(c *ui.Context) {
	c.SetTheme(lightTheme)
	if s.receiveSplit == 0 {
		s.receiveSplit = defaultReceiveSplit
	}
	_, height := c.Size()
	// Exclude the footer, outer padding/gap, and divider from the pane budget.
	available := max(height-27, minReceiveHeight+minSendWorkspaceHeight)
	share := min(max(s.receiveSplit, minReceiveHeight/available*100), (available-minSendWorkspaceHeight)/available*100)
	ui.Column(c.Key("desktop")).Fill().Padding(2).Gap(2).Background(c.Theme().Background).Children(func() {
		split := ui.SplitVerticalPercent(c.Key("receive-send-split"), &share, func() {
			if s.settings.ShowPresets {
				ui.SplitPercent(c.Key("split"), &s.presetSplit, func() { s.receivePane(c) }, func() { s.presetPane(c) }).Fill().MinHeight(100).SplitReset(64)
			} else {
				s.receivePane(c)
			}
		}, func() {
			ui.Column(c.Key("send-workspace")).Fill().Gap(4).Children(func() {
				s.fileBar(c)
				ui.Row(c.Key("lower")).FillWidth().Grow(1).MinHeight(0).Gap(8).AlignItems(ui.Start).Children(func() {
					s.serialPanel(c)
					s.sendPanel(c)
				})
			})
		}).FillWidth().Grow(1).MinHeight(minReceiveHeight + minSendWorkspaceHeight + 1).SplitReset(defaultReceiveSplit).Label(s.tr("调整接收和发送区域", "Resize receive and send areas"))
		if split.Changed() {
			s.receiveSplit = share
		}
		s.statusBar(c)
	})
	s.optionsDialog(c)
	s.portSettingsDialog(c)
	s.customBaudDialog(c)
	s.baudDeleteDialog(c)
	s.presetDeleteDialog(c)
}

func (s *controller) receivePane(c *ui.Context) {
	area := ui.TextAreaBuffer(c.Key("receive"), &s.log.text).Fill().MinHeight(80).Grow(1).ReadOnly(true).
		Background(ui.Hex("#FFFFFF")).Font("monospace").TrackScroll(&s.log.scroll).Label(s.tr("接收窗口", "Receive log"))
	if s.log.scrollToEnd {
		// Buffer updates reveal the old caret unless it follows the new tail.
		end := s.log.text.Len()
		area.SetTextSelection(end, end)
		s.log.scroll.Y = math.MaxFloat32
		s.log.scrollToEnd = false
	}
}

func (s *controller) fileBar(c *ui.Context) {
	p := s.settings
	presetLabel := s.tr("扩展", "Presets")
	if s.settings.ShowPresets {
		presetLabel = s.tr("收起", "Hide")
	}
	ui.Row(c.Key("file-bar")).FillWidth().Gap(3).Shrink(0).AlignItems(ui.Center).Children(func() {
		ui.Button(c.Key("clear-receive"), s.tr("清除窗口", "Clear log")).Height(controlHeight).PaddingY(controlPadY).OnClick(s.log.clear)
		ui.Button(c.Key("open-file"), s.tr("打开文件", "Open file")).Height(controlHeight).PaddingY(controlPadY).Disabled(s.dialogBusy).OnClick(s.chooseFile)
		ui.TextInput(c.Key("file-path"), &s.app.FilePath).Width(0).Grow(1).MinWidth(0).Height(controlHeight).PaddingY(controlPadY).
			Label(s.tr("待发送文件", "File to send")).Placeholder(s.tr("待发送文件", "File to send")).Tooltip(s.app.FilePath).ReadOnly(true)
		ui.Button(c.Key("send-file"), s.tr("发送文件", "Send file")).Height(controlHeight).PaddingY(controlPadY).Disabled(!s.app.CanSendFile()).OnClick(s.app.SendFile)
		ui.Row(c.Key("file-delay-options")).Shrink(0).Gap(3).AlignItems(ui.Center).Children(func() {
			ui.Text(c.Key("file-delay-label"), s.tr("文件延时", "File delay"))
			ui.TextInput(c.Key("file-delay"), &p.FileDelay).Width(55).Height(controlHeight).PaddingY(controlPadY).Label(s.tr("文件分段延时 (ms)", "File chunk delay (ms)")).OnChange(s.app.SettingsChanged)
			ui.Text(c.Key("file-delay-ms"), "ms")
		})
		ui.Button(c.Key("stop-send"), s.tr("停止", "Stop")).Height(controlHeight).PaddingY(controlPadY).Disabled(!s.app.Sending()).OnClick(s.app.StopSending)
		ui.Button(c.Key("show-presets"), presetLabel).Height(controlHeight).PaddingY(controlPadY).Label(s.tr("多条发送面板", "Preset panel")).OnClick(func() {
			s.settings.ShowPresets = !s.settings.ShowPresets
			s.app.SettingsChanged()
			s.app.SaveUsedSettings()
		})
	})
}

func (s *controller) optionsDialog(c *ui.Context) {
	ui.Modal(c.Key("options-dialog"), &s.optionsOpen, func() {
		p := s.settings
		ui.Column(c.Key("preferences")).Width(360).Gap(10).Children(func() {
			ui.Text(c.Key("window-heading"), s.tr("窗口与语言", "Window and language")).FontWeight(600)
			ui.Checkbox(c.Key("top"), &s.alwaysOnTop, s.tr("置顶", "On top")).OnChange(s.applyAlwaysOnTop)
			ui.Row(c.Key("language-options")).Gap(6).AlignItems(ui.Center).Children(func() {
				ui.Text(c.Key("language-label"), s.tr("语言", "Language"))
				ui.Button(c.Key("language"), s.tr("English", "简体中文")).Height(controlHeight).PaddingY(controlPadY).
					Label("语言 / Language").Tooltip(s.tr("切换到英文界面", "Switch to Simplified Chinese")).OnClick(s.toggleLanguage)
			})
			ui.Text(c.Key("display-heading"), s.tr("数据显示", "Data display")).FontWeight(600)
			ui.Row(c.Key("encoding-options")).Gap(3).AlignItems(ui.Center).Children(func() {
				ui.Text(c.Key("encoding-label"), s.tr("编码", "Encoding"))
				ui.Select(c.Key("encoding"), &p.Encoding, encodings).Width(88).Height(controlHeight).PaddingY(controlPadY).
					Label(s.tr("文本编码", "Text encoding")).OnChange(s.applyDisplaySettings)
			})
			displayBufferOptions(c.Key("buffer-limit-options"), p, s.applyDisplaySettings)
			ui.Checkbox(c.Key("show-sent"), &p.ShowSent, s.tr("显示发送记录", "Show sent data")).
				Tooltip(s.tr("在接收窗口显示本机发送的数据, 不会再次发送", "Show outgoing data in the receive log; does not send it again")).OnChange(s.applyDisplaySettings)
			ui.Row(c.Key("packet-options")).Gap(6).AlignItems(ui.Center).Children(func() {
				ui.Checkbox(c.Key("timestamp"), &p.Timestamp, s.tr("时间戳和分包显示", "Timestamp and packets")).OnChange(s.applyDisplaySettings)
				ui.Text(c.Key("packet-label"), s.tr("分包超时", "Packet gap"))
				ui.TextInput(c.Key("packet-gap"), &p.PacketGap).Width(55).Height(controlHeight).PaddingY(controlPadY).Label(s.tr("分包超时 (ms)", "Packet gap (ms)")).Disabled(!p.Timestamp).OnChange(s.displaySettingsChanged)
				ui.Text(c.Key("gap-ms"), "ms")
			})
			ui.Row(c.Key("actions")).FillWidth().Justify(ui.End).Children(func() {
				ui.Button(c.Key("close"), s.tr("关闭", "Close")).Width(76).Height(28).OnClick(func() { s.optionsOpen = false })
			})
		})
	}).Label(s.tr("选项", "Options"))
}

func (s *controller) serialPanel(c *ui.Context) {
	p := &s.settings.Port
	open := s.app.Connected()
	capture := s.tr("接收存文件", "Capture")
	if s.app.CapturePath != "" {
		capture = s.tr("停止接收", "Stop capture")
	}
	ui.Column(c.Key("serial")).Shrink(0).Gap(3).Padding(3).Border(1, c.Theme().Border).Children(func() {
		ui.Row(c.Key("port-row")).FillWidth().Gap(4).AlignItems(ui.Center).Children(func() {
			color := ui.Hex("#D32F2F")
			state := s.tr("串口已关闭", "Serial port closed")
			if open {
				color = ui.Hex("#2E7D32")
				state = s.tr("串口已打开", "Serial port open")
			}
			ui.Box(c.Key("port-indicator")).Size(8, 8).Shrink(0).Radius(4).Background(color).Label(state).Tooltip(state)
			s.portPicker(c.Key("port-name"), s.app.RefreshPorts)
		})
		ui.Row(c.Key("baud-row")).FillWidth().Gap(4).Justify(ui.SpaceBetween).AlignItems(ui.Center).Children(func() {
			ui.Text(c.Key("baud-label"), s.tr("波特率", "Baud"))
			s.baudPicker(c.Key("baud"), false, controlHeight).Width(76).Basis(76).Grow(0).Shrink(0)
			ui.Button(c.Key("port-settings"), s.tr("串口设置", "Settings")).Height(controlHeight).PaddingY(controlPadY).PaddingX(5).Label(s.tr("串口设置", "Port settings")).OnClick(s.openPortSettings)
			label := s.tr("打开", "Open")
			if open {
				label = s.tr("关闭", "Close")
			}
			ui.Button(c.Key("open-port"), label).Height(controlHeight).PaddingY(controlPadY).PaddingX(5).Disabled(s.app.PortBusy || !open && (p.Name == "" || p.Baud == "")).OnClick(s.app.TogglePort)
		})
		ui.Row(c.Key("receive-actions")).FillWidth().Gap(3).AlignItems(ui.Center).Children(func() {
			ui.Button(c.Key("save-receive"), s.tr("保存数据", "Save log")).Height(controlHeight).PaddingY(controlPadY).PaddingX(3).Disabled(s.dialogBusy || s.app.FileBusy).OnClick(s.saveLog)
			ui.Button(c.Key("capture"), capture).Height(controlHeight).PaddingY(controlPadY).PaddingX(3).Disabled(s.dialogBusy || s.app.FileBusy || !s.app.Connected()).OnClick(s.toggleCapture)
			ui.Button(c.Key("options"), s.tr("选项", "Options")).Height(controlHeight).PaddingY(controlPadY).PaddingX(3).OnClick(func() { s.optionsOpen = true })
		})
		ui.Row(c.Key("hex-options")).FillWidth().Gap(3).AlignItems(ui.Center).Children(func() {
			ui.Checkbox(c.Key("rx-hex"), &s.settings.ReceiveHex, s.tr("HEX 显示", "HEX display")).OnChange(s.applyDisplaySettings)
			ui.Checkbox(c.Key("tx-hex"), &s.settings.SendHex, s.tr("HEX 发送", "HEX send")).OnChange(s.app.SettingsChanged)
		})
	})
}

func (s *controller) sendPanel(c *ui.Context) {
	p := s.settings
	ui.Column(c.Key("send-panel")).Width(0).Grow(1).MinWidth(0).FillHeight().Gap(6).Padding(4).Border(1, c.Theme().Border).Children(func() {
		ui.Row(c.Key("send-format-options")).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Row(c.Key("repeat-options")).Shrink(0).Gap(3).AlignItems(ui.Center).Children(func() {
				ui.Checkbox(c.Key("repeat"), &s.app.Repeat, s.tr("定时发送", "Repeat"))
				ui.TextInput(c.Key("interval"), &p.Interval).Width(55).Height(controlHeight).PaddingY(controlPadY).Label(s.tr("定时发送间隔 (ms)", "Repeat interval (ms)")).Disabled(!s.app.Repeat).OnChange(s.app.SettingsChanged)
				ui.Text(c.Key("interval-ms"), s.tr("ms/次", "ms"))
			})
			ui.Checkbox(c.Key("crlf"), &p.CRLF, s.tr("回车换行", "CRLF")).OnChange(s.app.SettingsChanged)
			ui.Button(c.Key("send"), s.tr("发送", "Send")).Width(64).Height(controlHeight).PaddingY(controlPadY).Disabled(!s.app.CanSend()).OnClick(func() { s.app.SendText(*s.app.SendValue()) })
			ui.Button(c.Key("clear-send"), s.tr("清空发送", "Clear send")).Height(controlHeight).PaddingY(controlPadY).
				Label(s.tr("清空发送区", "Clear send area")).Tooltip(s.tr("清空发送区", "Clear send area")).OnClick(s.app.ClearSend)
		})
		key, hint := "send-text", s.tr("输入文本", "Enter text")
		if p.SendHex {
			key, hint = "send-hex", s.tr("输入十六进制字节, 如 01 03 00 FF", "Enter HEX bytes, e.g. 01 03 00 FF")
		}
		ui.TextArea(c.Key(key), s.app.SendValue()).FillWidth().Grow(1).MinHeight(40).MaxLength(200_000).
			Background(ui.Hex("#FFFFFF")).Font("monospace").Label(s.tr("发送区", "Send area")).Placeholder(hint).OnChange(s.app.SettingsChanged)
	})
}

func (s *controller) presetPane(c *ui.Context) {
	ui.Column(c.Key("presets")).Fill().MinWidth(0).Gap(3).Padding(3).Clip().Border(1, c.Theme().Border).Children(func() {
		ui.Row(c.Key("preset-actions")).FillWidth().Wrap().Gap(6).Shrink(0).AlignItems(ui.Center).Children(func() {
			ui.Button(c.Key("add-preset"), s.tr("新增", "Add")).Height(controlHeight).PaddingY(controlPadY).Disabled(s.app.Sending() || len(s.settings.Presets) >= config.PresetCount).OnClick(s.addPreset)
			ui.Button(c.Key("run-presets"), s.tr("发送勾选", "Send checked")).Height(controlHeight).PaddingY(controlPadY).Disabled(!s.app.CanSend() || len(s.settings.Presets) == 0).OnClick(func() { s.app.SendPresets(-1) })
			ui.Checkbox(c.Key("loop-presets"), &s.app.LoopPresets, s.tr("循环", "Loop"))
		})
		if len(s.settings.Presets) == 0 {
			ui.Text(c.Key("empty-presets"), s.tr("暂无条目, 点击新增添加", "No presets. Use Add to create one.")).FillWidth().TextColor(c.Theme().TextMuted)
		}
		ui.List(c.Key(s.presetGeneration), &s.presetList, len(s.settings.Presets), func(i int) {
			preset := &s.settings.Presets[i]
			ui.Column(c.Key(i)).FillWidth().Gap(2).Padding(3).Border(1, c.Theme().Border).Children(func() {
				ui.Row(c.Key("preset-top")).FillWidth().Gap(3).AlignItems(ui.Center).Children(func() {
					ui.Checkbox(c.Key("preset-enabled"), &preset.Enabled, strconv.Itoa(i+1)).OnChange(s.app.SettingsChanged)
					ui.TextArea(c.Key("preset-data"), &preset.Data).Width(0).MinWidth(0).Grow(1).MinHeight(controlHeight).Height(controlHeight).PaddingY(controlPadY).
						Font("monospace").MaxLength(4096).Placeholder(s.tr("发送内容", "Data to send")).Label(s.tr("条目内容", "Preset data")).OnChange(s.app.SettingsChanged)
					ui.Button(c.Key("preset-send"), s.tr("发送", "Send")).Height(controlHeight).PaddingY(controlPadY).Disabled(!s.app.CanSend()).OnClick(func() { s.app.SendPresets(i) })
				})
				ui.Row(c.Key("preset-options")).FillWidth().Wrap().Gap(6).AlignItems(ui.Center).Children(func() {
					ui.TextInput(c.Key("preset-name"), &preset.Label).Width(0).MinWidth(150).Grow(1).Height(controlHeight).PaddingY(controlPadY).Placeholder(s.tr("备注", "Note")).Label(s.tr("条目备注", "Preset note")).Tooltip(preset.Label).OnChange(s.app.SettingsChanged)
					ui.Row(c.Key("preset-format")).Shrink(0).Gap(3).AlignItems(ui.Center).Children(func() {
						ui.Checkbox(c.Key("preset-hex"), &preset.Hex, "HEX").OnChange(s.app.SettingsChanged)
						ui.Checkbox(c.Key("preset-crlf"), &preset.CRLF, "CRLF").OnChange(s.app.SettingsChanged)
						ui.TextInput(c.Key("preset-delay"), &preset.Interval).Width(55).Height(controlHeight).PaddingY(controlPadY).Label(s.tr("条目发送间隔 (ms)", "Preset interval (ms)")).OnChange(s.app.SettingsChanged)
						ui.Text(c.Key("preset-ms"), "ms")
						ui.Button(c.Key("preset-delete"), s.tr("删除", "Delete")).Height(controlHeight).PaddingY(controlPadY).PaddingX(3).
							Label(s.tr(fmt.Sprintf("删除第 %d 条", i+1), fmt.Sprintf("Delete preset %d", i+1))).Disabled(s.app.Sending()).OnClick(func() { s.openPresetDelete(i) })
					})
				})
			})
		}).FillWidth().Grow(1).MinHeight(0).Gap(4).Label(s.tr("发送条目", "Send presets"))
	})
}

func (s *controller) statusBar(c *ui.Context) {
	p := &s.settings.Port
	state := s.tr("已关闭", "Closed")
	if s.app.Connected() {
		state = s.tr("已打开", "Open")
	}
	color := c.Theme().TextMuted
	if s.app.StatusError {
		color = c.Theme().Danger
	}
	ui.Row(c.Key("status-bar")).FillWidth().Shrink(0).Height(20).Gap(1).AlignItems(ui.Center).Children(func() {
		statusCell(c, "tx-count", fmt.Sprintf("S: %d", s.app.Tx), 75)
		statusCell(c, "rx-count", fmt.Sprintf("R: %d", s.app.Rx), 75)
		ui.Button(c.Key("reset-counts"), s.tr("清计数", "Reset")).Height(20).PaddingY(1).PaddingX(5).
			Label(s.tr("收发计数清零", "Reset counters")).Tooltip(s.tr("收发计数清零", "Reset transmit/receive counters")).OnClick(s.app.ClearCounts)
		statusCell(c, "port-state", fmt.Sprintf("%s %s  %sbps,%s,%s,%s", p.Name, state, p.Baud, p.DataBits, p.StopBits, p.Parity), 205)
		ui.Row(c.Key("status-message")).Width(0).Grow(1).MinWidth(0).FillHeight().Padding(0, 3).Border(1, c.Theme().Border).AlignItems(ui.Center).Children(func() {
			ui.Text(c.Key("message"), s.statusText()).Width(0).Grow(1).MinWidth(0).SingleLine().Ellipsis("...").TextColor(color).Label(s.tr("操作状态", "Operation status"))
		})
	})
}

func statusCell(c *ui.Context, id, value string, width float32) {
	ui.Row(c.Key(id)).Width(width).FillHeight().Padding(0, 3).Border(1, c.Theme().Border).AlignItems(ui.Center).Tooltip(value).Children(func() {
		ui.Text(c.Key("value"), value).Width(0).Grow(1).MinWidth(0).SingleLine().Ellipsis("...")
	})
}
