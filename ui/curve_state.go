package ui

import (
	"fmt"
	"slices"
	"strconv"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/fcying/SerialGo/config"
	"github.com/fcying/SerialGo/internal"
	"github.com/fcying/SerialGo/internal/i18n"
)

type curveChannelDraft struct {
	name, mode, pattern, scale, offset, unit, color string
	enabled                                         bool
}

type curveProfileDraft struct {
	name, timePattern, timeScale string
	channels                     []curveChannelDraft
}

type curveViewState struct {
	selected               int
	choice                 string
	draft                  curveProfileDraft
	dirty, running, paused bool
	previewing             bool
	connected              bool
	status                 internal.Text
	statusError            bool
	previewText            string
	snapshot               internal.CurveSnapshot
	span                   string
	position               float64
	rulesWidth             float32 // Preferred width, independent of viewport limits and visibility.
	rulesVisible           bool
	rulesDragWidth         float32
	rulesDragMoved         bool
	rulesDragging          bool
	rulesGrabOffset        float32
	error                  localizedText
	confirmOpen            bool
	confirmText            localizedText
	confirmAction          func()
	ruleEditor             *curveRuleEdit
	profileEditor          *curveProfileEdit
	testWindow             *mygo.Window
	channelColors          []ui.Color
	paths                  []ui.Path
	geometry               curveGeometry
}

func newCurveViewState(settings *config.Settings) *curveViewState {
	v := &curveViewState{selected: -1, span: "60 s", position: 100, rulesWidth: config.NormalizeCurveSidebarWidth(settings.CurveSidebarWidth), rulesVisible: true,
		previewText: "[ADC] VBAT=3712 mV\n[ADC] IBAT=1024 ua\n[ADC] VBAT=3680 mV\n[ADC] IBAT=926 ua\n[ADC] VBAT=3740 mV\n[ADC] IBAT=1524 ua\n"}
	if len(settings.CurveProfiles) > 0 {
		index := min(max(settings.CurveProfile, 0), len(settings.CurveProfiles)-1)
		v.loadProfile(settings, index)
	} else {
		v.draft = curveDraft(config.NewCurveProfile("Voltage"))
		v.refreshCurveColors(false)
	}
	return v
}

func curveDraft(profile config.CurveProfile) curveProfileDraft {
	draft := curveProfileDraft{name: profile.Name, timePattern: profile.TimePattern,
		timeScale: strconv.FormatFloat(profile.TimeScale, 'g', -1, 64)}
	for i, channel := range profile.Channels {
		color := channel.Color
		if color == "" {
			color = config.DefaultCurveColor(i)
		}
		draft.channels = append(draft.channels, curveChannelDraft{
			name: channel.Name, mode: channel.Mode, pattern: channel.Pattern, unit: channel.Unit, color: color,
			scale:  strconv.FormatFloat(channel.Scale, 'g', -1, 64),
			offset: strconv.FormatFloat(channel.Offset, 'g', -1, 64), enabled: channel.Enabled,
		})
	}
	return draft
}

func (v *curveViewState) refreshCurveColors(recolorSnapshot bool) {
	count := len(v.draft.channels)
	if cap(v.channelColors) < count {
		v.channelColors = make([]ui.Color, count)
	} else {
		v.channelColors = v.channelColors[:count]
	}
	active := 0
	for i := range v.draft.channels {
		channel := &v.draft.channels[i]
		color := channel.color
		if color == "" {
			color = config.DefaultCurveColor(i)
		}
		rgba, valid := config.ParseCurveColor(color)
		if valid {
			v.channelColors[i] = curveColor(rgba)
			if recolorSnapshot && channel.enabled && active < len(v.snapshot.Series) {
				v.snapshot.Series[active].Color = rgba
			}
		} else {
			v.channelColors[i] = ui.RGB(128, 128, 128)
		}
		if channel.enabled {
			active++
		}
	}
}

func (v *curveViewState) profile() (config.CurveProfile, error) {
	profile := config.CurveProfile{Name: v.draft.name, TimePattern: v.draft.timePattern}
	var err error
	profile.TimeScale, err = strconv.ParseFloat(v.draft.timeScale, 64)
	if err != nil && profile.TimePattern != "" {
		return profile, i18n.NewError("时间倍率必须是数字", "Time scale must be a number")
	}
	for i, draft := range v.draft.channels {
		channel := config.CurveChannel{Name: draft.name, Mode: draft.mode, Pattern: draft.pattern,
			Unit: draft.unit, Enabled: draft.enabled, Color: draft.color}
		channel.Scale, err = strconv.ParseFloat(draft.scale, 64)
		if err != nil {
			return profile, i18n.NewError(fmt.Sprintf("第 %d 条曲线的倍率无效", i+1), fmt.Sprintf("Invalid scale for curve %d", i+1))
		}
		channel.Offset, err = strconv.ParseFloat(draft.offset, 64)
		if err != nil {
			return profile, i18n.NewError(fmt.Sprintf("第 %d 条曲线的偏移无效", i+1), fmt.Sprintf("Invalid offset for curve %d", i+1))
		}
		profile.Channels = append(profile.Channels, channel)
	}
	return profile, nil
}

