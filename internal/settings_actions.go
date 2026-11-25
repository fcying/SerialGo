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

var (
	errPortSettingsBusy     = i18n.NewError("串口操作进行中", "A serial port operation is in progress")
	errPortSettingsObsolete = i18n.NewError("原串口连接已关闭", "The original serial port connection has closed")
	errConnectedBaudNeeded  = i18n.NewError("串口已打开, 必须保留一个选中的波特率", "The port is open; a baud rate must remain selected")
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

// CustomBaud edits an isolated draft, or selects the baud on the live port.
// On a live request it returns validation errors immediately and reports
// driver failures through the application's status after completion.
func (s *App) CustomBaud(text string, draft *serialport.Settings, rates *[]string) error {
	if err := s.portSettingsAvailable(); err != nil {
		return err
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
		if value == s.Settings.Port.Baud && slices.Equal(next, s.Settings.BaudRates) {
			return nil
		}
		if s.Connected() {
			s.applyLiveBaud(value, next)
		} else {
			s.Settings.BaudRates, s.Settings.Port.Baud = next, value
			s.SettingsChanged()
			s.SaveUsedSettings()
		}
	}
	return nil
}

func (s *App) SelectBaud(value string) {
	if s.portSettingsAvailable() != nil || !slices.Contains(s.Settings.BaudRates, value) || value == s.Settings.Port.Baud {
		return
	}
	if s.Connected() {
		s.applyLiveBaud(value, s.Settings.BaudRates)
		return
	}
	s.Settings.Port.Baud = value
	s.SettingsChanged()
}

func (s *App) applyLiveBaud(value string, rates []string) {
	p := s.session
	next := s.Settings.Port
	next.Baud = value
	s.ApplyPortSettingsAsync(next, rates, func(err error) {
		if err == nil {
			return
		}
		if s.session == p {
			s.Fail("设置波特率失败", "Failed to set baud rate", err)
		} else {
			s.ReportPortSettingsFailure(err)
		}
	})
}

func (s *App) DeleteBaud(value string, draft *serialport.Settings, rates *[]string) error {
	if err := s.portSettingsAvailable(); err != nil {
		return err
	}
	live := draft == nil
	if live {
		draft, rates = &s.Settings.Port, &s.Settings.BaudRates
	}
	index := slices.Index(*rates, value)
	if index < 0 {
		return nil
	}
	if s.Connected() {
		if live && draft.Baud == value {
			return i18n.NewError("串口已打开, 不能删除当前波特率", "The port is open; the current baud rate cannot be deleted")
		}
		if len(*rates) == 1 {
			return errConnectedBaudNeeded
		}
	}
	next := make([]string, len(*rates)-1)
	copy(next, (*rates)[:index])
	copy(next[index:], (*rates)[index+1:])
	if draft.Baud == value {
		draft.Baud = ""
		if len(next) > 0 {
			draft.Baud = next[0]
		}
	}
	*rates = next
	if live {
		s.SettingsChanged()
		s.SaveUsedSettings()
	}
	return nil
}

func (s *App) PortSettingsDraft() (serialport.Settings, []string) {
	return s.Settings.Port, slices.Clone(s.Settings.BaudRates)
}

func (s *App) portSettingsAvailable() error {
	if s.closing {
		return errPortSettingsObsolete
	}
	if s.PortBusy {
		return errPortSettingsBusy
	}
	return nil
}

// ApplyPortSettingsAsync commits only after the captured connection accepts
// the settings. Offline edits and immediate rejections complete inline; a
// live request completes on the frontend, unless shutdown discards the post.
func (s *App) ApplyPortSettingsAsync(next serialport.Settings, rates []string, done func(error)) {
	finish := func(err error) {
		if done != nil {
			done(err)
		}
	}
	if err := s.portSettingsAvailable(); err != nil {
		finish(err)
		return
	}
	previous := s.Settings.Port
	next.Name = previous.Name
	p := s.session
	if p == nil {
		if !slices.Equal(rates, s.Settings.BaudRates) {
			rates = slices.Clone(rates)
		}
		s.commitPortSettings(next, rates)
		finish(nil)
		return
	}
	if next.Baud == "" || !slices.Contains(rates, next.Baud) {
		finish(errConnectedBaudNeeded)
		return
	}
	rates = slices.Clone(rates)
	s.PortBusy = true
	s.jobs.Add(1)
	go func() {
		defer s.jobs.Done()
		err := p.applyPortSettings(previous, next)
		var failure *terminalError
		if err != nil && err != errPortSettingsObsolete {
			failure = s.retainTerminalError(err)
		}
		s.dispatch(func() {
			if s.session == p {
				// Prefer an already-queued read failure to a settings commit.
				s.drain(p)
			}
			if s.session != p {
				// The disconnect or replacement owns PortBusy now.
				if err == nil {
					err = errPortSettingsObsolete
				} else if err != errPortSettingsObsolete {
					err = errors.Join(errPortSettingsObsolete, err)
				}
			} else {
				s.PortBusy = false
				if p.ctx.Err() != nil && err == nil {
					err = errPortSettingsObsolete
				}
				if err == nil {
					s.commitPortSettings(next, rates)
				}
			}
			finish(err)
			if failure != nil && done != nil {
				s.acknowledgeTerminalError(failure)
			}
		})
	}()
}

// ReportPortSettingsFailure delivers a completion that its original UI can no longer show.
func (s *App) ReportPortSettingsFailure(err error) {
	if err == nil || (err == errPortSettingsObsolete && s.StatusError) {
		return
	}
	if errors.Is(err, errPortSettingsObsolete) && s.StatusError {
		s.appendOperationError("设置串口参数失败", "Failed to set serial port parameters", err)
		return
	}
	s.Fail("设置串口参数失败", "Failed to set serial port parameters", err)
}

func (s *App) commitPortSettings(next serialport.Settings, rates []string) {
	if next == s.Settings.Port && slices.Equal(rates, s.Settings.BaudRates) {
		return
	}
	s.Settings.Port = next
	if !slices.Equal(rates, s.Settings.BaudRates) {
		s.Settings.BaudRates = rates
	}
	s.SettingsChanged()
	s.Note("串口设置已更新", "Port settings updated")
	s.SaveUsedSettings()
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

// SetCurveSidebarWidth records a preference without scheduling a settings write.
func (s *App) SetCurveSidebarWidth(width float32) bool {
	width = config.NormalizeCurveSidebarWidth(width)
	if width == config.NormalizeCurveSidebarWidth(s.Settings.CurveSidebarWidth) {
		return false
	}
	s.Settings.CurveSidebarWidth = width
	s.SettingsChanged()
	return true
}

func (s *App) SetCurveWindowGeometry(geometry config.WindowSettings) {
	if s.Settings.CurveWindow == geometry {
		return
	}
	s.Settings.CurveWindow = geometry
	s.SettingsChanged()
	s.SaveUsedSettings()
}
