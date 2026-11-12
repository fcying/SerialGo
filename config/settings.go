package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/fcying/SerialGo/assets"
	"github.com/fcying/SerialGo/communication/serialport"
	"github.com/fcying/SerialGo/internal/i18n"

	"github.com/BurntSushi/toml"
)

const (
	PresetCount        = 99
	defaultPresetCount = 5
)

var defaultSettings, defaultSettingsErr = parseDefaultSettings(assets.DefaultConfig)

type Preset struct {
	Label, Data, Interval string
	Hex, CRLF, Enabled    bool
}

// DisplayBufferPreset uses decimal K/M labels.
type DisplayBufferPreset struct {
	Label string
	Bytes int
}

func DisplayBufferPresets() [5]DisplayBufferPreset {
	return [5]DisplayBufferPreset{
		{Label: "200K", Bytes: 200_000},
		{Label: "500K", Bytes: 500_000},
		{Label: "1M", Bytes: 1_000_000},
		{Label: "2M", Bytes: 2_000_000},
		{Label: "5M", Bytes: 5_000_000},
	}
}

func validDisplayBufferLimit(bytes int) bool {
	for _, preset := range DisplayBufferPresets() {
		if bytes == preset.Bytes {
			return true
		}
	}
	return false
}

// NormalizeDisplayBufferLimit falls back to the embedded root template.
func NormalizeDisplayBufferLimit(bytes int) int {
	if validDisplayBufferLimit(bytes) {
		return bytes
	}
	return defaultSettings.DisplayBufferLimit
}

type Settings struct {
	Port                                serialport.Settings
	BaudRates                           []string
	SendText, SendHexText               string
	Language                            string
	Encoding                            string
	Interval, PacketGap, FileDelay      string
	SendHex, CRLF, ReceiveHex, ShowSent bool
	Timestamp, ShowPresets              bool
	DisplayBufferLimit                  int      // Retained event payload bytes, not rendered text bytes.
	Presets                             []Preset `toml:"-"`
}

type fileSettings struct {
	*Settings
	Presets           []Preset
	LegacyCustomBauds []string `toml:"CustomBauds,omitempty"`
	LegacyCustomBaud  string   `toml:"CustomBaud,omitempty"`
}

func parseDefaultSettings(source string) (Settings, error) {
	var settings Settings
	stored := fileSettings{Settings: &settings}
	if _, err := toml.Decode(source, &stored); err != nil {
		return Settings{}, fmt.Errorf("invalid embedded default configuration: %w", err)
	}
	if len(stored.Presets) != defaultPresetCount || stored.Presets[0].Interval == "" {
		return Settings{}, fmt.Errorf("invalid embedded default configuration: expected %d presets with an interval", defaultPresetCount)
	}
	if !validDisplayBufferLimit(settings.DisplayBufferLimit) {
		return Settings{}, errors.New("invalid embedded default configuration: unsupported display buffer limit")
	}
	settings.Presets = stored.Presets
	return settings, nil
}

func defaults() Settings {
	settings := defaultSettings
	settings.BaudRates = slices.Clone(settings.BaudRates)
	settings.Presets = slices.Clone(settings.Presets)
	return settings
}

func NewPreset() Preset {
	var preset Preset
	if len(defaultSettings.Presets) > 0 {
		preset.Interval = defaultSettings.Presets[0].Interval
	}
	return preset
}

func Load() (Settings, string, error) {
	if defaultSettingsErr != nil {
		return defaults(), "", defaultSettingsErr
	}
	exe, err := os.Executable()
	if err != nil {
		return defaults(), "", err
	}
	exe, err = filepath.EvalSymlinks(exe)
	if err != nil {
		return defaults(), "", err
	}
	return load(filepath.Join(filepath.Dir(exe), "config.toml"))
}

func load(path string) (Settings, string, error) {
	s := defaults()
	if defaultSettingsErr != nil {
		return s, "", defaultSettingsErr
	}
	err := decode(path, &s)
	if errors.Is(err, os.ErrNotExist) {
		err = Save(path, s)
		return s, path, err
	}
	if err != nil {
		// Do not overwrite a configuration we could not read.
		return defaults(), "", err
	}
	return s, path, nil
}

func decode(path string, s *Settings) error {
	stored := struct {
		*Settings
		Presets           []toml.Primitive
		LegacyCustomBauds []string `toml:"CustomBauds"`
		LegacyCustomBaud  string   `toml:"CustomBaud"`
	}{Settings: s}
	metadata, err := toml.DecodeFile(path, &stored)
	if err != nil {
		return err
	}
	if len(stored.Presets) > PresetCount {
		return i18n.NewError(
			fmt.Sprintf("预设数量过多: 共 %d 条, 最多支持 %d 条", len(stored.Presets), PresetCount),
			fmt.Sprintf("Too many presets: got %d, maximum %d", len(stored.Presets), PresetCount),
		)
	}
	presets := make([]Preset, len(stored.Presets))
	for i, raw := range stored.Presets {
		presets[i] = NewPreset()
		if i < len(defaultSettings.Presets) {
			presets[i] = defaultSettings.Presets[i]
		}
		err := metadata.PrimitiveDecode(raw, &presets[i])
		if err != nil {
			return err
		}
	}
	s.Presets = presets
	if !metadata.IsDefined("BaudRates") {
		for _, text := range stored.LegacyCustomBauds {
			appendLegacyBaud(s, text)
		}
		appendLegacyBaud(s, stored.LegacyCustomBaud)
		appendLegacyBaud(s, s.Port.Baud)
	}
	if s.Language != "zh-CN" && s.Language != "en" {
		s.Language = defaultSettings.Language
	}
	s.DisplayBufferLimit = NormalizeDisplayBufferLimit(s.DisplayBufferLimit)
	return nil
}

func appendLegacyBaud(s *Settings, text string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	for _, digit := range text {
		if digit < '0' || digit > '9' {
			return
		}
	}
	value, err := strconv.ParseUint(text, 10, 32)
	if err != nil || value == 0 {
		return
	}
	canonical := strconv.FormatUint(value, 10)
	if !slices.Contains(s.BaudRates, canonical) {
		s.BaudRates = append(s.BaudRates, canonical)
	}
}

func Save(path string, s Settings) error {
	if defaultSettingsErr != nil {
		return defaultSettingsErr
	}
	if path == "" {
		return nil
	}
	if len(s.Presets) > PresetCount {
		return i18n.NewError(
			fmt.Sprintf("预设数量过多: 共 %d 条, 最多支持 %d 条", len(s.Presets), PresetCount),
			fmt.Sprintf("Too many presets: got %d, maximum %d", len(s.Presets), PresetCount),
		)
	}
	s.DisplayBufferLimit = NormalizeDisplayBufferLimit(s.DisplayBufferLimit)
	// An explicit empty list records manual deletion and must survive reload.
	if s.BaudRates == nil {
		s.BaudRates = []string{}
	}
	dir := filepath.Dir(path)
	file, err := os.CreateTemp(dir, ".settings-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	presets := s.Presets
	if presets == nil {
		presets = []Preset{}
	}
	stored := fileSettings{Settings: &s, Presets: presets}
	writeErr := toml.NewEncoder(file).Encode(stored)
	closeErr := file.Close()
	if err := errors.Join(writeErr, closeErr); err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}
