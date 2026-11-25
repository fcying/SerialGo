package ui

import (
	"fmt"
	"slices"

	"github.com/egoist/mygo/ui"
	"github.com/fcying/SerialGo/config"
)

const (
	curveRulesContentMinWidth float32 = 170
	curveDividerWidth         float32 = 5
	curveChartMinWidth        float32 = 160
	curveCompactChartWidth    float32 = 220
)

var (
	curveEditIcon   = ui.MustParseSVG([]byte(`<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round"><path d="m16 3 5 5-12 12-6 1 1-6L16 3Z"/><path d="m13 6 5 5"/></svg>`))
	curveDeleteIcon = ui.MustParseSVG([]byte(`<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round"><path d="M3 6h18M9 6V3h6v3M5 6l1 15h12l1-15M10 10v7M14 10v7"/></svg>`))
)

func curveProfileLabel(index int, name string) string {
	return fmt.Sprintf("%d. %s", index+1, name)
}

func (s *controller) curveDiscardThen(action func()) {
	v := s.curve
	if !v.dirty {
		action()
		return
	}
	v.confirmText = localizedText{zh: "放弃未完成的规则输入?", en: "Discard incomplete rule input?"}
	v.confirmAction = action
	v.confirmOpen = true
}

func (s *controller) deleteCurveProfile() {
	v := s.curve
	index := v.selected
	v.confirmText = localizedText{zh: "删除当前方案?", en: "Delete the current profile?"}
	v.confirmAction = func() {
		err := s.app.DeleteCurveProfile(index)
		if err != nil {
			v.error = dialogError(err)
			return
		}
		profiles := s.settings.CurveProfiles
		next := s.settings.CurveProfile
		v.previewing = false
		v.running = s.app.CurveRunning()
		if !v.running {
			v.paused = false
		}
		v.snapshot.Revision = 0
		s.app.CurveLiveSnapshot(&v.snapshot, &v.liveCache)
		if len(profiles) == 0 {
			v.selected, v.choice = -1, ""
			v.running, v.paused = false, false
			v.draft = curveDraft(config.NewCurveProfile("Voltage"))
			v.dirty = false
			v.refreshCurveColors(false)
			v.error = localizedText{}
		} else {
			v.loadProfile(s.settings, next)
		}
	}
	v.confirmOpen = true
}

func (s *controller) serialCurveView(c *ui.Context) {
	c.SetTheme(lightTheme)
	v := s.curve
	ui.Column(c.Key("serial-curve")).Fill().Padding(wideGap).Gap(controlGap).Background(c.Theme().Background).Children(func() {
		s.curveToolbar(c.Key("toolbar"))
		if v.error.zh != "" || v.error.en != "" {
			ui.Text(c.Key("error"), s.tr(v.error.zh, v.error.en)).FillWidth().TextColor(c.Theme().Danger)
		}
		if s.app.StatusError {
			ui.Text(c.Key("operation-error"), s.statusText()).FillWidth().TextColor(c.Theme().Danger)
		}
		s.curveWorkspace(c.Key("workspace"))
	})
	s.curveConfirmation(c)
}

func (s *controller) setCurveSidebarWidth(width float32) {
	v := s.curve
	v.rulesWidth = config.NormalizeCurveSidebarWidth(width)
	v.rulesVisible = true
	s.app.SetCurveSidebarWidth(v.rulesWidth)
}

