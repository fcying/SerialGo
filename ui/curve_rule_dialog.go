package ui

import (
	"fmt"
	"runtime"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/fcying/SerialGo/config"
	"github.com/fcying/SerialGo/internal"
)

var curveRegexEngine = "Go regexp / RE2 (" + runtime.Version() + ")"

type curveRuleEdit struct {
	window  *mygo.Window
	index   int
	channel curveChannelDraft
	picker  ui.Color
	error   localizedText
}

func newCurveRuleEdit(draft curveProfileDraft, index int) *curveRuleEdit {
	e := &curveRuleEdit{index: index}
	if index >= 0 {
		e.channel = draft.channels[index]
	} else {
		e.channel = curveChannelDraft{name: fmt.Sprintf("CH%d", len(draft.channels)+1), mode: "prefix", scale: "1", offset: "0", enabled: true}
	}
	colorIndex := index
	if colorIndex < 0 {
		colorIndex = len(draft.channels)
	}
	defaultColor := config.DefaultCurveColor(colorIndex)
	if e.channel.color == "" {
		e.channel.color = defaultColor
	}
	rgba, valid := config.ParseCurveColor(e.channel.color)
	if !valid {
		rgba, _ = config.ParseCurveColor(defaultColor)
	}
	e.picker = curveColor(rgba)
	return e
}

func (e *curveRuleEdit) colorHexChanged() {
	rgba, valid := config.ParseCurveColor(e.channel.color)
	if valid {
		e.picker = curveColor(rgba)
	}
}

func (e *curveRuleEdit) colorPickerChanged() {
	color := e.picker
	if color.A == 255 {
		e.channel.color = fmt.Sprintf("#%02X%02X%02X", color.R, color.G, color.B)
	} else {
		e.channel.color = fmt.Sprintf("#%02X%02X%02X%02X", color.R, color.G, color.B, color.A)
	}
}

func (s *controller) curveRuleEditorTitle() string {
	e := s.curve.ruleEditor
	if e.index < 0 {
		return s.tr("添加曲线", "Add curve")
	}
	return s.tr("编辑曲线", "Edit curve")
}

func (s *controller) openCurveRuleEditor(index int) {
	v := s.curve
	if v.ruleEditor != nil {
		if v.ruleEditor.window != nil {
			v.ruleEditor.window.Focus()
		}
		return
	}
	e := newCurveRuleEdit(v.draft, index)
	v.ruleEditor = e
	e.window = mygo.NewWindow(mygo.WindowOptions{
		Title: s.curveRuleEditorTitle(), Parent: s.curveWindow, Modal: true,
		Width: 420, Height: 440, MinWidth: 380, MinHeight: 320,
		Content: ui.View(s.curveRuleEditorView),
	})
	e.window.OnClosed(func() {
		if v.ruleEditor == e {
			v.ruleEditor = nil
		}
		if s.curveWindow != nil {
			s.curveWindow.Invalidate()
		}
	})
}

func (s *controller) closeCurveRuleEditor() {
	if s.curve == nil || s.curve.ruleEditor == nil {
		return
	}
	e := s.curve.ruleEditor
	s.curve.ruleEditor = nil
	if e.window != nil {
		e.window.Close()
	}
}

func (s *controller) confirmCurveRuleEditor() {
	v := s.curve
	e := v.ruleEditor
	rgba, valid := config.ParseCurveColor(e.channel.color)
	if !valid {
		e.error = localizedText{
			zh: "曲线颜色无效, 请使用 #RRGGBB 或 #RRGGBBAA",
			en: "Invalid curve color; use #RRGGBB or #RRGGBBAA",
		}
		return
	}
	e.picker = curveColor(rgba)
	e.colorPickerChanged()
	// Validate only the edited rule, so unrelated invalid stored rules remain editable.
	draft := curveDraft(config.NewCurveProfile("rule"))
	draft.channels[0] = e.channel
	candidate := curveViewState{draft: draft}
	profile, err := candidate.profile()
	if err == nil {
		_, err = internal.PreviewCurve(profile, "")
	}
	if err != nil {
		e.error = dialogError(err)
		return
	}
	changed := false
	if e.index < 0 {
		v.draft.channels = append(v.draft.channels, e.channel)
		changed = true
	} else if v.draft.channels[e.index] != e.channel {
		v.draft.channels[e.index] = e.channel
		changed = true
	}
	if changed {
		s.curveChanged()
	}
	s.closeCurveRuleEditor()
}

func curveRuleInput(c *ui.Context, label string, value *string) {
	ui.Column(c).FillWidth().Gap(compactGap).Children(func() {
		ui.Text(c.Key("caption"), label).FontSize(11)
		ui.TextInput(c.Key("input"), value).FillWidth().Height(controlHeight).PaddingY(controlPadY).MaxLength(4096).Label(label)
	})
}

