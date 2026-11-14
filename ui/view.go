package ui

import (
	"fmt"
	"math"

	"github.com/fcying/SerialGo/config"

	"github.com/egoist/mygo/ui"
)

var (
	dataBits  = []string{"5", "6", "7", "8"}
	parities  = []string{"None", "Odd", "Even", "Mark", "Space"}
	stopBits  = []string{"1", "1.5", "2"}
	encodings = []string{"UTF-8", "GB18030"}
)

const (
	presetReceiveShare             float32 = 45
	presetPaneMinWidth             float32 = 440
	presetNoteMinWidth             float32 = 120
	receiveOptionsFirstColumnWidth float32 = 72
	minReceiveHeight                       = 100
	// One send-header row, the editor/actions, spacing, padding, and borders.
	minSendWorkspaceHeight = 90
	minWindowWidth         = 800
)

func (s *controller) view(c *ui.Context) {
	c.SetTheme(lightTheme)
	width, height := c.Size()
	// Exclude the footer, outer padding/gap, and divider from the pane budget.
	available := max(height-27, minReceiveHeight+minSendWorkspaceHeight)
	defaultShare := (available - minSendWorkspaceHeight) / available * 100
	if !s.receiveSplitAdjusted {
		s.receiveSplit = defaultShare
	}
	share := min(max(s.receiveSplit, minReceiveHeight/available*100), defaultShare)
	s.log.viewportHeight = available * share / 100
	connectionWidth := s.connectionRowContentWidth(c) + panelChromeWidth
	receiveOptionsWidth := s.receiveOptionsPanelWidth(c)
	fileWidth := float32(184)
	ui.Column(c.Key("desktop")).Fill().Padding(2).Gap(compactGap).Background(c.Theme().Background).Children(func() {
		split := ui.SplitVerticalPercent(c.Key("receive-send-split"), &share, func() {
			if s.settings.ShowPresets {
				splitWidth := max(width-4, presetPaneMinWidth+1)
				maxReceiveShare := (splitWidth - 1 - presetPaneMinWidth) * 100 / (splitWidth - 1)
				presetShare := min(s.presetSplit, maxReceiveShare)
				presetLayout := ui.SplitPercent(c.Key("split"), &presetShare, func() { s.receivePane(c) }, func() { s.presetPane(c) }).Fill().MinHeight(100).SplitReset(presetReceiveShare)
				if presetLayout.Changed() {
					s.presetSplit = min(presetShare, maxReceiveShare)
				}
			} else {
				s.receivePane(c)
			}
		}, func() {
			ui.Column(c.Key("send-workspace")).Fill().Children(func() {
				ui.Row(c.Key("lower")).FillWidth().Grow(1).MinHeight(0).Gap(controlGap).AlignItems(ui.Start).Children(func() {
					s.serialPanel(c, connectionWidth)
					s.receiveOptionsPanel(c, receiveOptionsWidth)
					s.filePanel(c, fileWidth)
					s.sendPanel(c)
				})
			})
		}).FillWidth().Grow(1).MinHeight(minReceiveHeight + minSendWorkspaceHeight + 1).SplitReset(defaultShare).Label(s.tr("调整接收和发送区域", "Resize receive and send areas"))
		if split.Changed() {
			s.receiveSplit = share
			s.receiveSplitAdjusted = true
		}
		s.statusBar(c)
	})
	s.optionsDialog(c)
	s.receiveFontDialog(c)
	s.portSettingsDialog(c)
	s.customBaudDialog(c)
	s.baudDeleteDialog(c)
	s.presetDeleteDialog(c)
}

func (s *controller) receivePane(c *ui.Context) {
	p := s.settings
	area := ui.TextAreaBuffer(c.Key("receive"), &s.log.text).Fill().MinHeight(80).Grow(1).ReadOnly(true).
		Background(textAreaBackground).Font(config.NormalizeReceiveFont(p.ReceiveFont)).
		FontSize(float32(config.NormalizeReceiveFontSize(p.ReceiveFontSize)) * 4 / 3).
		TextColor(ui.Hex(config.NormalizeReceiveFontColor(p.ReceiveFontColor))).
		TrackScroll(&s.log.scroll).Label(s.tr("接收窗口", "Receive log"))
	if s.log.scrollToEnd {
		selectionStart, selectionEnd := area.TextSelection()
		if selectionStart != selectionEnd {
			// Keep the pending follow request until the selection is cleared.
			return
		}
		// Buffer updates reveal the old caret unless it follows the new tail.
		end := s.log.text.Len()
		area.SetTextSelection(end, end)
		s.log.scroll.Y = math.MaxFloat32
		s.log.scrollToEnd = false
		s.log.preserveScroll = false
	} else if s.log.preserveScroll {
		area.SetTextSelection(s.log.scrollAnchor, s.log.scrollAnchor)
		s.log.preserveScroll = false
	}
}