func (s *controller) curveWorkspace(c *ui.Context) {
	v := s.curve
	width, _ := c.Size()
	maxRulesWidth := max(float32(0), width-2*wideGap-curveDividerWidth-curveChartMinWidth)
	minRulesWidth := min(curveRulesContentMinWidth, maxRulesWidth)
	clampWidth := func(width float32) float32 { return min(max(width, minRulesWidth), maxRulesWidth) }
	shownWidth := clampWidth(v.rulesWidth)
	if !v.rulesVisible {
		shownWidth = 0
	}
	if v.rulesDragging && v.rulesVisible {
		shownWidth = clampWidth(v.rulesDragWidth)
	}
	workspace := ui.Row(c).FillWidth().Grow(1).MinHeight(0)
	pointerX, _, _ := workspace.PointerPosition()
	workspace.Children(func() {
		list := ui.Box(c.Key("list")).Width(shownWidth).Shrink(0).MinWidth(0).FillHeight().Clip()
		divider := ui.Box(c.Key("divider")).Width(curveDividerWidth).Shrink(0).FillHeight().Focusable().Role(ui.RoleSplitter).
			Label(s.tr("调整曲线列表和图表区域", "Resize curve list and plot areas")).Cursor(ui.CursorResizeEW).Step(10)
		dx, _, held := divider.Dragged()
		wasDragging := v.rulesDragging
		if held {
			if !wasDragging {
				v.rulesGrabOffset = pointerX - shownWidth - dx
				v.rulesDragMoved = false
			}
			v.rulesDragMoved = v.rulesDragMoved || dx != 0
			requested := pointerX - v.rulesGrabOffset
			if v.rulesVisible || v.rulesDragMoved && requested > 0 {
				shownWidth = clampWidth(requested)
				v.rulesVisible = shownWidth > 0
			}
			v.rulesDragWidth = shownWidth
			divider.Focus()
		}
		v.rulesDragging = held
		if wasDragging && !held && v.rulesDragMoved && shownWidth > 0 {
			s.setCurveSidebarWidth(shownWidth)
		}
		left := divider.Shortcut(0, ui.KeyLeft)
		right := divider.Shortcut(0, ui.KeyRight)
		if right || left && v.rulesVisible {
			step := float32(10)
			if left {
				step = -step
			}
			next := clampWidth(shownWidth + step)
			if next > 0 && next != shownWidth {
				s.setCurveSidebarWidth(next)
				shownWidth = next
			}
		}
		minValue := minRulesWidth
		if !v.rulesVisible {
			minValue = 0
		}
		divider.Range(float64(minValue), float64(maxRulesWidth), float64(shownWidth)).FocusRing(divider.FocusVisible())
		color := c.Theme().Border
		if held || divider.Hovered() {
			color = c.Theme().Accent
		}
		divider.Draw(func(p *ui.Painter, r ui.Rect) {
			p.Fill(ui.Rect{X: r.X + (r.W-1)/2, Y: r.Y, W: 1, H: r.H}, color, 0)
		})
		list.Width(shownWidth).Children(func() {
			s.curveEditor(c.Key("editor"), shownWidth)
		})
		chartWidth := width - 2*wideGap - shownWidth - curveDividerWidth
		s.curveChartPane(c.Key("chart-column"), chartWidth)
	})
}

func (s *controller) curveChartPane(c *ui.Context, width float32) {
	v := s.curve
	ui.Column(c).FillWidth().Grow(1).MinWidth(0).FillHeight().Gap(controlGap).Children(func() {
		s.curveChart(c.Key("chart"))
		ui.Row(c.Key("time-window")).FillWidth().Gap(controlGap).AlignItems(ui.Center).Children(func() {
			if width >= curveCompactChartWidth {
				ui.Text(c.Key("span-label"), s.tr("显示范围", "Time range"))
			}
			label := s.tr("曲线时间范围", "Plot time range")
			ui.Select(c.Key("span"), &v.span, []string{"10 s", "60 s", "300 s", s.tr("全部", "All")}).Width(88).Height(controlHeight).
				Label(label).Tooltip(label).OnChange(func() { v.position = 100 })
			ui.Slider(c.Key("history-position"), &v.position, 0, 100).Width(0).MinWidth(0).Grow(1).
				Disabled(!v.paused && v.running).Label(s.tr("历史位置", "History position"))
		})
		status := s.tr("未连接串口", "Port disconnected")
		if s.app.Connected() {
			status = s.tr("串口已连接", "Port connected")
		}
		if v.previewing {
			status = s.tr("日志预览", "Log preview")
		}
		ui.Text(c.Key("stats"), fmt.Sprintf("%s | %s %d | %s %d | %s %d | %s %d", status,
			s.tr("行", "Lines"), v.snapshot.Lines, s.tr("匹配", "Matched"), v.snapshot.Matched,
			s.tr("无效", "Invalid"), v.snapshot.Invalid, s.tr("超长", "Oversize"), v.snapshot.Oversize)).FillWidth().SingleLine().TextColor(c.Theme().TextMuted)
		if v.snapshot.DroppedChunks != 0 {
			ui.Text(c.Key("dropped"), fmt.Sprintf(s.tr("曲线输入丢失: %d 块, %d 字节", "Plot input dropped: %d chunks, %d bytes"),
				v.snapshot.DroppedChunks, v.snapshot.DroppedBytes)).FillWidth().SingleLine().TextColor(c.Theme().Danger)
		}
	})
}

