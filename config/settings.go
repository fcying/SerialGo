package config

import (
	"errors"
	"fmt"
	"io"
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
	PresetCount         = 99
	defaultPresetCount  = 5
	defaultWindowWidth  = 800
	defaultWindowHeight = 600
)

var defaultSettings, defaultSettingsErr = parseDefaultSettings(assets.DefaultConfig)

type Preset struct {
	Label, Data, Interval string
	Hex, CRLF, Enabled    bool
}

type WindowSettings struct {
	X, Y                    int
	Width, Height           int
	PixelX, PixelY          int
	PixelWidth, PixelHeight int
	Maximized               bool
	PositionSet             bool
}

// DisplayBufferPreset uses decimal K/M labels.
type DisplayBufferPreset struct {
	Label string
	Bytes int
}

func DisplayBufferPresets() [6]DisplayBufferPreset {
	return [...]DisplayBufferPreset{
		{Label: "200K", Bytes: 200_000},
		{Label: "500K", Bytes: 500_000},
		{Label: "1M", Bytes: 1_000_000},
		{Label: "2M", Bytes: 2_000_000},
		{Label: "5M", Bytes: 5_000_000},
		{Label: "10M", Bytes: 10_000_000},
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

const DefaultReceiveHexBytesPerLine = 32

func ReceiveHexLineWidths() [3]int {
	return [...]int{16, DefaultReceiveHexBytesPerLine, 64}
}

func NormalizeReceiveHexBytesPerLine(width int) int {
	for _, option := range ReceiveHexLineWidths() {
		if width == option {
			return width
		}
	}
	return DefaultReceiveHexBytesPerLine
}

const (
	DefaultReceiveFont      = "宋体"
	DefaultReceiveFontSize  = 9
	DefaultReceiveFontColor = "#000000"
	MinReceiveFontSize      = 5
	MaxReceiveFontSize      = 72
)

func NormalizeReceiveFont(family string) string {
	family = strings.TrimSpace(family)
	if family == "" {
		return DefaultReceiveFont
	}
	return family
}

func NormalizeReceiveFontSize(size int) int {
	if size < MinReceiveFontSize || size > MaxReceiveFontSize {
		return DefaultReceiveFontSize
	}
	return size
}

func NormalizeReceiveFontColor(value string) string {
	value = strings.TrimSpace(value)
	if len(value) != 7 || value[0] != '#' {
		return DefaultReceiveFontColor
	}
	if _, err := strconv.ParseUint(value[1:], 16, 24); err != nil {
		return DefaultReceiveFontColor
	}
	return strings.ToUpper(value)
}

func validReceiveFontSettings(settings Settings) bool {
	return strings.TrimSpace(settings.ReceiveFont) != "" &&
		settings.ReceiveFontSize >= MinReceiveFontSize && settings.ReceiveFontSize <= MaxReceiveFontSize &&
		NormalizeReceiveFontColor(settings.ReceiveFontColor) == strings.ToUpper(settings.ReceiveFontColor)
}

type Settings struct {
	Port                                serialport.Settings
	Window                              WindowSettings
	CurveWindow                         WindowSettings
	BaudRates                           []string
	SendText, SendHexText               string
	Language                            string
	Encoding                            string
	Interval, PacketGap, FileDelay      string
	SendHex, CRLF, ReceiveHex, ShowSent bool
	ParseSendEscapes                    bool
	ChecksumAlgorithm, ChecksumHeaderBytes, ChecksumTailBytes string
	Timestamp, ShowPresets              bool
	DisplayBufferLimit                  int      // Retained event payload bytes, not rendered text bytes.
	ReceiveHexBytesPerLine              int
	ReceiveFont                         string   // Font family for the receive display.
	ReceiveFontSize                     int      // Font size in points.
	ReceiveFontColor                    string   // Foreground color as #RRGGBB.
	Presets                             []Preset `toml:"-"`
	CurveProfiles                       []CurveProfile
	CurveProfile                        int
	CurveSidebarWidth                   float32
}

type fileSettings struct {
	*Settings
	Presets           []Preset
	LegacyCustomBauds []string `toml:"CustomBauds,omitempty"`
	LegacyCustomBaud  string   `toml:"CustomBaud,omitempty"`
}

func parseDefaultSettings(source string) (Settings, error) {
	var settings Settings
	stored := struct {
		fileSettings
		CurveProfiles []toml.Primitive
	}{fileSettings: fileSettings{Settings: &settings}}
	metadata, err := toml.Decode(source, &stored)
	if err != nil {
		return Settings{}, fmt.Errorf("invalid embedded default configuration: %w", err)
	}
	settings.CurveProfiles, err = decodeCurveProfiles(metadata, stored.CurveProfiles)
	if err != nil {
		return Settings{}, fmt.Errorf("invalid embedded default configuration: %w", err)
	}
	if len(stored.Presets) != defaultPresetCount || stored.Presets[0].Interval == "" {
		return Settings{}, fmt.Errorf("invalid embedded default configuration: expected %d presets with an interval", defaultPresetCount)
	}
	if !validDisplayBufferLimit(settings.DisplayBufferLimit) {
		return Settings{}, errors.New("invalid embedded default configuration: unsupported display buffer limit")
	}
	if !validReceiveFontSettings(settings) {
		return Settings{}, errors.New("invalid embedded default configuration: unsupported receive font settings")
	}
	settings.ReceiveHexBytesPerLine = NormalizeReceiveHexBytesPerLine(settings.ReceiveHexBytesPerLine)
	settings.CurveSidebarWidth = NormalizeCurveSidebarWidth(settings.CurveSidebarWidth)
	settings.Presets = stored.Presets
	return settings, nil
}

// Seed each profile before decoding so an absent limit differs from an explicit zero.
func decodeCurveProfiles(metadata toml.MetaData, raw []toml.Primitive) ([]CurveProfile, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	profiles := make([]CurveProfile, len(raw))
	for i, entry := range raw {
		profiles[i].PointLimit = DefaultCurvePointLimit
		err := metadata.PrimitiveDecode(entry, &profiles[i])
		if err != nil {
			return nil, err
		}
	}
	return profiles, nil
}

func defaults() Settings {
	settings := defaultSettings
	settings.BaudRates = slices.Clone(settings.BaudRates)
	settings.Presets = slices.Clone(settings.Presets)
	settings.CurveProfiles = CloneCurveProfiles(settings.CurveProfiles)
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
	return loadDefault(nil)
}

// LoadWithIdentity returns the actual opened file, even if decoding fails.
// The caller owns and must close any non-nil file returned.
func LoadWithIdentity() (Settings, string, *os.File, error) {
	var file *os.File
	s, path, err := loadDefault(&file)
	return s, path, file, err
}

func loadDefault(opened **os.File) (Settings, string, error) {
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
	return loadFile(filepath.Join(filepath.Dir(exe), "config.toml"), opened)
}

func load(path string) (Settings, string, error) {
	return loadFile(path, nil)
}

func loadFile(path string, opened **os.File) (Settings, string, error) {
	return loadFileInDir(path, filepath.Dir(path), opened)
}

func loadFileInDir(path, logicalDir string, opened **os.File) (Settings, string, error) {
	s := defaults()
	if defaultSettingsErr != nil {
		return s, "", defaultSettingsErr
	}
	target, err := settingsTargetInDir(path, logicalDir)
	if err != nil {
		return s, "", err
	}
	err = decodeFile(target, &s, opened)
	if errors.Is(err, os.ErrNotExist) && (opened == nil || *opened == nil) {
		if err = saveFileInDir(path, logicalDir, s, true, opened); err != nil {
			return defaults(), "", err
		}
		return s, path, nil
	}
	if err != nil {
		// Do not overwrite a configuration we could not read.
		return defaults(), "", err
	}
	return s, path, nil
}

func decode(path string, s *Settings) error {
	return decodeFile(path, s, nil)
}

func decodeFile(path string, s *Settings, opened **os.File) error {
	file, err := openSettingsFile(path)
	if err != nil {
		return err
	}
	if opened == nil {
		defer file.Close()
	} else {
		*opened = file
	}
	return decodeSettings(file, s)
}

func decodeSettings(file io.Reader, s *Settings) error {
	stored := struct {
		*Settings
		Presets           []toml.Primitive
		CurveProfiles     []toml.Primitive
		LegacyCustomBauds []string `toml:"CustomBauds"`
		LegacyCustomBaud  string   `toml:"CustomBaud"`
		// Decode wider than the UI type so invalid numeric widths can be normalized.
		CurveSidebarWidth float64
	}{Settings: s, CurveSidebarWidth: float64(s.CurveSidebarWidth)}
	metadata, err := toml.NewDecoder(file).Decode(&stored)
	if err != nil {
		return err
	}
	if metadata.IsDefined("CurveProfiles") {
		s.CurveProfiles, err = decodeCurveProfiles(metadata, stored.CurveProfiles)
		if err != nil {
			return err
		}
	}
	if !metadata.IsDefined("Window", "PositionSet") {
		s.Window.PositionSet = s.Window.X != 0 || s.Window.Y != 0 ||
			s.Window.PixelX != 0 || s.Window.PixelY != 0 ||
			s.Window.PixelWidth != 0 || s.Window.PixelHeight != 0 ||
			s.Window.Width != defaultWindowWidth || s.Window.Height != defaultWindowHeight
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
	s.ReceiveHexBytesPerLine = NormalizeReceiveHexBytesPerLine(s.ReceiveHexBytesPerLine)
	s.ReceiveFont = NormalizeReceiveFont(s.ReceiveFont)
	s.ReceiveFontSize = NormalizeReceiveFontSize(s.ReceiveFontSize)
	s.ReceiveFontColor = NormalizeReceiveFontColor(s.ReceiveFontColor)
	s.CurveSidebarWidth = NormalizeCurveSidebarWidth(float32(stored.CurveSidebarWidth))
	for i := range s.CurveProfiles {
		for j := range s.CurveProfiles[i].Channels {
			channel := &s.CurveProfiles[i].Channels[j]
			if channel.Color == "" {
				channel.Color = DefaultCurveColor(j)
			}
		}
	}
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

// settingsTarget follows only links whose next hop is in the configuration
// directory. Never inspect an out-of-directory target, even in a link chain.
func settingsTarget(path string) (string, error) {
	return settingsTargetInDir(path, filepath.Dir(path))
}

func settingsTargetInDir(path, logicalDir string) (string, error) {
	path, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	dir := filepath.Dir(path)
	logicalDir, err = filepath.Abs(logicalDir)
	if err != nil {
		return "", err
	}
	seen := make(map[string]bool)
	for {
		if seen[path] {
			return "", fmt.Errorf("configuration symlink cycle: %s", path)
		}
		seen[path] = true
		info, err := os.Lstat(path)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) && len(seen) == 1 {
				return path, nil
			}
			return "", fmt.Errorf("inspect configuration target %s: %w", path, err)
		}
		if info.Mode()&os.ModeSymlink == 0 {
			return path, nil
		}
		link, err := os.Readlink(path)
		if err != nil {
			return "", fmt.Errorf("read configuration symlink %s: %w", path, err)
		}
		if filepath.IsAbs(link) {
			if !sameSettingsDirectory(filepath.Dir(link), logicalDir) {
				return "", fmt.Errorf("configuration symlink points outside its directory: %s", path)
			}
			path = filepath.Join(dir, filepath.Base(link))
		} else {
			if filepath.Dir(link) != "." || filepath.Clean(link) == "." || filepath.Clean(link) == ".." {
				return "", fmt.Errorf("configuration symlink points outside its directory: %s", path)
			}
			path = filepath.Join(dir, link)
		}
	}
}

func Save(path string, s Settings) error {
	return save(path, s, false)
}

// SaveWithIdentity returns an open handle to the file written. The caller owns
// and must close any non-nil file returned. A blank path writes nothing.
func SaveWithIdentity(path string, s Settings) (*os.File, error) {
	var file *os.File
	if err := saveFile(path, s, false, &file); err != nil {
		return nil, err
	}
	return file, nil
}

// save creates a new configuration without replacing a file that appeared
// after the load attempt when createOnly is true.
func save(path string, s Settings, createOnly bool) error {
	return saveFile(path, s, createOnly, nil)
}

func saveFile(path string, s Settings, createOnly bool, opened **os.File) error {
	return saveFileInDir(path, filepath.Dir(path), s, createOnly, opened)
}

func saveFileInDir(path, logicalDir string, s Settings, createOnly bool, opened **os.File) error {
	if path == "" {
		return defaultSettingsErr
	}
	if err := prepareSettings(&s); err != nil {
		return err
	}
	if !createOnly {
		var err error
		path, err = settingsTargetInDir(path, logicalDir)
		if err != nil {
			return err
		}
	}
	dir := filepath.Dir(path)
	file, err := createSettingsTemp(dir)
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	return publishSettingsFile(file, path, s, createOnly, opened)
}

func prepareSettings(s *Settings) error {
	if defaultSettingsErr != nil {
		return defaultSettingsErr
	}
	if len(s.Presets) > PresetCount {
		return i18n.NewError(
			fmt.Sprintf("预设数量过多: 共 %d 条, 最多支持 %d 条", len(s.Presets), PresetCount),
			fmt.Sprintf("Too many presets: got %d, maximum %d", len(s.Presets), PresetCount),
		)
	}
	// Curve edits and acquisition validate at their own boundaries. Persistence must
	// retain legacy invalid profiles when unrelated settings are saved.
	s.ReceiveHexBytesPerLine = NormalizeReceiveHexBytesPerLine(s.ReceiveHexBytesPerLine)
	s.ReceiveFont = NormalizeReceiveFont(s.ReceiveFont)
	s.ReceiveFontSize = NormalizeReceiveFontSize(s.ReceiveFontSize)
	s.ReceiveFontColor = NormalizeReceiveFontColor(s.ReceiveFontColor)
	s.CurveSidebarWidth = NormalizeCurveSidebarWidth(s.CurveSidebarWidth)
	// An explicit empty list records manual deletion and must survive reload.
	if s.BaudRates == nil {
		s.BaudRates = []string{}
	}
	return nil
}

func publishSettingsFile(file *os.File, path string, s Settings, createOnly bool, opened **os.File) error {
	presets := s.Presets
	if presets == nil {
		presets = []Preset{}
	}
	stored := fileSettings{Settings: &s, Presets: presets}
	writeErr := toml.NewEncoder(file).Encode(stored)
	var duplicate *os.File
	var dupErr error
	if writeErr == nil && opened != nil {
		duplicate, dupErr = duplicateSettingsFile(file)
	}
	closeErr := file.Close()
	if err := errors.Join(writeErr, dupErr, closeErr); err != nil {
		if duplicate != nil {
			duplicate.Close()
		}
		return err
	}
	var err error
	if createOnly {
		err = installSettingsFile(file.Name(), path)
	} else {
		err = os.Rename(file.Name(), path)
	}
	if err != nil {
		if duplicate != nil {
			duplicate.Close()
		}
		return err
	}
	if opened != nil {
		*opened = duplicate
	}
	return nil
}