func (s *controller) filePanel(c *ui.Context, width float32) {
	ui.Column(c.Key("file-panel")).Width(width).Basis(width).Shrink(0).FillHeight().Gap(controlGap).Padding(4, panelPaddingX).Radius(4).Background(panelBackground).Border(panelBorderWidth, c.Theme().Border).
		Label(s.tr("文件操作", "File operations")).Children(func() {
		ui.Row(c.Key("file-actions")).FillWidth().Gap(controlGap).Children(func() {
			ui.Row(c.Key("open-file-cell")).Width(0).MinWidth(0).Grow(1).Children(func() {
				ui.Button(c.Key("open-file"), s.tr("打开文件", "Open file")).FillWidth().MinWidth(0).Height(controlHeight).Padding(controlPadY, 4).Disabled(s.dialogBusy).OnClick(s.chooseFile)
			})
			ui.Row(c.Key("send-file-tooltip")).Width(0).MinWidth(0).Grow(1).Tooltip(s.app.FilePath).Children(func() {
				ui.Button(c.Key("send-file"), s.tr("发送文件", "Send file")).FillWidth().MinWidth(0).Height(controlHeight).Padding(controlPadY, 4).
					Disabled(!s.app.CanSendFile()).OnClick(s.app.SendFile)
			})
		})
		ui.Row(c.Key("file-record-actions")).FillWidth().Gap(controlGap).Children(func() {
			busy := s.dialogBusy || s.app.FileBusy
			ui.Row(c.Key("save-receive-cell")).Width(0).MinWidth(0).Grow(1).Children(func() {
				ui.Button(c.Key("save-data"), s.tr("保存数据", "Save data")).FillWidth().MinWidth(0).Height(controlHeight).Padding(controlPadY, 4).
					Disabled(busy).OnClick(s.saveLog)
			})
			if s.app.CapturePath != "" {
				ui.Row(c.Key("stop-capture-cell")).Width(0).MinWidth(0).Grow(1).Children(func() {
					ui.Button(c.Key("stop-capture"), s.tr("停止录制", "Stop recording")).FillWidth().MinWidth(0).Height(controlHeight).Padding(controlPadY, 0).
						Disabled(busy).OnClick(s.app.StopCapture)
				})
			} else {
				ui.Row(c.Key("start-capture-cell")).Width(0).MinWidth(0).Grow(1).Children(func() {
					ui.Button(c.Key("start-capture"), s.tr("录制数据", "Record data")).FillWidth().MinWidth(0).Height(controlHeight).Padding(controlPadY, 4).
						Disabled(busy || !s.app.Connected()).OnClick(s.startCapture)
				})
			}
		})
		ui.Row(c.Key("file-delay-options")).FillWidth().Gap(controlGap).AlignItems(ui.Center).Children(func() {
			ui.Text(c.Key("file-delay-label"), s.tr("延时", "Delay")).Label(s.tr("文件延时", "File delay"))
			ui.TextInput(c.Key("file-delay"), &s.settings.FileDelay).Width(intervalWidth).Height(controlHeight).PaddingY(controlPadY).TextAlign(ui.Center).
				Label(s.tr("文件分段延时 (ms)", "File chunk delay (ms)")).OnChange(s.app.SettingsChanged)
			ui.Text(c.Key("file-delay-ms"), "ms")
			ui.Row(c.Key("stop-send-tooltip")).Width(0).MinWidth(0).Grow(1).Tooltip(s.tr("停止文件发送", "Stop file sending")).Children(func() {
				ui.Button(c.Key("stop-send"), s.tr("停止", "Stop")).FillWidth().MinWidth(0).Height(controlHeight).Padding(controlPadY, 4).
					Disabled(!s.app.Sending()).OnClick(s.app.StopSending)
			})
		})
	})
}

