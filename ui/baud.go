package ui

import (
	"slices"

	"github.com/egoist/mygo/ui"
)

type baudPickerState struct {
	value, choice, language string
	rates                   []string
	options                 []string
	open                    bool
	triggerWidth            float32
	triggerWidthChoice      string
}

func (s *controller) mainBaudPickerWidth(c *ui.Context) float32 {
	state := &s.baudPickers[0]
	value := s.settings.Port.Baud
	if state.value != value {
		state.value, state.choice = value, value
	}
	return s.baudPickerWidth(c, state)
}

func (s *controller) portBaudPickerWidth(c *ui.Context) float32 {
	state := &s.baudPickers[1]
	value := s.portDraft.Baud
	if state.value != value {
		state.value, state.choice = value, value
	}
	return s.baudPickerWidth(c, state)
}

func (s *controller) baudPickerWidth(c *ui.Context, state *baudPickerState) float32 {
	if state.triggerWidth == 0 || state.triggerWidthChoice != state.choice {
		standardWidth, _ := c.MeasureText(0, ui.Span{Text: "1000000"})
		selectedWidth, _ := c.MeasureText(0, ui.Span{Text: state.choice})
		state.triggerWidth = max(standardWidth, selectedWidth) + baudPickerChromeWidth
		state.triggerWidthChoice = state.choice
	}
	return state.triggerWidth
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
	triggerWidth := s.baudPickerWidth(c, state)
	theme := c.Theme()
	sel := ui.SelectBaseOpen(c, &state.choice, &state.open)
	if s.app.Connected() || s.app.PortBusy {
		sel.SetOpen(false)
	}
	trigger := sel.Trigger.Width(triggerWidth).MinWidth(triggerWidth).Grow(0).Shrink(0).Height(height).
		Padding(controlPadY, serialSelectorArrowPaddingX, controlPadY, 0).Gap(microGap).Radius(theme.Radius).Border(baudPickerBorderWidth, theme.Border).
		Background(theme.Surface).TextColor(theme.Text).AlignItems(ui.Center).
		Label(s.tr("波特率", "Baud rate")).Disabled(s.app.Connected() || s.app.PortBusy).OnChange(func() {
		if state.choice == customLabel {
			state.choice = *value
			s.openCustomBaud(forPort)
			return
		}
		if state.choice == *value {
			return
		}
		if forPort {
			*value = state.choice
		} else {
			s.app.SelectBaud(state.choice)
		}
		state.value, state.choice = *value, *value
	})
	if trigger.Pressed() {
		trigger.Background(theme.SurfacePressed)
	} else if trigger.Hovered() {
		trigger.Background(theme.SurfaceHover)
	}
	trigger.Children(func() {
		ui.Text(c.Key("selected"), state.choice).Width(0).MinWidth(0).Grow(1).Padding(0, serialSelectorPaddingX).SingleLine().TextAlign(ui.Center)
		ui.Box(c.Key("arrow")).Size(baudPickerArrowSize, baudPickerArrowSize).Shrink(0).Draw(func(p *ui.Painter, r ui.Rect) {
			var path ui.Path
			path.MoveTo(r.X+r.W*0.1, r.Y+r.H*0.3).LineTo(r.X+r.W*0.5, r.Y+r.H*0.7).LineTo(r.X+r.W*0.9, r.Y+r.H*0.3)
			p.StrokePath(&path, 1.5, theme.TextMuted)
		})
	})
	sel.Popup(func(panel ui.Element) {
		panel.Padding(3).Radius(theme.Radius).Background(theme.Background).Border(1, theme.Border).Children(func() {
			ui.Scroll(c.Key("baud-options")).FillWidth().MaxHeight(240).Children(func() {
				ui.Column(c.Key("choices")).FillWidth().Gap(compactGap).Children(func() {
					for _, option := range state.options {
						item := sel.Item(option).Padding(4, 6).Radius(theme.Radius)
						if item.Highlighted() {
							item.Background(theme.Accent).TextColor(theme.AccentText)
						} else if option == *value {
							item.Background(theme.Surface)
						}
						if option != customLabel {
							item.Tooltip(s.tr("右键删除此波特率", "Right-click to delete this baud rate")).ContextMenu(func(m *ui.Menu) {
								chosen := m.Item(s.tr("删除", "Delete")).Disabled(s.app.Connected() || s.app.PortBusy).Chosen()
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
	if s.app.Connected() || s.app.PortBusy {
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
	var err error
	if s.customBaudForPort {
		err = s.app.CustomBaud(s.customBaudText, &s.portDraft, &s.portBaudRatesDraft)
	} else {
		err = s.app.CustomBaud(s.customBaudText, nil, nil)
	}
	if err != nil {
		s.customBaudError = dialogError(err)
		return
	}
	s.customBaudOpen = false
	s.customBaudError = localizedText{}
}

func (s *controller) customBaudDialog(c *ui.Context) {
	ui.Modal(c.Key("custom-baud-dialog"), &s.customBaudOpen, func() {
		ui.Column(c.Key("custom-baud")).Width(310).Gap(sectionGap).Children(func() {
			ui.Text(c.Key("title"), s.tr("自定义波特率", "Custom baud rate")).FontSize(16).FontWeight(600)
			ui.TextInput(c.Key("value"), &s.customBaudText).FillWidth().Height(28).AutoFocus().Label(s.tr("波特率数值", "Baud rate value"))
			if s.customBaudError.en != "" || s.customBaudError.zh != "" {
				ui.Text(c.Key("error"), s.tr(s.customBaudError.zh, s.customBaudError.en)).FillWidth().TextColor(c.Theme().Danger)
			}
			ui.Row(c.Key("actions")).FillWidth().Gap(wideGap).Justify(ui.End).Children(func() {
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
	if s.app.Connected() || s.app.PortBusy || !slices.Contains(rates, value) {
		return
	}
	s.baudPickers[index].open = false
	s.baudDeleteValue = value
	s.baudDeleteForPort = forPort
	s.baudDeleteError = localizedText{}
	s.baudDeleteOpen = true
}

func (s *controller) applyBaudDelete() {
	var err error
	if s.baudDeleteForPort {
		err = s.app.DeleteBaud(s.baudDeleteValue, &s.portDraft, &s.portBaudRatesDraft)
	} else {
		err = s.app.DeleteBaud(s.baudDeleteValue, nil, nil)
	}
	if err != nil {
		s.baudDeleteError = dialogError(err)
		return
	}
	s.baudDeleteOpen = false
	s.baudDeleteError = localizedText{}
}

func (s *controller) baudDeleteDialog(c *ui.Context) {
	ui.Modal(c.Key("baud-delete-dialog"), &s.baudDeleteOpen, func() {
		ui.Column(c.Key("baud-delete")).Width(310).Gap(sectionGap).Children(func() {
			ui.Text(c.Key("title"), s.tr("删除波特率", "Delete baud rate")).FontSize(16).FontWeight(600)
			ui.Text(c.Key("question"), s.tr("确认删除波特率 ", "Delete baud rate ")+s.baudDeleteValue+"?").FillWidth()
			if s.baudDeleteError.en != "" || s.baudDeleteError.zh != "" {
				ui.Text(c.Key("error"), s.tr(s.baudDeleteError.zh, s.baudDeleteError.en)).FillWidth().TextColor(c.Theme().Danger)
			}
			ui.Row(c.Key("actions")).FillWidth().Gap(wideGap).Justify(ui.End).Children(func() {
				ui.Button(c.Key("delete"), s.tr("删除", "Delete")).Width(76).Height(28).OnClick(s.applyBaudDelete)
				ui.Button(c.Key("cancel"), s.tr("取消", "Cancel")).Width(76).Height(28).AutoFocus().OnClick(func() { s.baudDeleteOpen = false })
			})
		})
	}).Label(s.tr("删除波特率", "Delete baud rate"))
}
