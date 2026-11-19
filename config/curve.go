package config

import (
	"fmt"
	"math"
	"slices"
	"strings"

	"github.com/fcying/SerialGo/internal/i18n"
)

const (
	CurveProfileLimit                = 16
	MaxCurvePatternBytes             = 1024
	DefaultCurveSidebarWidth float32 = 260
)

// NormalizeCurveSidebarWidth preserves the preferred width independently of window size.
func NormalizeCurveSidebarWidth(width float32) float32 {
	if width <= 0 || math.IsNaN(float64(width)) || math.IsInf(float64(width), 0) {
		return DefaultCurveSidebarWidth
	}
	return width
}

type CurveChannel struct {
	Name, Mode, Pattern, Unit, Color string
	Scale, Offset                    float64
	Enabled                          bool
}

type CurveProfile struct {
	Name        string
	TimePattern string
	TimeScale   float64
	Channels    []CurveChannel
}

var defaultCurveColors = [...]string{
	"#1677CC", "#D94B35", "#168451", "#944AC4", "#B87308", "#07858D", "#CB3977", "#555B66",
}

// DefaultCurveColor assigns the palette by configured channel index, including disabled channels.
func DefaultCurveColor(index int) string {
	if index < 0 {
		index = 0
	}
	return defaultCurveColors[index%len(defaultCurveColors)]
}

// ParseCurveColor accepts only #RRGGBB or #RRGGBBAA and returns packed RGBA.
func ParseCurveColor(value string) (uint32, bool) {
	if (len(value) != 7 && len(value) != 9) || value[0] != '#' {
		return 0, false
	}
	var rgba uint32
	for i := 1; i < len(value); i++ {
		digit := value[i]
		switch {
		case digit >= '0' && digit <= '9':
			digit -= '0'
		case digit >= 'a' && digit <= 'f':
			digit = digit - 'a' + 10
		case digit >= 'A' && digit <= 'F':
			digit = digit - 'A' + 10
		default:
			return 0, false
		}
		rgba = rgba<<4 | uint32(digit)
	}
	if len(value) == 7 {
		rgba = rgba<<8 | 0xFF
	}
	return rgba, true
}

// SameCurveSchema preserves acquisition reset semantics while ignoring display colors.
// Profile names are metadata; channel names and units remain part of the schema.
func SameCurveSchema(a, b CurveProfile) bool {
	if a.TimePattern != b.TimePattern || a.TimeScale != b.TimeScale || len(a.Channels) != len(b.Channels) {
		return false
	}
	for i, channel := range a.Channels {
		other := b.Channels[i]
		channel.Color, other.Color = "", ""
		if channel != other {
			return false
		}
	}
	return true
}

func CloneCurveProfiles(profiles []CurveProfile) []CurveProfile {
	cloned := slices.Clone(profiles)
	for i := range cloned {
		cloned[i].Channels = slices.Clone(cloned[i].Channels)
	}
	return cloned
}

func NewCurveProfile(name string) CurveProfile {
	return CurveProfile{Name: name, TimeScale: .001, Channels: []CurveChannel{
		{Name: "VBAT", Mode: "prefix", Pattern: "VBAT=", Unit: "V", Color: DefaultCurveColor(0), Scale: .001, Enabled: true},
	}}
}

// ValidateCurveProfiles checks storage limits; the acquisition layer compiles regexes.
func ValidateCurveProfiles(profiles []CurveProfile, active int) error {
	if len(profiles) > CurveProfileLimit {
		return i18n.NewError("曲线方案最多支持 16 个", "At most 16 curve profiles are supported")
	}
	if len(profiles) == 0 {
		if active != 0 && active != -1 {
			return i18n.NewError("曲线方案索引无效", "Invalid curve profile index")
		}
		return nil
	}
	if active < 0 || active >= len(profiles) {
		return i18n.NewError("曲线方案索引无效", "Invalid curve profile index")
	}
	finite := func(value float64) bool { return !math.IsNaN(value) && !math.IsInf(value, 0) }
	for i, profile := range profiles {
		var err error
		switch {
		case strings.TrimSpace(profile.Name) == "" || len(profile.Name) > 128:
			err = i18n.NewError("方案名称不能为空且不能超过 128 字节", "Profile name must contain 1–128 bytes")
		case len(profile.TimePattern) > MaxCurvePatternBytes || strings.ContainsAny(profile.TimePattern, "\r\n"):
			err = i18n.NewError("时间正则最多支持 1024 字节且不能包含换行", "Time regex allows at most 1024 bytes without line breaks")
		case !finite(profile.TimeScale):
			err = i18n.NewError("时间倍率必须是有限数值", "Time scale must be finite")
		case profile.TimePattern != "" && profile.TimeScale <= 0:
			err = i18n.NewError("设备时间倍率必须大于零", "Device time scale must be positive")
		case len(profile.Channels) == 0:
			err = i18n.NewError("每个方案至少需要一个通道", "Each profile requires at least one channel")
		}
		if err != nil {
			return i18n.WrapError(fmt.Sprintf("曲线方案 %d 无效", i+1), fmt.Sprintf("Invalid curve profile %d", i+1), err)
		}
		for j, channel := range profile.Channels {
			_, validColor := ParseCurveColor(channel.Color)
			switch {
			case strings.TrimSpace(channel.Name) == "" || len(channel.Name) > 128 || len(channel.Unit) > 64:
				err = i18n.NewError("通道名称需要 1 至 128 字节，单位最多 64 字节", "Channel name requires 1–128 bytes; unit allows at most 64 bytes")
			case channel.Mode != "prefix" && channel.Mode != "regex":
				err = i18n.NewError("通道模式必须是 prefix 或 regex", "Channel mode must be prefix or regex")
			case channel.Pattern == "" || len(channel.Pattern) > MaxCurvePatternBytes || strings.ContainsAny(channel.Pattern, "\r\n"):
				err = i18n.NewError("通道规则需要 1 至 1024 字节且不能包含换行", "Channel pattern requires 1–1024 bytes without line breaks")
			case !finite(channel.Scale) || !finite(channel.Offset):
				err = i18n.NewError("通道倍率和偏移必须是有限数值", "Channel scale and offset must be finite")
			case channel.Color != "" && !validColor:
				err = i18n.NewError("通道颜色必须是 #RRGGBB 或 #RRGGBBAA", "Channel color must be #RRGGBB or #RRGGBBAA")
			}
			if err != nil {
				return i18n.WrapError(fmt.Sprintf("方案 %d 通道 %d 无效", i+1, j+1), fmt.Sprintf("Invalid profile %d channel %d", i+1, j+1), err)
			}
		}
	}
	return nil
}