func (s *controller) curveToolbar(c *ui.Context) {
	v := s.curve
	labels := make([]string, len(s.settings.CurveProfiles))
	for i, p := range s.settings.CurveProfiles {
		labels[i] = curveProfileLabel(i, p.Name)
	}
	ui.Row(c).FillWidth().Shrink(0).Gap(controlGap).AlignItems(ui.Center).Children(func() {
		ui.Select(c.Key("profile"), &v.choice, labels).Width(170).Height(controlHeight).Label(s.tr("固件方案", "Firmware profile")).
			OnChange(func() {
				next := slices.Index(labels, v.choice)
				v.choice = ""
				if v.selected >= 0 {
					v.choice = labels[v.selected]
				}
				if next < 0 || next == v.selected {
					return
				}
				s.curveDiscardThen(func() {
					v.loadProfile(s.settings, next)
					s.applyCurveDraft()
				})
			})
		ui.Button(c.Key("new"), s.tr("新增", "New")).Height(controlHeight).Padding(controlPadY, 6).Disabled(len(labels) >= config.CurveProfileLimit).OnClick(func() {
			s.curveDiscardThen(func() {
				v.selected, v.choice = -1, ""
				v.draft = curveDraft(config.NewCurveProfile(fmt.Sprintf("Profile %d", len(labels)+1)))
				s.curveChanged()
			})
		})
		ui.Button(c.Key("delete"), s.tr("删除", "Delete")).Height(controlHeight).Padding(controlPadY, 6).Disabled(v.selected < 0).OnClick(s.deleteCurveProfile)
		ui.Button(c.Key("configuration"), s.tr("配置", "Configuration")).Height(controlHeight).Padding(controlPadY, 6).OnClick(s.openCurveProfileEditor)
		sidebarLabel := s.tr("显示", "Show")
		sidebarHelp := s.tr("显示侧边栏", "Show sidebar")
		if v.rulesVisible {
			sidebarLabel = s.tr("隐藏", "Hide")
			sidebarHelp = s.tr("隐藏侧边栏", "Hide sidebar")
		}
		ui.Button(c.Key("sidebar"), sidebarLabel).Height(controlHeight).Padding(controlPadY, 6).Label(sidebarHelp).Tooltip(sidebarHelp).OnClick(func() {
			v.rulesVisible = !v.rulesVisible
		})
		ui.Box(c.Key("spacer")).Width(0).Grow(1)
		ui.Button(c.Key("test-rules"), s.tr("测试规则", "Test rules")).Height(controlHeight).Padding(controlPadY, 6).OnClick(s.openCurveRuleTest)
		startLabel := s.tr("开始", "Start")
		if v.running {
			startLabel = s.tr("停止", "Stop")
		}
		ui.PrimaryButton(c.Key("start-stop"), startLabel).Height(controlHeight).Padding(controlPadY, 8).Label(s.tr("曲线采集", "Plot acquisition")).OnClick(func() {
			if v.running {
				s.app.StopCurve()
				if !v.paused {
					s.app.CurveLiveSnapshot(&v.snapshot, &v.liveCache)
				}
				v.running = false
			} else {
				s.startCurve()
			}
		})
		pauseLabel := s.tr("暂停查看", "Pause view")
		if v.paused {
			pauseLabel = s.tr("实时跟随", "Follow live")
			if !v.running {
				pauseLabel = s.tr("查看最终快照", "View final snapshot")
			}
		}
		ui.Button(c.Key("pause"), pauseLabel).Height(controlHeight).Padding(controlPadY, 6).Disabled(!v.running && !v.paused).OnClick(func() {
			v.paused = !v.paused
			if !v.paused {
				v.previewing = false
				v.position = 100
				v.snapshot.Revision = 0
				s.app.CurveLiveSnapshot(&v.snapshot, &v.liveCache)
			}
		})
		ui.Button(c.Key("clear"), s.tr("清空", "Clear")).Height(controlHeight).Padding(controlPadY, 6).Label(s.tr("清空曲线", "Clear plot")).OnClick(func() {
			s.app.ClearCurve()
			v.snapshot.Revision = 0
			v.previewing = false
			s.app.CurveLiveSnapshot(&v.snapshot, &v.liveCache)
		})
	})
}