func (s *controller) optionsDialog(c *ui.Context) {
	ui.Modal(c.Key("options-dialog"), &s.optionsOpen, func() {
		p := s.settings
		ui.Column(c.Key("preferences")).Width(360).Gap(sectionGap).Children(func() {
			ui.Text(c.Key("window-heading"), s.tr("窗口与语言", "Window and language")).FontWeight(600)
			ui.Checkbox(c.Key("top"), &s.alwaysOnTop, s.tr("置顶", "On top")).OnChange(s.applyAlwaysOnTop)
			ui.Row(c.Key("language-options")).Gap(groupGap).AlignItems(ui.Center).Children(func() {
				ui.Text(c.Key("language-label"), s.tr("语言", "Language"))
				ui.Button(c.Key("language"), s.tr("English", "简体中文")).Height(controlHeight).PaddingY(controlPadY).
					Label("语言 / Language").Tooltip(s.tr("切换到英文界面", "Switch to Simplified Chinese")).OnClick(s.toggleLanguage)
			})
			ui.Text(c.Key("display-heading"), s.tr("数据显示", "Data display")).FontWeight(600)
			ui.Row(c.Key("encoding-options")).Gap(tightGap).AlignItems(ui.Center).Children(func() {
				ui.Text(c.Key("encoding-label"), s.tr("编码", "Encoding"))
				ui.Select(c.Key("encoding"), &p.Encoding, encodings).Width(88).Height(controlHeight).PaddingY(controlPadY).
					Label(s.tr("文本编码", "Text encoding")).OnChange(s.applyDisplaySettings)
			})
			displayBufferOptions(c.Key("buffer-limit-options"), p, s.applyDisplaySettings)
			s.receiveFontButton(c)
			ui.Row(c.Key("packet-options")).Gap(groupGap).AlignItems(ui.Center).Children(func() {
				ui.Text(c.Key("packet-label"), s.tr("时间戳分包超时", "Timestamp packet gap"))
				ui.TextInput(c.Key("packet-gap"), &s.packetGapDraft).Width(55).Height(controlHeight).PaddingY(controlPadY).
					Label(s.tr("时间戳分包超时 (ms)", "Timestamp packet gap (ms)")).OnChange(s.packetGapChanged)
				ui.Text(c.Key("gap-ms"), "ms")
			})
			if s.packetGapError.zh != "" || s.packetGapError.en != "" {
				ui.Text(c.Key("packet-gap-error"), s.tr(s.packetGapError.zh, s.packetGapError.en)).FillWidth().TextColor(c.Theme().Danger)
			}
			ui.Row(c.Key("actions")).FillWidth().Justify(ui.End).Children(func() {
				ui.Button(c.Key("close"), s.tr("关闭", "Close")).Width(76).Height(28).OnClick(func() { s.optionsOpen = false })
			})
		})
	}).Label(s.tr("选项", "Options"))
}

func (s *controller) connectionRowContentWidth(c *ui.Context) float32 {
	baudWidth := s.mainBaudPickerWidth(c)
	if s.connectionContentWidth == 0 || s.connectionLanguage != s.settings.Language || s.connectionBaudWidth != baudWidth {
		settingsLabelWidth, _ := c.MeasureText(0, ui.Span{Text: s.tr("设置", "Settings")})
		s.connectionContentWidth = baudWidth + controlGap + settingsLabelWidth + serialConfigButtonChromeWidth
		s.connectionLanguage = s.settings.Language
		s.connectionBaudWidth = baudWidth
	}
	return s.connectionContentWidth
}

func (s *controller) receiveOptionsPanelWidth(c *ui.Context) float32 {
	if s.receiveOptionsContentWidth == 0 || s.receiveOptionsLanguage != s.settings.Language {
		autoScrollTextWidth, _ := c.MeasureText(0, ui.Span{Text: s.tr("自动滚动", "Auto scroll")})
		autoScrollCheckboxWidth := autoScrollTextWidth + c.Theme().Space(4) + c.Theme().Space(2)
		s.receiveOptionsContentWidth = receiveOptionsFirstColumnWidth + controlGap + autoScrollCheckboxWidth + panelChromeWidth
		s.receiveOptionsLanguage = s.settings.Language
	}
	return s.receiveOptionsContentWidth
}

