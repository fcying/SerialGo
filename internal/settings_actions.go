package internal

import (
	"errors"
	"slices"
	"strconv"
	"strings"

	"github.com/fcying/SerialGo/communication/serialport"
	"github.com/fcying/SerialGo/config"
	"github.com/fcying/SerialGo/internal/i18n"
)

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

func (s *App) InitializeBaudRates() {
	s.Settings.BaudRates = baudRateChoices(s.Settings.BaudRates)
	current, valid := normalizedBaud(s.Settings.Port.Baud)
	if !valid || !slices.Contains(s.Settings.BaudRates, current) {
		current = ""
		if len(s.Settings.BaudRates) > 0 {
			current = s.Settings.BaudRates[0]
		}
	}
	s.Settings.Port.Baud = current
}

// CustomBaud validates and edits either an isolated property draft or live settings.
func (s *App) CustomBaud(text string, draft *serialport.Settings, rates *[]string) error {
	if s.PortBusy {
		return i18n.NewError("串口操作进行中", "A serial port operation is in progress")
	}
	if s.Connected() {
		return i18n.NewError("串口已打开, 不能修改波特率", "The port is open; the baud rate cannot be changed")
	}
	value, valid := normalizedBaud(text)
	if !valid {
		return i18n.NewError("请输入 1 到 4294967295 之间的整数", "Enter an integer between 1 and 4294967295")
	}
	if draft != nil {
		*rates = insertBaudRate(*rates, value)
		draft.Baud = value
	} else {
		next := insertBaudRate(s.Settings.BaudRates, value)
		if value != s.Settings.Port.Baud || !slices.Equal(next, s.Settings.BaudRates) {
			s.Settings.BaudRates, s.Settings.Port.Baud = next, value
			s.SettingsChanged()
			s.SaveUsedSettings()
		}
	}
	return nil
}

func (s *App) SelectBaud(value string) {
	if s.Connected() || s.PortBusy || !slices.Contains(s.Settings.BaudRates, value) || value == s.Settings.Port.Baud {
		return
	}
	s.Settings.Port.Baud = value
	s.SettingsChanged()
}

func (s *App) DeleteBaud(value string, draft *serialport.Settings, rates *[]string) error {
	if s.PortBusy {
		return i18n.NewError("串口操作进行中", "A serial port operation is in progress")
	}
	if s.Connected() {
		return i18n.NewError("串口已打开, 不能删除波特率", "The port is open; baud rates cannot be deleted")
	}
	live := draft == nil
	if live {
		draft, rates = &s.Settings.Port, &s.Settings.BaudRates
	}
	index := slices.Index(*rates, value)
	if index < 0 {
		return nil
	}
	next := make([]string, len(*rates)-1)
	copy(next, (*rates)[:index])
	copy(next[index:], (*rates)[index+1:])
	*rates = next
	if draft.Baud == value {
		draft.Baud = ""
		if len(next) > 0 {
			draft.Baud = next[0]
		}
	}
	if live {
		s.SettingsChanged()
		s.SaveUsedSettings()
	}
	return nil
}

func (s *App) PortSettingsDraft() (serialport.Settings, []string) {
	return s.Settings.Port, slices.Clone(s.Settings.BaudRates)
}

func (s *App) ApplyPortSettings(next serialport.Settings, rates []string) error {
	if s.PortBusy {
		return i18n.NewError("串口操作进行中", "A serial port operation is in progress")
	}
	previous := s.Settings.Port
	next.Name = previous.Name
	if s.Connected() {
		next.Baud, next.DataBits, next.Parity, next.StopBits = previous.Baud, previous.DataBits, previous.Parity, previous.StopBits
		rates = s.Settings.BaudRates
		if next.DTR != previous.DTR || next.RTS != previous.RTS {
			if err := applyControlLines(s.session.SetLines, previous, next); err != nil {
				return err
			}
		}
	}
	if next != previous || !slices.Equal(rates, s.Settings.BaudRates) {
		s.Settings.Port, s.Settings.BaudRates = next, slices.Clone(rates)
		s.SettingsChanged()
		s.Note("串口设置已更新", "Port settings updated")
		s.SaveUsedSettings()
	}
	return nil
}

func applyControlLines(setLines func(bool, bool) error, previous, next serialport.Settings) error {
	if err := setLines(next.DTR, next.RTS); err != nil {
		restoreErr := setLines(previous.DTR, previous.RTS)
		if restoreErr != nil {
			return errors.Join(
				i18n.WrapError("设置控制线失败", "Failed to set control lines", err),
				i18n.WrapError("恢复原控制线配置失败, 硬件状态可能与配置不一致", "Failed to restore previous control-line settings; hardware state may not match configuration", restoreErr),
			)
		}
		return i18n.WrapError("设置控制线失败, 已重新应用原配置状态", "Failed to set control lines; previous configured state was reapplied", err)
	}
	return nil
}

func (s *App) AddPreset() bool {
	if s.Sending() || len(s.Settings.Presets) >= config.PresetCount {
		return false
	}
	s.Settings.Presets = append(s.Settings.Presets, config.NewPreset())
	s.SettingsChanged()
	s.SaveUsedSettings()
	return true
}

func (s *App) DeletePreset(index int) bool {
	if s.Sending() || index < 0 || index >= len(s.Settings.Presets) {
		return false
	}
	s.Settings.Presets = slices.Delete(s.Settings.Presets, index, index+1)
	s.SettingsChanged()
	s.SaveUsedSettings()
	return true
}

func (s *App) SetLanguage(language string) bool {
	if language != "zh-CN" {
		language = "en"
	}
	if language == s.Settings.Language {
		return false
	}
	s.Settings.Language = language
	s.SettingsChanged()
	s.SaveUsedSettings()
	return true
}