func (s *controller) curveEditor(c *ui.Context, width float32) {
	v := s.curve
	ui.Column(c).Width(max(width, curveRulesContentMinWidth)).Shrink(0).FillHeight().Padding(6).Gap(wideGap).Background(c.Theme().Surface).Children(func() {
		ui.Row(c.Key("list-header")).FillWidth().Gap(controlGap).AlignItems(ui.Center).Children(func() {
			ui.Text(c.Key("list-label"), s.tr("曲线列表", "Curves")).Width(0).Grow(1)
			ui.Button(c.Key("add-channel"), s.tr("+ 添加", "+ Add")).Height(controlHeight).Label(s.tr("添加曲线", "Add curve")).OnClick(func() { s.openCurveRuleEditor(-1) })
		})
		ui.Scroll(c.Key("rules-scroll")).FillWidth().Grow(1).MinHeight(0).Children(func() {
			ui.Column(c.Key("channels")).FillWidth().Gap(controlGap).Children(func() {
				for i := range v.draft.channels {
					s.curveChannelCard(c.Key(i), i)
				}
			})
		})
	})
}

func (s *controller) curveChannelCard(c *ui.Context, index int) {
	v := s.curve
	draft := &v.draft.channels[index]
	name := draft.name
	summary := draft.pattern
	if draft.mode == "regex" {
		summary = s.tr("正则", "Regex") + ": " + summary
	}
	if draft.unit != "" {
		summary += " · " + draft.unit
	}
	color := c.Theme().TextMuted
	if draft.enabled {
		color = v.channelColors[index]
	}
	editLabel := s.tr("编辑曲线 ", "Edit curve ") + name
	deleteLabel := s.tr("删除曲线 ", "Delete curve ") + name
	ui.Row(c).FillWidth().Padding(6).Gap(controlGap).AlignItems(ui.Center).Border(1, c.Theme().Border).Radius(4).Children(func() {
		ui.Box(c.Key("color")).Width(3).Height(36).Shrink(0).Radius(2).Background(color)
		ui.Checkbox(c.Key("enabled"), &draft.enabled, "").Shrink(0).Label(s.tr("启用曲线 ", "Enable curve ") + name).OnChange(s.curveChanged)
		ui.Column(c.Key("description")).Width(0).Grow(1).MinWidth(0).Gap(compactGap).Children(func() {
			ui.Text(c.Key("name"), name).FillWidth().SingleLine().Tooltip(name)
			ui.Text(c.Key("summary"), summary).FillWidth().SingleLine().FontSize(11).TextColor(c.Theme().TextMuted).Tooltip(summary)
		})
		ui.Button(c.Key("edit"), "").Size(controlHeight, controlHeight).Shrink(0).Padding(0).Label(editLabel).Tooltip(editLabel).OnClick(func() { s.openCurveRuleEditor(index) }).Children(func() {
			ui.Icon(c, curveEditIcon).Size(16, 16)
		})
		ui.Button(c.Key("delete"), "").Size(controlHeight, controlHeight).Shrink(0).Padding(0).Label(deleteLabel).Tooltip(deleteLabel).Disabled(len(v.draft.channels) <= 1).OnClick(func() {
			v.confirmText = localizedText{zh: "删除曲线 " + name + "?", en: "Delete curve " + name + "?"}
			v.confirmAction = func() {
				v.draft.channels = slices.Delete(v.draft.channels, index, index+1)
				s.curveChanged()
			}
			v.confirmOpen = true
		}).Children(func() {
			ui.Icon(c, curveDeleteIcon).Size(16, 16)
		})
	})
}

func curveSpanSeconds(span string) float64 {
	switch span {
	case "10 s":
		return 10
	case "60 s":
		return 60
	case "300 s":
		return 300
	default:
		return 0
	}
}
