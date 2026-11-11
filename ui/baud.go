package ui

import (
	"slices"
	"strconv"
	"strings"

	"github.com/egoist/mygo/ui"
)

type baudPickerState struct {
	value, choice, language string
	rates                   []string
	options                 []string
	open                    bool
}

func normalizedBaud(text string) (string, bool) {
	value, err := strconv.ParseUint(strings.TrimSpace(text), 10, 32)
	if err != nil || value == 0 {
		return "", false
	}
	return strconv.FormatUint(value, 10), true
}

func baudRateChoices(values []string) []string {
	rates := make([]uint64, 0, len(values))
	for _, text := range values {
		value, err := strconv.ParseUint(strings.TrimSpace(text), 10, 32)
		if err == nil && value > 0 {
			rates = append(rates, value)
		}
	}
	slices.Sort(rates)
	rates = slices.Compact(rates)
	choices := make([]string, len(rates))
	for i, rate := range rates {
		choices[i] = strconv.FormatUint(rate, 10)
	}
	return choices
}

// The list and value are canonical positive decimal strings, sorted numerically.
func insertBaudRate(rates []string, value string) []string {
	number, _ := strconv.ParseUint(value, 10, 32)
	index := len(rates)
	for i, text := range rates {
		if text == value {
			return rates
		}
		other, _ := strconv.ParseUint(text, 10, 32)
		if number < other {
			index = i
			break
		}
	}
	result := make([]string, len(rates)+1)
	copy(result, rates[:index])
	result[index] = value
	copy(result[index+1:], rates[index:])
	return result
}

func (s *controller) initializeBaudRates() {
	s.settings.BaudRates = baudRateChoices(s.settings.BaudRates)
	current, valid := normalizedBaud(s.settings.Port.Baud)
	if !valid || !slices.Contains(s.settings.BaudRates, current) {
		current = ""
		if len(s.settings.BaudRates) > 0 {
			current = s.settings.BaudRates[0]
		}
	}
	s.settings.Port.Baud = current
}

func (s *controller) baudPicker(c *ui.Context, forPort bool, height float32) ui.Element {
	index := 0
	value := &s.settings.Port.Baud
	rates := s.settings.BaudRates
	if forPort {
		index = 1
		value = &s.portDraft.Baud
		rates = s.portBaudRatesDraft
	}
	state := &s.baudPickers[index]
	if state.value != *value {
		state.value, state.choice = *value, *value
	}
	customLabel := s.tr("自定义...", "Custom...")
	if state.options == nil || !slices.Equal(state.rates, rates) || state.language != s.settings.Language {
		state.options = make([]string, len(rates)+1)
		copy(state.options, rates)
		state.options[len(rates)] = customLabel
		state.rates = slices.Clone(rates)
		state.language = s.settings.Language
	}
	theme := c.Theme()
	sel := ui.SelectBaseOpen(c, &state.choice, &state.open)
	if s.session != nil {
		sel.SetOpen(false)
	}
	trigger := sel.Trigger.Width(0).MinWidth(0).Grow(1).Height(height).
		Padding(controlPadY, 3).Gap(2).Radius(theme.Radius).Border(1, theme.Border).
		Background(theme.Surface).TextColor(theme.Text).AlignItems(ui.Center).
		Label(s.tr("波特率", "Baud rate")).Disabled(s.session != nil).OnChange(func() {
		if state.choice == customLabel {
			state.choice = *value
			s.openCustomBaud(forPort)
			return
		}
		if state.choice == *value {
			return
		}
		*value, state.value = state.choice, state.choice
		if !forPort {
			s.settingsChanged()
		}
	})
	if trigger.Pressed() {
		trigger.Background(theme.SurfacePressed)
	} else if trigger.Hovered() {
		trigger.Background(theme.SurfaceHover)
	}
	trigger.Children(func() {
		ui.Text(c.Key("selected"), state.choice).Width(0).MinWidth(0).Grow(1).SingleLine()
		ui.Box(c.Key("arrow")).Size(8, 8).Shrink(0).Draw(func(p *ui.Painter, r ui.Rect) {
			var path ui.Path
			path.MoveTo(r.X+r.W*0.1, r.Y+r.H*0.3).LineTo(r.X+r.W*0.5, r.Y+r.H*0.7).LineTo(r.X+r.W*0.9, r.Y+r.H*0.3)
			p.StrokePath(&path, 1.5, theme.TextMuted)
		})
	})
	sel.Popup(func(panel ui.Element) {
		panel.Padding(3).Radius(theme.Radius).Background(theme.Background).Border(1, theme.Border).Children(func() {
			ui.Scroll(c.Key("baud-options")).FillWidth().MaxHeight(240).Children(func() {
				ui.Column(c.Key("choices")).FillWidth().Gap(2).Children(func() {
					for _, option := range state.options {
						item := sel.Item(option).Padding(4, 6).Radius(theme.Radius)
						if item.Highlighted() {
							item.Background(theme.Accent).TextColor(theme.AccentText)
						} else if option == *value {
							item.Background(theme.Surface)
						}
						if option != customLabel {
							item.Tooltip(s.tr("右键删除此波特率", "Right-click to delete this baud rate")).ContextMenu(func(m *ui.Menu) {
								chosen := m.Item(s.tr("删除", "Delete")).Disabled(s.session != nil).Chosen()
								if chosen {
									sel.SetOpen(false)
									s.openBaudDelete(option, forPort)
								}
							})
						}
						item.Children(func() { ui.Text(c.Key("label"), option).SingleLine() })
					}
				})
			})
		})
	})
	return trigger
}