func (s *controller) curveRuleEditorView(c *ui.Context) {
	c.SetTheme(lightTheme)
	e := s.curve.ruleEditor
	if e == nil {
		return
	}
	ui.Column(c.Key("rule-editor")).Fill().Padding(12).Gap(wideGap).Background(c.Theme().Background).Children(func() {
		ui.Scroll(c.Key("fields")).FillWidth().Grow(1).MinHeight(0).Children(func() {
			ui.Column(c.Key("form")).FillWidth().Gap(wideGap).Children(func() {
				draft := &e.channel
				curveRuleInput(c.Key("channel-name"), s.tr("曲线名称", "Curve name"), &draft.name)
				ui.Row(c.Key("enabled-color")).FillWidth().Gap(wideGap).AlignItems(ui.Center).Children(func() {
					ui.Checkbox(c.Key("enabled"), &draft.enabled, s.tr("启用", "Enabled"))
					ui.Text(c.Key("color-label"), s.tr("颜色", "Color"))
					ui.ColorWell(c.Key("color-picker"), &e.picker).Width(48).Height(controlHeight).
						Label(s.tr("曲线颜色", "Curve color")).OnChange(e.colorPickerChanged)
					ui.TextInput(c.Key("color-hex"), &draft.color).Width(0).MinWidth(0).Grow(1).Height(controlHeight).PaddingY(controlPadY).
						MaxLength(4096).Label(s.tr("曲线颜色 HEX (#RRGGBB 或 #RRGGBBAA)", "Curve color HEX (#RRGGBB or #RRGGBBAA)")).OnChange(e.colorHexChanged)
				})
				prefix, regex := s.tr("前缀", "Prefix"), s.tr("正则 (Go/RE2)", "Regex (Go/RE2)")
				mode := prefix
				if draft.mode == "regex" {
					mode = regex
				}
				ui.Select(c.Key("mode"), &mode, []string{prefix, regex}).FillWidth().Height(controlHeight).Label(s.tr("匹配方式", "Match mode")).OnChange(func() {
					draft.mode = "prefix"
					if mode == regex {
						draft.mode = "regex"
					}
				})
				patternLabel := s.tr("数字前缀", "Numeric prefix")
				if draft.mode == "regex" {
					patternLabel = s.tr("正则 (第一个捕获组为数值)", "Regex (group 1 is the value)")
				}
				curveRuleInput(c.Key("pattern"), patternLabel, &draft.pattern)
				ui.Row(c.Key("conversion")).FillWidth().Gap(wideGap).Children(func() {
					ui.Column(c.Key("scale")).Width(0).Grow(1).Children(func() { curveRuleInput(c.Key("input"), s.tr("倍率", "Scale"), &draft.scale) })
					ui.Column(c.Key("offset")).Width(0).Grow(1).Children(func() { curveRuleInput(c.Key("input"), s.tr("偏移", "Offset"), &draft.offset) })
					ui.Column(c.Key("unit")).Width(0).Grow(1).Children(func() { curveRuleInput(c.Key("input"), s.tr("单位", "Unit"), &draft.unit) })
				})
				ui.Text(c.Key("conversion-help"), s.tr("换算: 值 × 倍率 + 偏移. 正则使用第一个捕获组作为数值.", "Value = input × scale + offset. Regex uses capture group 1 as the number.")).FillWidth().FontSize(11)
				if e.channel.mode == "regex" {
					ui.Text(c.Key("regex-engine"), curveRegexEngine).FillWidth().FontSize(11).TextColor(c.Theme().TextMuted)
					ui.Text(c.Key("regex-limits"), s.tr("不支持前后查找, 反向引用 (如 \\1) 或 \\C.", "No lookaround, backreferences (such as \\1), or \\C.")).FillWidth().FontSize(11).TextColor(c.Theme().TextMuted)
				}
			})
		})
		if e.error.en != "" || e.error.zh != "" {
			ui.Text(c.Key("error"), s.tr(e.error.zh, e.error.en)).FillWidth().TextColor(c.Theme().Danger)
		}
		ui.Row(c.Key("actions")).FillWidth().Gap(wideGap).Justify(ui.End).Children(func() {
			ui.Button(c.Key("cancel"), s.tr("取消", "Cancel")).Width(76).Height(28).OnClick(s.closeCurveRuleEditor)
			ui.PrimaryButton(c.Key("ok"), s.tr("确定", "OK")).Width(76).Height(28).OnClick(s.confirmCurveRuleEditor)
		})
	})
	if c.Shortcut(0, ui.KeyEscape) {
		s.closeCurveRuleEditor()
	}
}
