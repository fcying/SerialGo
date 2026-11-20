package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/fcying/SerialGo/internal/i18n"
)

func TestCurveColorRGBA(t *testing.T) {
	for _, test := range []struct {
		value string
		rgba  uint32
		valid bool
	}{
		{"#1677CC", 0x1677CCFF, true},
		{"#aBcDeF80", 0xABCDEF80, true},
		{"#000000", 0x000000FF, true},
		{"#00000000", 0, true},
		{"#FFFFFF00", 0xFFFFFF00, true},
		{"", 0, false},
		{"1677CC", 0, false},
		{"#123", 0, false},
		{"#1234567", 0, false},
		{"#123456789", 0, false},
		{" #1677CC", 0, false},
		{"#1677CC ", 0, false},
		{"#GG0000", 0, false},
		{"#+12345", 0, false},
	} {
		rgba, valid := ParseCurveColor(test.value)
		if rgba != test.rgba || valid != test.valid {
			t.Fatalf("ParseCurveColor(%q) = %#08x, %v; want %#08x, %v", test.value, rgba, valid, test.rgba, test.valid)
		}
	}
	palette := []string{"#1677CC", "#D94B35", "#168451", "#944AC4", "#B87308", "#07858D", "#CB3977", "#555B66"}
	for i, want := range palette {
		if DefaultCurveColor(i) != want || DefaultCurveColor(i+len(palette)) != want {
			t.Fatalf("default color %d does not preserve the existing palette", i)
		}
	}
	if DefaultCurveColor(-1) != palette[0] || NewCurveProfile("device").Channels[0].Color != palette[0] {
		t.Fatal("negative indexes and new profiles must use the first explicit default color")
	}
}

func TestCurveColorsPersistAndLegacyLoad(t *testing.T) {
	settings := defaults()
	profile := NewCurveProfile("device")
	profile.Channels[0].Color = "#00000000"
	other := profile.Channels[0]
	other.Name, other.Color = "other", "#aBcDeF80"
	profile.Channels = append(profile.Channels, other)
	settings.CurveProfiles = []CurveProfile{profile}
	path := filepath.Join(t.TempDir(), "colors.toml")
	err := Save(path, settings)
	if err != nil {
		t.Fatal(err)
	}
	loaded, _, err := load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(loaded.CurveProfiles, settings.CurveProfiles) {
		t.Fatalf("colors did not round trip: %+v", loaded.CurveProfiles)
	}

	stored := `CurveProfile = 0
[[CurveProfiles]]
Name = 'legacy'
TimeScale = 0.001
[[CurveProfiles.Channels]]
Name = 'disabled'
Mode = 'prefix'
Pattern = 'A='
Scale = 1
Enabled = false
[[CurveProfiles.Channels]]
Name = 'enabled'
Mode = 'prefix'
Pattern = 'B='
Scale = 1
Enabled = true
Color = ''
[[CurveProfiles.Channels]]
Name = 'invalid'
Mode = 'prefix'
Pattern = 'C='
Scale = 1
Enabled = false
Color = '#nothex'
[[CurveProfiles.Channels]]
Name = 'transparent'
Mode = 'prefix'
Pattern = 'D='
Scale = 1
Enabled = true
Color = '#00000000'
`
	err = os.WriteFile(path, []byte(stored), 0600)
	if err != nil {
		t.Fatal(err)
	}
	loaded, loadedPath, err := load(path)
	if err != nil || loadedPath != path {
		t.Fatalf("legacy or invalid colors prevented load: %s, %v", loadedPath, err)
	}
	want := []string{DefaultCurveColor(0), DefaultCurveColor(1), "#nothex", "#00000000"}
	for i, channel := range loaded.CurveProfiles[0].Channels {
		if channel.Color != want[i] {
			t.Fatalf("loaded channel %d color %q; want %q", i, channel.Color, want[i])
		}
	}
	// Defaults become channel properties before enable changes or deletion can reorder them.
	loaded.CurveProfiles[0].Channels = loaded.CurveProfiles[0].Channels[1:]
	loaded.CurveProfiles[0].Channels[0].Enabled = false
	if loaded.CurveProfiles[0].Channels[0].Color != DefaultCurveColor(1) {
		t.Fatal("deleting another legacy channel shifted its assigned color")
	}
	err = ValidateCurveProfiles(loaded.CurveProfiles, 0)
	if err == nil || !strings.Contains(i18n.ErrorText(err, true), "#RRGGBB") || !strings.Contains(i18n.ErrorText(err, false), "颜色") {
		t.Fatalf("invalid nonempty color must retain bilingual validation: %v", err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != stored {
		t.Fatal("loading colors rewrote the original file")
	}
	profile.Channels[0].Color = ""
	err = ValidateCurveProfiles([]CurveProfile{profile}, 0)
	if err != nil {
		t.Fatalf("manually constructed legacy channels must allow empty colors: %v", err)
	}
}

func TestCurveColorDoesNotChangeSchema(t *testing.T) {
	profile := NewCurveProfile("device")
	changed := CloneCurveProfiles([]CurveProfile{profile})[0]
	changed.Name = "renamed profile"
	changed.Channels[0].Color = "#00000000"
	if !SameCurveSchema(profile, changed) {
		t.Fatal("display color and profile name must not reset acquisition")
	}
	for _, edit := range []func(*CurveProfile){
		func(p *CurveProfile) { p.TimePattern = `t=([0-9]+)` },
		func(p *CurveProfile) { p.TimeScale = 1 },
		func(p *CurveProfile) { p.Channels[0].Name = "renamed channel" },
		func(p *CurveProfile) { p.Channels[0].Unit = "A" },
		func(p *CurveProfile) { p.Channels[0].Mode = "regex" },
		func(p *CurveProfile) { p.Channels[0].Pattern = "I=" },
		func(p *CurveProfile) { p.Channels[0].Scale = 2 },
		func(p *CurveProfile) { p.Channels[0].Offset = 1 },
		func(p *CurveProfile) { p.Channels[0].Enabled = false },
		func(p *CurveProfile) { p.Channels = nil },
	} {
		changed = CloneCurveProfiles([]CurveProfile{profile})[0]
		edit(&changed)
		if SameCurveSchema(profile, changed) {
			t.Fatalf("acquisition schema change was ignored: %+v", changed)
		}
	}
}
