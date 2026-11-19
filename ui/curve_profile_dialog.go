package ui

import (
	"strconv"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/fcying/SerialGo/config"
	"github.com/fcying/SerialGo/internal"
)

type curveProfileEdit struct {
	window                       *mygo.Window
	name, timePattern, timeScale string
	error                        localizedText
}

func newCurveProfileEdit(draft curveProfileDraft) *curveProfileEdit {
	return &curveProfileEdit{name: draft.name, timePattern: draft.timePattern, timeScale: draft.timeScale}
}

func (s *controller) openCurveProfileEditor() {
	v := s.curve
	if v.profileEditor != nil {
		if v.profileEditor.window != nil {
			v.profileEditor.window.Focus()
		}
		return
	}
	e := newCurveProfileEdit(v.draft)
	v.profileEditor = e
	e.window = mygo.NewWindow(mygo.WindowOptions{
		Title: s.tr("配置", "Configuration"), Parent: s.curveWindow, Modal: true,
		Width: 320, Height: 240, MinWidth: 320, MinHeight: 240,
		Content: ui.View(s.curveProfileEditorView),
	})
	e.window.OnClosed(func() {
		if v.profileEditor == e {
			v.profileEditor = nil
		}
		if s.curveWindow != nil {
			s.curveWindow.Invalidate()
		}
	})
}

func (s *controller) closeCurveProfileEditor() {
	if s.curve == nil || s.curve.profileEditor == nil {
		return
	}
	e := s.curve.profileEditor
	s.curve.profileEditor = nil
	if e.window != nil {
		e.window.Close()
	}
}

func (s *controller) confirmCurveProfileEditor() {
	v := s.curve
	if v == nil || v.profileEditor == nil {
		return
	}
	e := v.profileEditor
	// Validate the common fields independently of unfinished channel edits.
	profile := config.NewCurveProfile(e.name)
	profile.TimePattern = e.timePattern
	var err error
	profile.TimeScale, err = strconv.ParseFloat(e.timeScale, 64)
	if err != nil {
		e.error = localizedText{zh: "时间倍率必须是数字", en: "Time scale must be a number"}
		return
	}
	err = config.ValidateCurveProfiles([]config.CurveProfile{profile}, 0)
	if err == nil && e.timePattern != v.draft.timePattern {
		err = internal.ValidateCurveProfile(profile)
	}
	if err != nil {
		e.error = dialogError(err)
		return
	}
	if e.name == v.draft.name && e.timePattern == v.draft.timePattern && e.timeScale == v.draft.timeScale {
		s.closeCurveProfileEditor()
		return
	}
	candidate := v.draft
	candidate.name, candidate.timePattern, candidate.timeScale = e.name, e.timePattern, e.timeScale
	v.draft, v.dirty = candidate, true
	applied := s.applyCurveDraft()
	if !applied {
		e.error = v.error
		return
	}
	s.closeCurveProfileEditor()
	if s.curveWindow != nil {
		s.curveWindow.Invalidate()
	}
}

func (s *controller) curveProfileEditorView(c *ui.Context) {
	c.SetTheme(lightTheme)
	e := s.curve.profileEditor
	if e == nil {
		return
	}
	timeHelp := s.tr("留空: 使用系统接收时间.\n填写: 提取第 1 个捕获组, 乘以时间倍率得到设备时间 (秒).", "Empty: Use system receive time.\nFilled: Extract capture group 1 and multiply by the time scale for device time (seconds).")
	ui.Column(c.Key("profile-editor")).Fill().Padding(12).Gap(wideGap).Background(c.Theme().Background).Children(func() {
		ui.Scroll(c.Key("fields")).FillWidth().Grow(1).MinHeight(0).Children(func() {
			ui.Column(c.Key("form")).FillWidth().Gap(wideGap).Children(func() {
				ui.Column(c.Key("profile-name")).FillWidth().Gap(compactGap).Children(func() {
					label := s.tr("方案名称", "Profile name")
					ui.Text(c.Key("caption"), label).FontSize(11)
					ui.TextInput(c.Key("input"), &e.name).FillWidth().Height(controlHeight).PaddingY(controlPadY).MaxLength(128).Label(label)
				})
				ui.Column(c.Key("time-pattern")).FillWidth().Gap(compactGap).Children(func() {
					label := s.tr("时间正则 (可选)", "Time regex (optional)")
					ui.Text(c.Key("caption"), label).FontSize(11)
					input := ui.TextInput(c.Key("input"), &e.timePattern).FillWidth().Height(controlHeight).PaddingY(controlPadY).MaxLength(config.MaxCurvePatternBytes).Label(label).Description(timeHelp)
					if input.Hovered() {
						ui.Overlay(c.Key("time-help"), func() {
							ui.Box(c.Key("tip")).Width(260).AttachTo(input, ui.AnchorBottomLeft, ui.AnchorTopLeft).Margin(6, 0, 0, 0).
								Padding(5, 8).Radius(4).Background(c.Theme().Text).TextColor(c.Theme().Surface).IgnoreInput(true).Role(ui.RoleTooltip).
								Children(func() { ui.Text(c.Key("message"), timeHelp).FillWidth().FontSize(11) })
						})
					}
				})
				ui.Column(c.Key("time-scale")).FillWidth().Gap(compactGap).Children(func() {
					label := s.tr("时间倍率", "Time scale")
					ui.Text(c.Key("caption"), label).FontSize(11)
					// Keep the accessible value available while MyGo disables the editor.
					ui.TextInput(c.Key("input"), &e.timeScale).FillWidth().Height(controlHeight).PaddingY(controlPadY).MaxLength(32).Label(label).Value(e.timeScale).Disabled(e.timePattern == "").Description(timeHelp)
				})
			})
		})
		if e.error.en != "" || e.error.zh != "" {
			ui.Scroll(c.Key("error")).FillWidth().Height(40).Children(func() {
				ui.Text(c.Key("message"), s.tr(e.error.zh, e.error.en)).FillWidth().TextColor(c.Theme().Danger)
			})
		}
		ui.Row(c.Key("actions")).FillWidth().Gap(wideGap).Justify(ui.Center).Children(func() {
			ui.Button(c.Key("cancel"), s.tr("取消", "Cancel")).Width(76).Height(28).OnClick(s.closeCurveProfileEditor)
			ui.PrimaryButton(c.Key("ok"), s.tr("确定", "OK")).Width(76).Height(28).OnClick(s.confirmCurveProfileEditor)
		})
	})
	if c.Shortcut(0, ui.KeyEscape) {
		s.closeCurveProfileEditor()
	}
}