func (s *controller) serialPanel(c *ui.Context, width float32) {
	p := &s.settings.Port
	open := s.app.Connected()
	color := ui.Hex("#D32F2F")
	state := s.tr("串口已关闭", "Serial port closed")
	if open {
		color = ui.Hex("#2E7D32")
		state = s.tr("串口已打开", "Serial port open")
	}
	portRowWidth := s.connectionRowContentWidth(c)
	ui.Column(c.Key("serial-panel")).Width(width).Basis(width).Shrink(0).FillHeight().Gap(controlGap).Padding(4, panelPaddingX).Radius(4).Background(panelBackground).Border(panelBorderWidth, c.Theme().Border).
		Label(s.tr("连接控制", "Connection panel")).Children(func() {
		ui.Row(c.Key("port-row")).Width(portRowWidth).Shrink(0).Gap(controlGap).AlignItems(ui.Center).Children(func() {
			s.portPicker(c.Key("port-name"), s.app.RefreshPorts)
		})
		ui.Row(c.Key("baud-row")).FillWidth().Gap(controlGap).AlignItems(ui.Center).Children(func() {
			s.baudPicker(c.Key("baud"), false, controlHeight).Shrink(0)
			ui.Button(c.Key("port-settings"), s.tr("设置", "Settings")).Height(controlHeight).PaddingY(controlPadY).PaddingX(serialConfigPaddingX).Border(serialConfigBorderWidth, c.Theme().Border).
				Label(s.tr("串口设置", "Port settings")).Tooltip(s.tr("配置数据位, 校验位, 停止位及 RTS/DTR", "Configure data bits, parity, stop bits, and RTS/DTR")).OnClick(s.openPortSettings)
		})
		ui.Row(c.Key("serial-actions")).FillWidth().Gap(controlGap).Children(func() {
			ui.Box(c.Key("port-indicator")).Size(8, 8).Shrink(0).Radius(4).Background(color).Label(state).Tooltip(state)
			label := s.tr("打开", "Open")
			if open {
				label = s.tr("关闭", "Close")
			}
			ui.PrimaryButton(c.Key("open-port"), label).Width(0).Grow(1).MinWidth(60).Height(controlHeight).PaddingY(controlPadY).
				Disabled(s.app.PortBusy || !open && (p.Name == "" || p.Baud == "")).OnClick(s.app.TogglePort)
		})
	})
}

func (s *controller) receiveOptionsPanel(c *ui.Context, width float32) {
	p := s.settings
	firstColumnWidth := receiveOptionsFirstColumnWidth
	ui.Column(c.Key("receive-options")).Width(width).Basis(width).Shrink(0).FillHeight().Gap(controlGap).Padding(4, panelPaddingX).Radius(4).Background(panelBackground).Border(panelBorderWidth, c.Theme().Border).
		Label(s.tr("接收显示控制", "Receive display controls")).Children(func() {
		ui.Row(c.Key("receive-display-options")).FillWidth().Height(controlHeight).Gap(controlGap).AlignItems(ui.Center).Children(func() {
			ui.Checkbox(c.Key("rx-hex"), &p.ReceiveHex, s.tr("HEX 显示", "HEX")).Width(firstColumnWidth).Shrink(0).Label(s.tr("HEX 显示", "HEX display")).OnChange(s.applyDisplaySettings)
			ui.Checkbox(c.Key("timestamp"), &p.Timestamp, s.tr("时间戳", "Timestamp")).
				Tooltip(s.tr("按空闲间隔分包并显示时间戳", "Split after the idle gap and show timestamps")).OnChange(s.applyDisplaySettings)
		})
		ui.Row(c.Key("receive-send-options")).FillWidth().Height(controlHeight).Gap(controlGap).AlignItems(ui.Center).Children(func() {
			ui.Checkbox(c.Key("show-sent"), &p.ShowSent, s.tr("显示发送", "Show sent")).Width(firstColumnWidth).Shrink(0).
				Label(s.tr("显示发送", "Show sent")).Tooltip(s.tr("在接收窗口显示本机发送的数据, 不会再次发送", "Show outgoing data in the receive log; does not transmit it again")).OnChange(s.applyDisplaySettings)
			ui.Checkbox(c.Key("auto-scroll"), &s.autoScroll, s.tr("自动滚动", "Auto scroll")).OnChange(s.autoScrollChanged)
		})
		ui.Row(c.Key("receive-actions")).FillWidth().Gap(controlGap).Children(func() {
			ui.Button(c.Key("clear-receive"), s.tr("清空", "Clear")).Width(0).MinWidth(0).Grow(1).Height(controlHeight).PaddingY(controlPadY).
				Label(s.tr("清除接收窗口", "Clear receive log")).OnClick(s.log.clear)
			ui.Button(c.Key("options"), s.tr("选项", "Options")).Width(0).MinWidth(0).Grow(1).Height(controlHeight).PaddingY(controlPadY).OnClick(s.openOptions)
		})
	})
}