func (s *controller) openCustomBaud(forPort bool) {
	if s.session != nil {
		return
	}
	s.customBaudForPort = forPort
	s.customBaudText = s.settings.Port.Baud
	if forPort {
		s.customBaudText = s.portDraft.Baud
	}
	s.customBaudError = localizedText{}
	s.customBaudOpen = true
}

func (s *controller) applyCustomBaud() {
	if s.session != nil {
		s.customBaudError = localizedText{zh: "串口已打开, 不能修改波特率", en: "The port is open; the baud rate cannot be changed"}
		return
	}
	value, valid := normalizedBaud(s.customBaudText)
	if !valid {
		s.customBaudError = localizedText{zh: "请输入 1 到 4294967295 之间的整数", en: "Enter an integer between 1 and 4294967295"}
		return
	}
	if s.customBaudForPort {
		s.portBaudRatesDraft = insertBaudRate(s.portBaudRatesDraft, value)
		s.portDraft.Baud = value
	} else {
		rates := insertBaudRate(s.settings.BaudRates, value)
		if value != s.settings.Port.Baud || !slices.Equal(rates, s.settings.BaudRates) {
			s.settings.BaudRates = rates
			s.settings.Port.Baud = value
			s.settingsChanged()
			s.saveUsedSettings()
		}
	}
	s.customBaudOpen = false
	s.customBaudError = localizedText{}
}

func (s *controller) customBaudDialog(c *ui.Context) {
	ui.Modal(c.Key("custom-baud-dialog"), &s.customBaudOpen, func() {
		ui.Column(c.Key("custom-baud")).Width(310).Gap(10).Children(func() {
			ui.Text(c.Key("title"), s.tr("自定义波特率", "Custom baud rate")).FontSize(16).FontWeight(600)
			ui.TextInput(c.Key("value"), &s.customBaudText).FillWidth().Height(28).AutoFocus().Label(s.tr("波特率数值", "Baud rate value"))
			if s.customBaudError.en != "" || s.customBaudError.zh != "" {
				ui.Text(c.Key("error"), s.tr(s.customBaudError.zh, s.customBaudError.en)).FillWidth().TextColor(c.Theme().Danger)
			}
			ui.Row(c.Key("actions")).FillWidth().Gap(8).Justify(ui.End).Children(func() {
				ui.Button(c.Key("ok"), s.tr("确定", "OK")).Width(76).Height(28).OnClick(s.applyCustomBaud)
				ui.Button(c.Key("cancel"), s.tr("取消", "Cancel")).Width(76).Height(28).OnClick(func() { s.customBaudOpen = false })
			})
		})
	}).Label(s.tr("自定义波特率", "Custom baud rate"))
}

func (s *controller) openBaudDelete(value string, forPort bool) {
	rates := s.settings.BaudRates
	index := 0
	if forPort {
		rates = s.portBaudRatesDraft
		index = 1
	}
	if s.session != nil || !slices.Contains(rates, value) {
		return
	}
	s.baudPickers[index].open = false
	s.baudDeleteValue = value
	s.baudDeleteForPort = forPort
	s.baudDeleteError = localizedText{}
	s.baudDeleteOpen = true
}

func (s *controller) applyBaudDelete() {
	if s.session != nil {
		s.baudDeleteError = localizedText{zh: "串口已打开, 不能删除波特率", en: "The port is open; baud rates cannot be deleted"}
		return
	}
	rates := &s.settings.BaudRates
	current := &s.settings.Port.Baud
	if s.baudDeleteForPort {
		rates = &s.portBaudRatesDraft
		current = &s.portDraft.Baud
	}
	index := slices.Index(*rates, s.baudDeleteValue)
	if index < 0 {
		s.baudDeleteOpen = false
		return
	}
	next := make([]string, len(*rates)-1)
	copy(next, (*rates)[:index])
	copy(next[index:], (*rates)[index+1:])
	*rates = next
	if *current == s.baudDeleteValue {
		*current = ""
		if len(next) > 0 {
			*current = next[0]
		}
	}
	if !s.baudDeleteForPort {
		s.settingsChanged()
		s.saveUsedSettings()
	}
	s.baudDeleteOpen = false
	s.baudDeleteError = localizedText{}
}

func (s *controller) baudDeleteDialog(c *ui.Context) {
	ui.Modal(c.Key("baud-delete-dialog"), &s.baudDeleteOpen, func() {
		ui.Column(c.Key("baud-delete")).Width(310).Gap(10).Children(func() {
			ui.Text(c.Key("title"), s.tr("删除波特率", "Delete baud rate")).FontSize(16).FontWeight(600)
			ui.Text(c.Key("question"), s.tr("确认删除波特率 ", "Delete baud rate ")+s.baudDeleteValue+"?").FillWidth()
			if s.baudDeleteError.en != "" || s.baudDeleteError.zh != "" {
				ui.Text(c.Key("error"), s.tr(s.baudDeleteError.zh, s.baudDeleteError.en)).FillWidth().TextColor(c.Theme().Danger)
			}
			ui.Row(c.Key("actions")).FillWidth().Gap(8).Justify(ui.End).Children(func() {
				ui.Button(c.Key("delete"), s.tr("删除", "Delete")).Width(76).Height(28).OnClick(s.applyBaudDelete)
				ui.Button(c.Key("cancel"), s.tr("取消", "Cancel")).Width(76).Height(28).AutoFocus().OnClick(func() { s.baudDeleteOpen = false })
			})
		})
	}).Label(s.tr("删除波特率", "Delete baud rate"))
}