func (v *curveViewState) loadProfile(settings *config.Settings, index int) {
	v.selected = index
	v.choice = curveProfileLabel(index, settings.CurveProfiles[index].Name)
	v.draft = curveDraft(settings.CurveProfiles[index])
	v.dirty = false
	err := internal.ValidateCurveProfile(settings.CurveProfiles[index])
	v.error = dialogError(err)
	v.refreshCurveColors(false)
}

func (s *controller) applyCurveDraft() bool {
	v := s.curve
	profile, err := v.profile()
	if err != nil {
		v.refreshCurveColors(false)
		v.error = dialogError(err)
		return false
	}
	profiles := slices.Clone(s.settings.CurveProfiles)
	index := v.selected
	refreshPreview := true
	if index < 0 {
		index = len(profiles)
		profiles = append(profiles, profile)
	} else {
		previous := profiles[index]
		refreshPreview = s.settings.CurveProfile != index || !config.SameCurveSchema(previous, profile)
		profiles[index] = profile
	}
	err = s.app.ApplyCurveProfiles(profiles, index)
	if err != nil {
		v.error = dialogError(err)
		v.refreshCurveColors(false)
		return false
	}
	v.selected = index
	v.choice = curveProfileLabel(index, profile.Name)
	v.dirty = false
	err = s.app.CurveError()
	v.error = dialogError(err)
	v.running = s.app.CurveRunning()
	if err != nil && !v.running {
		v.previewing, v.paused = false, false
	}
	if v.previewing || v.paused && !refreshPreview {
		if refreshPreview {
			s.previewCurve()
		} else {
			v.refreshCurveColors(true)
		}
	} else {
		v.refreshCurveColors(false)
		s.app.CurveSnapshot(&v.snapshot)
	}
	if v.testWindow != nil {
		v.testWindow.Invalidate()
	}
	return true
}
func (s *controller) curveChanged() {
	s.curve.dirty = true
	s.applyCurveDraft()
}

func (s *controller) startCurve() {
	if s.curve.dirty || s.curve.selected < 0 {
		applied := s.applyCurveDraft()
		if !applied {
			return
		}
	}
	v := s.curve
	err := s.app.StartCurve(v.selected)
	if err != nil {
		v.running = s.app.CurveRunning()
		v.error = dialogError(err)
		return
	}
	v.error = localizedText{}
	v.running, v.paused, v.previewing = true, false, false
	v.position = 100
	v.snapshot.Revision = 0
	s.app.CurveSnapshot(&v.snapshot)
}

func (s *controller) previewCurve() {
	v := s.curve
	profile, err := v.profile()
	if err != nil {
		v.error = dialogError(err)
		return
	}
	snapshot, err := internal.PreviewCurve(profile, v.previewText)
	if err != nil {
		v.error = dialogError(err)
		return
	}
	// Preview is isolated from the live parser and never saves settings.
	v.snapshot, v.previewing, v.paused = snapshot, true, true
	v.refreshCurveColors(false)
	v.position = 100
	v.error = localizedText{}
}

func (s *controller) curveConfirmation(c *ui.Context) {
	v := s.curve
	ui.Modal(c.Key("curve-confirm"), &v.confirmOpen, func() {
		ui.Column(c.Key("body")).Width(320).Gap(sectionGap).Children(func() {
			ui.Text(c.Key("question"), s.tr(v.confirmText.zh, v.confirmText.en)).FillWidth()
			ui.Row(c.Key("actions")).FillWidth().Gap(controlGap).Justify(ui.End).Children(func() {
				ui.Button(c.Key("cancel"), s.tr("取消", "Cancel")).AutoFocus().OnClick(func() { v.confirmOpen = false })
				ui.Button(c.Key("confirm"), s.tr("确定", "OK")).OnClick(func() {
					v.confirmOpen = false
					action := v.confirmAction
					v.confirmAction = nil
					if action != nil {
						action()
					}
				})
			})
		})
	}).Label(s.tr("确认", "Confirm"))
}