func (s *controller) sendPanel(c *ui.Context) {
	p := s.settings
	presetLabel := "+"
	presetTooltip := s.tr("展开多条发送", "Expand preset panel")
	if p.ShowPresets {
		presetLabel = "-"
		presetTooltip = s.tr("收起多条发送", "Collapse preset panel")
	}
	ui.Column(c.Key("send-panel")).Width(0).Grow(1).MinWidth(0).FillHeight().Gap(controlGap).Padding(4, panelPaddingX).Radius(4).Background(panelBackground).Border(panelBorderWidth, c.Theme().Border).
		Label(s.tr("发送面板", "Send panel")).Children(func() {
		ui.Row(c.Key("send-heading")).FillWidth().Height(controlHeight).Gap(controlGap).AlignItems(ui.Center).Children(func() {
			ui.Checkbox(c.Key("tx-hex"), &p.SendHex, "HEX").Shrink(0).Label(s.tr("HEX 发送", "HEX send")).OnChange(s.app.SettingsChanged)
			ui.Checkbox(c.Key("crlf"), &p.CRLF, "CRLF").Shrink(0).
				Label("CRLF").Tooltip(s.tr("发送内容末尾追加回车换行", "Append CRLF to sent text")).OnChange(s.app.SettingsChanged)
			ui.Row(c.Key("repeat-options")).Grow(1).Shrink(0).Gap(controlGap).AlignItems(ui.Center).Children(func() {
				ui.Checkbox(c.Key("repeat"), &s.app.Repeat, s.tr("定时", "Repeat")).Label(s.tr("定时发送", "Repeat"))
				ui.TextInput(c.Key("interval"), &p.Interval).Width(intervalWidth).Height(controlHeight).Padding(controlPadY, 4).TextAlign(ui.Center).
					Label(s.tr("定时发送间隔 (ms)", "Repeat interval (ms)")).Disabled(!s.app.Repeat).OnChange(s.app.SettingsChanged)
				ui.Text(c.Key("interval-ms"), "ms")
			})
			ui.Button(c.Key("show-presets"), presetLabel).Width(presetToggleWidth).MinWidth(0).Shrink(0).Height(controlHeight).Padding(controlPadY, 4).
				Label(s.tr("多条发送面板", "Preset panel")).Tooltip(presetTooltip).OnClick(func() {
				s.settings.ShowPresets = !s.settings.ShowPresets
				s.app.SettingsChanged()
				s.app.SaveUsedSettings()
			})
		})
		ui.Row(c.Key("send-input-row")).FillWidth().Grow(1).MinHeight(0).Gap(controlGap).Children(func() {
			key := "send-text"
			if p.SendHex {
				key = "send-hex"
			}
			ui.TextArea(c.Key(key), s.app.SendValue()).Width(0).Grow(1).MinWidth(0).FillHeight().MinHeight(40).MaxLength(200_000).
				Background(textAreaBackground).Font("monospace").Label(s.tr("发送区", "Send area")).Placeholder(s.tr("输入发送内容...", "Enter data to send...")).OnChange(s.app.SettingsChanged)
			ui.Column(c.Key("send-actions")).Width(80).Shrink(0).FillHeight().Gap(controlGap).Children(func() {
				ui.PrimaryButton(c.Key("send"), s.tr("发送", "Send")).Width(80).Height(controlHeight).PaddingY(controlPadY).
					Disabled(!s.app.CanSend()).OnClick(func() { s.app.SendText(*s.app.SendValue()) })
				ui.Button(c.Key("clear-send"), s.tr("清空发送", "Clear send")).Width(80).Height(controlHeight).PaddingY(controlPadY).
					Label(s.tr("清空发送区", "Clear send area")).Tooltip(s.tr("清空发送区", "Clear send area")).OnClick(s.app.ClearSend)
			})
		})
	})
}

