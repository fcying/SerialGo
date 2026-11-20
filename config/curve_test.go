package config

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestCurveProfilesPersistAndClone(t *testing.T) {
	settings := defaults()
	if len(settings.CurveProfiles) != 0 {
		t.Fatal("missing profiles must remain empty")
	}
	settings.CurveProfiles = []CurveProfile{NewCurveProfile("固件 A"), NewCurveProfile("固件 B")}
	settings.CurveProfile = 1
	for i := 1; i < 32; i++ {
		channel := settings.CurveProfiles[0].Channels[0]
		channel.Name = fmt.Sprintf("Channel %d", i)
		channel.Pattern = fmt.Sprintf("C%d=", i)
		channel.Color = DefaultCurveColor(i)
		settings.CurveProfiles[0].Channels = append(settings.CurveProfiles[0].Channels, channel)
	}
	if settings.CurveProfiles[0].TimePattern != "" || settings.CurveProfiles[0].TimeScale != .001 {
		t.Fatal("默认方案必须使用系统时间并保留毫秒转秒倍率")
	}
	settings.CurveProfiles[0].TimePattern = `t=([0-9]+)`
	settings.CurveProfiles[0].TimeScale = .01
	cloned := CloneCurveProfiles(settings.CurveProfiles)
	cloned[0].Channels[0].Pattern = "OTHER="
	cloned[1].Name = "changed"
	cloned[0].TimePattern = `other=([0-9]+)`
	cloned[0].TimeScale = 2
	if settings.CurveProfiles[0].Channels[0].Pattern != "VBAT=" || settings.CurveProfiles[1].Name != "固件 B" || settings.CurveProfiles[0].TimePattern != `t=([0-9]+)` || settings.CurveProfiles[0].TimeScale != .01 {
		t.Fatal("clone aliases source profiles or channels")
	}
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := Save(path, settings); err != nil {
		t.Fatal(err)
	}
	got := defaults()
	if err := decode(path, &got); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.CurveProfiles, settings.CurveProfiles) || got.CurveProfile != 1 {
		t.Fatal("curve rules did not round trip")
	}
}

func TestCurveValidationLimits(t *testing.T) {
	profile := NewCurveProfile("firmware")
	for _, mutate := range []func(*CurveProfile){
		func(p *CurveProfile) { p.TimePattern = strings.Repeat("x", MaxCurvePatternBytes+1) },
		func(p *CurveProfile) { p.TimePattern = "t=(.*)\n" },
		func(p *CurveProfile) { p.TimeScale = math.Inf(1) },
		func(p *CurveProfile) { p.TimeScale = math.NaN() },
		func(p *CurveProfile) { p.TimePattern, p.TimeScale = `t=([0-9]+)`, 0 },
		func(p *CurveProfile) { p.TimePattern, p.TimeScale = `t=([0-9]+)`, -1 },
		func(p *CurveProfile) { p.Channels[0].Scale = math.Inf(1) },
		func(p *CurveProfile) { p.Channels[0].Offset = math.NaN() },
		func(p *CurveProfile) { p.Channels[0].Pattern = strings.Repeat("x", MaxCurvePatternBytes+1) },
		func(p *CurveProfile) { p.Channels[0].Mode = "unknown" },
	} {
		bad := CloneCurveProfiles([]CurveProfile{profile})
		mutate(&bad[0])
		if err := ValidateCurveProfiles(bad, 0); err == nil {
			t.Fatal("invalid curve accepted")
		}
	}
	if err := ValidateCurveProfiles(make([]CurveProfile, CurveProfileLimit+1), 0); err == nil {
		t.Fatal("too many profiles accepted")
	}
	for _, scale := range []float64{0, -1} {
		profile.TimeScale = scale
		if err := ValidateCurveProfiles([]CurveProfile{profile}, 0); err != nil {
			t.Fatalf("系统时间模式不应要求正倍率: %v", err)
		}
	}
}

func TestCurveInvalidTimeSettingsLoadUnchanged(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.toml")
	stored := `CurveProfile = 1
[[CurveProfiles]]
Name = 'legacy'
TimeScale = 0
TimePattern = '['
[[CurveProfiles.Channels]]
Name = 'voltage'
Mode = 'prefix'
Pattern = 'VBAT='
Unit = 'V'
Scale = 0.001
Offset = -1.5
Enabled = true
[[CurveProfiles.Channels]]
Name = 'disabled'
Mode = 'regex'
Pattern = 'I=([0-9]+)'
Unit = 'A'
Scale = 2
Offset = 3
Enabled = false
[[CurveProfiles]]
Name = 'other'
TimeScale = 0.5
TimePattern = 't=([0-9]+)'
[[CurveProfiles.Channels]]
Name = 'temperature'
Mode = 'prefix'
Pattern = 'T='
Unit = 'C'
Scale = 1
Offset = -40
Enabled = true
`
	if err := os.WriteFile(path, []byte(stored), 0600); err != nil {
		t.Fatal(err)
	}
	settings, loadedPath, err := load(path)
	if err != nil {
		t.Fatalf("无效时间设置不应阻止加载: %v", err)
	}
	want := []CurveProfile{
		{Name: "legacy", TimePattern: "[", TimeScale: 0, Channels: []CurveChannel{
			{Name: "voltage", Mode: "prefix", Pattern: "VBAT=", Unit: "V", Color: DefaultCurveColor(0), Scale: .001, Offset: -1.5, Enabled: true},
			{Name: "disabled", Mode: "regex", Pattern: `I=([0-9]+)`, Unit: "A", Color: DefaultCurveColor(1), Scale: 2, Offset: 3, Enabled: false},
		}},
		{Name: "other", TimePattern: `t=([0-9]+)`, TimeScale: .5, Channels: []CurveChannel{
			{Name: "temperature", Mode: "prefix", Pattern: "T=", Unit: "C", Color: DefaultCurveColor(0), Scale: 1, Offset: -40, Enabled: true},
		}},
	}
	if loadedPath != path || settings.CurveProfile != 1 || !reflect.DeepEqual(settings.CurveProfiles, want) {
		t.Fatalf("legacy load changed profile or channel settings: %+v", settings.CurveProfiles)
	}
	if err := ValidateCurveProfiles(settings.CurveProfiles, settings.CurveProfile); err == nil {
		t.Fatal("使用设备时间时必须拒绝无效倍率")
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != stored {
		t.Fatal("loading rewrote the original legacy file")
	}
	settings.Language = "en"
	err = Save(path, settings)
	if err != nil {
		t.Fatalf("invalid legacy curves blocked an unrelated language save: %v", err)
	}
	reloaded, _, err := load(path)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Language != "en" || reloaded.CurveProfile != settings.CurveProfile || !reflect.DeepEqual(reloaded.CurveProfiles, want) {
		t.Fatal("unrelated settings save changed legacy curve values")
	}
}