func (s *controller) presetPane(c *ui.Context) {
	ui.Column(c.Key("presets")).Fill().MinWidth(presetPaneMinWidth).Gap(compactGap).Padding(2).Clip().Children(func() {
		ui.Row(c.Key("preset-actions")).FillWidth().Wrap().Gap(groupGap).Shrink(0).AlignItems(ui.Center).Children(func() {
			ui.Button(c.Key("add-preset"), s.tr("新增", "Add")).Height(controlHeight).PaddingY(controlPadY).Disabled(s.app.Sending() || len(s.settings.Presets) >= config.PresetCount).OnClick(s.addPreset)
			ui.PrimaryButton(c.Key("run-presets"), s.tr("发送勾选", "Send checked")).Height(controlHeight).PaddingY(controlPadY).
				Label(s.tr("按顺序发送已勾选条目", "Send checked presets in order")).Disabled(!s.app.CanSend() || len(s.settings.Presets) == 0).OnClick(func() { s.app.SendPresets(-1) })
			ui.Checkbox(c.Key("loop-presets"), &s.app.LoopPresets, s.tr("循环", "Loop")).
				Tooltip(s.tr("持续循环发送已勾选条目", "Loop checked presets continuously"))
		})
		if len(s.settings.Presets) == 0 {
			ui.Text(c.Key("empty-presets"), s.tr("暂无条目, 点击新增添加", "No presets. Use Add to create one.")).FillWidth().TextColor(c.Theme().TextMuted)
		}
		ui.List(c.Key(s.presetGeneration), &s.presetList, len(s.settings.Presets), func(i int) {
			preset := &s.settings.Presets[i]
			ui.Column(c.Key(i)).FillWidth().Gap(tightGap).Padding(2).Children(func() {
				ui.Row(c.Key("preset-options")).FillWidth().Gap(controlGap).AlignItems(ui.Center).Children(func() {
					ui.Checkbox(c.Key("preset-enabled"), &preset.Enabled, "").Width(18).Shrink(0).
						Label(s.tr(fmt.Sprintf("选择第 %d 条发送内容", i+1), fmt.Sprintf("Select preset %d for multi-send", i+1))).OnChange(s.app.SettingsChanged)
					ui.Text(c.Key("preset-index"), fmt.Sprintf("%02d", i+1)).Width(22).Shrink(0).TextColor(c.Theme().TextMuted)
					ui.TextInput(c.Key("preset-name"), &preset.Label).Width(0).MinWidth(presetNoteMinWidth).Grow(1).Height(controlHeight).PaddingY(controlPadY).
						Placeholder(s.tr("备注", "Note")).Label(s.tr("条目备注", "Preset note")).Tooltip(preset.Label).OnChange(s.app.SettingsChanged)
					ui.Row(c.Key("preset-format")).Shrink(0).Gap(tightGap).AlignItems(ui.Center).Children(func() {
						ui.Checkbox(c.Key("preset-hex"), &preset.Hex, "HEX").
							Label(s.tr(fmt.Sprintf("第 %d 条 HEX", i+1), fmt.Sprintf("Preset %d HEX", i+1))).OnChange(s.app.SettingsChanged)
						ui.Checkbox(c.Key("preset-crlf"), &preset.CRLF, "CRLF").
							Label(s.tr(fmt.Sprintf("第 %d 条 CRLF", i+1), fmt.Sprintf("Preset %d CRLF", i+1))).OnChange(s.app.SettingsChanged)
						ui.Text(c.Key("preset-interval-prefix"), s.tr("间隔", "Interval")).SingleLine().Shrink(0)
						ui.TextInput(c.Key("preset-delay"), &preset.Interval).Width(55).Height(controlHeight).PaddingY(controlPadY).
							Label(s.tr("条目发送间隔 (ms)", "Preset interval (ms)")).OnChange(s.app.SettingsChanged)
						ui.Text(c.Key("preset-ms"), "ms")
						ui.Button(c.Key("preset-delete"), s.tr("删除", "Delete")).Height(controlHeight).PaddingY(controlPadY).PaddingX(3).
							Label(s.tr(fmt.Sprintf("删除第 %d 条", i+1), fmt.Sprintf("Delete preset %d", i+1))).Disabled(s.app.Sending()).OnClick(func() { s.openPresetDelete(i) })
					})
				})
				ui.Row(c.Key("preset-data-row")).FillWidth().Gap(controlGap).AlignItems(ui.Center).Children(func() {
					ui.TextArea(c.Key("preset-data"), &preset.Data).Width(0).MinWidth(0).Grow(1).MinHeight(controlHeight).Height(controlHeight).PaddingY(controlPadY).
						Font("monospace").MaxLength(4096).Placeholder(s.tr("发送内容", "Data to send")).Label(s.tr("条目内容", "Preset data")).OnChange(s.app.SettingsChanged)
					ui.PrimaryButton(c.Key("preset-send"), s.tr("发送", "Send")).Height(controlHeight).PaddingY(controlPadY).
						Label(s.tr(fmt.Sprintf("发送第 %d 条", i+1), fmt.Sprintf("Send preset %d", i+1))).Disabled(!s.app.CanSend()).OnClick(func() { s.app.SendPresets(i) })
				})
			})
		}).FillWidth().Grow(1).MinHeight(0).Gap(controlGap).Label(s.tr("发送条目", "Send presets"))
	})
}

func (s *controller) statusBar(c *ui.Context) {
	p := &s.settings.Port
	serialConfig := formatSerialConfig(p.Baud, p.DataBits, p.Parity, p.StopBits)
	statusText := s.statusText()
	color := c.Theme().TextMuted
	if s.app.StatusError {
		color = c.Theme().Danger
	}
	ui.Row(c.Key("status-bar")).FillWidth().Shrink(0).Height(20).AlignItems(ui.Center).Children(func() {
		statusCell(c, "tx-count", fmt.Sprintf("S: %d", s.app.Tx))
		ui.Text(c.Key("tx-rx-separator"), "|").TextColor(c.Theme().TextMuted).Shrink(0)
		statusCell(c, "rx-count", fmt.Sprintf("R: %d", s.app.Rx))
		ui.Text(c.Key("rx-reset-separator"), "|").TextColor(c.Theme().TextMuted).Shrink(0)
		ui.Button(c.Key("reset-counts"), s.tr("清零", "Reset")).Height(20).PaddingY(1).PaddingX(8).Margin(0, 8, 0, 8).
			Label(s.tr("收发计数清零", "Reset counters")).Tooltip(s.tr("收发计数清零", "Reset transmit/receive counters")).OnClick(s.app.ClearCounts)
		ui.Text(c.Key("reset-config-separator"), "|").TextColor(c.Theme().TextMuted).Shrink(0)
		ui.Text(c.Key("serial-config"), serialConfig).Shrink(0).Padding(0, 8).
			SingleLine().Tooltip(serialConfig)
		ui.Text(c.Key("config-status-separator"), "|").TextColor(c.Theme().TextMuted).Shrink(0)
		ui.Row(c.Key("status-message")).Width(0).Grow(1).MinWidth(0).FillHeight().Padding(0, 8).AlignItems(ui.Center).Tooltip(statusText).Children(func() {
			ui.Text(c.Key("message"), statusText).Width(0).Grow(1).MinWidth(0).SingleLine().Ellipsis("...").TextColor(color).Label(s.tr("操作状态", "Operation status"))
		})
	})
}

func formatSerialConfig(baud, dataBits, parity, stopBits string) string {
	parityCode := parity
	switch parity {
	case "None":
		parityCode = "N"
	case "Odd":
		parityCode = "O"
	case "Even":
		parityCode = "E"
	case "Mark":
		parityCode = "M"
	case "Space":
		parityCode = "S"
	}
	return fmt.Sprintf("%s bps · %s%s%s", baud, dataBits, parityCode, stopBits)
}

func statusCell(c *ui.Context, id, value string) {
	ui.Text(c.Key(id), value).Shrink(0).Padding(0, 8).SingleLine().Tooltip(value)
}
