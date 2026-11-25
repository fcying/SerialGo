package config

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fcying/SerialGo/internal/i18n"

	"github.com/BurntSushi/toml"
)

func savedPresets(t *testing.T, path string) []Preset {
	t.Helper()
	var stored struct{ Presets []Preset }
	_, err := toml.DecodeFile(path, &stored)
	if err != nil {
		t.Fatal(err)
	}
	return stored.Presets
}

func makeConfigSymlink(t *testing.T, target, path string) {
	t.Helper()
	if err := os.Symlink(target, path); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
}

func TestConfigSymlinkSaveKeepsLinks(t *testing.T) {
	for _, absolute := range []bool{false, true} {
		t.Run(map[bool]string{false: "relative", true: "absolute"}[absolute], func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "config.toml")
			middle := filepath.Join(dir, "middle.toml")
			target := filepath.Join(dir, "actual.toml")
			if err := Save(target, defaults()); err != nil {
				t.Fatal(err)
			}
			first, second := "middle.toml", "actual.toml"
			if absolute {
				first, second = middle, target
			}
			makeConfigSymlink(t, second, middle)
			makeConfigSymlink(t, first, path)
			settings, loadedPath, err := load(path)
			if err != nil || loadedPath != path {
				t.Fatalf("load: path=%q err=%v", loadedPath, err)
			}
			settings.SendText = "updated through symlink"
			if err := Save(loadedPath, settings); err != nil {
				t.Fatal(err)
			}
			for link, want := range map[string]string{path: first, middle: second} {
				got, err := os.Readlink(link)
				if err != nil || got != want {
					t.Fatalf("link %s changed: target=%q err=%v", link, got, err)
				}
			}
			got := defaults()
			if err := decode(target, &got); err != nil || got.SendText != settings.SendText {
				t.Fatalf("target not updated: SendText=%q err=%v", got.SendText, err)
			}
		})
	}
}

func TestConfigSymlinkRejectsUnsafeTargets(t *testing.T) {
	for _, kind := range []string{"dangling", "outside", "outside chain", "cycle"} {
		t.Run(kind, func(t *testing.T) {
			dir, outside := t.TempDir(), t.TempDir()
			path := filepath.Join(dir, "config.toml")
			other := filepath.Join(outside, "other.toml")
			original := []byte("SendText = \"do not overwrite\"\n")
			if err := os.WriteFile(other, original, 0600); err != nil {
				t.Fatal(err)
			}
			target := "missing.toml"
			switch kind {
			case "outside":
				target = other
			case "outside chain":
				target = "middle.toml"
				makeConfigSymlink(t, other, filepath.Join(dir, target))
			case "cycle":
				target = "middle.toml"
				makeConfigSymlink(t, "config.toml", filepath.Join(dir, target))
			}
			makeConfigSymlink(t, target, path)
			if _, _, err := load(path); err == nil {
				t.Fatal("load accepted an unsafe symlink")
			}
			if err := Save(path, defaults()); err == nil {
				t.Fatal("save accepted an unsafe symlink")
			}
			if got, err := os.Readlink(path); err != nil || got != target {
				t.Fatalf("configuration link changed: target=%q err=%v", got, err)
			}
			if got, err := os.ReadFile(other); err != nil || !bytes.Equal(got, original) {
				t.Fatalf("outside configuration changed: %q err=%v", got, err)
			}
			if kind == "dangling" {
				if _, err := os.Lstat(filepath.Join(dir, target)); !os.IsNotExist(err) {
					t.Fatalf("dangling target was created: %v", err)
				}
			}
			if entries, err := filepath.Glob(filepath.Join(dir, ".settings-*")); err != nil || len(entries) != 0 {
				t.Fatalf("temporary files left behind: %v err=%v", entries, err)
			}
		})
	}
}

func TestDecodeRejectsTargetReplacedBySymlink(t *testing.T) {
	dir, outside := t.TempDir(), t.TempDir()
	path := filepath.Join(dir, "config.toml")
	target := filepath.Join(dir, "actual.toml")
	other := filepath.Join(outside, "outside.toml")
	if err := os.WriteFile(target, []byte("SendText = \"inside\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(other, []byte("SendText = \"outside\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	makeConfigSymlink(t, "actual.toml", path)
	resolved, err := settingsTarget(path)
	if err != nil || resolved != target {
		t.Fatalf("resolve: target=%q err=%v", resolved, err)
	}
	if err := os.Remove(target); err != nil {
		t.Fatal(err)
	}
	makeConfigSymlink(t, other, target)
	settings := defaults()
	before := settings.SendText
	if err := decode(resolved, &settings); err == nil {
		t.Fatal("decode followed a substituted symlink")
	}
	if settings.SendText != before {
		t.Fatalf("decode read outside settings: %q", settings.SendText)
	}
}

func TestCreateMissingConfigDoesNotReplaceNewFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	original := []byte("SendText = \"created concurrently\"\n")
	if err := os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}
	if err := save(path, defaults(), true); err == nil {
		t.Fatal("exclusive creation replaced an existing file")
	}
	if got, err := os.ReadFile(path); err != nil || !bytes.Equal(got, original) {
		t.Fatalf("existing configuration changed: %q err=%v", got, err)
	}
}

func TestLoadCreationFailureDoesNotReturnWritablePath(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "missing")
	path := filepath.Join(dir, "config.toml")
	settings, loadedPath, err := load(path)
	if err == nil || loadedPath != "" {
		t.Fatalf("load after creation failure: path=%q err=%v", loadedPath, err)
	}
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	original := []byte("SendText = \"created after failed load\"\n")
	if err := os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}
	if err := Save(loadedPath, settings); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(path); err != nil || !bytes.Equal(got, original) {
		t.Fatalf("unread configuration changed: %q err=%v", got, err)
	}
}

func TestCreateMissingConfigPublishesCompleteFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	want := defaults()
	want.SendText = "new configuration"
	if err := save(path, want, true); err != nil {
		t.Fatal(err)
	}
	got := defaults()
	if err := decode(path, &got); err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("created configuration: got=%+v err=%v", got, err)
	}
	if entries, err := filepath.Glob(filepath.Join(dir, ".settings-*")); err != nil || len(entries) != 0 {
		t.Fatalf("temporary files left behind: %v err=%v", entries, err)
	}
}

func TestLoadFileReturnsOpenedIdentity(t *testing.T) {
	for _, kind := range []string{"created", "existing", "invalid"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "config.toml")
			if kind != "created" {
				data := []byte("SendText = \"existing\"\n")
				if kind == "invalid" {
					data = []byte("invalid = [\n")
				}
				if err := os.WriteFile(path, data, 0600); err != nil {
					t.Fatal(err)
				}
			}
			var opened *os.File
			_, loadedPath, err := loadFile(path, &opened)
			if (err != nil) != (kind == "invalid") || opened == nil {
				t.Fatalf("load: path=%q file=%v err=%v", loadedPath, opened, err)
			}
			defer opened.Close()
			info, err := opened.Stat()
			if err != nil {
				t.Fatal(err)
			}
			actual, err := os.Stat(path)
			if err != nil || !os.SameFile(info, actual) {
				t.Fatalf("identity differs from loaded file: %v", err)
			}
			if err := os.Rename(path, filepath.Join(dir, "old.toml")); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte("replacement"), 0600); err != nil {
				t.Fatal(err)
			}
			replacement, err := os.Stat(path)
			if err != nil || os.SameFile(info, replacement) {
				t.Fatalf("identity followed a replacement: %v", err)
			}
		})
	}
}

func TestSaveWithIdentityTracksWrittenFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	file, err := SaveWithIdentity(path, defaults())
	if err != nil || file == nil {
		t.Fatalf("save: file=%v err=%v", file, err)
	}
	defer file.Close()
	identity, err := file.Stat()
	if err != nil {
		t.Fatal(err)
	}
	actual, err := os.Stat(path)
	if err != nil || !os.SameFile(identity, actual) {
		t.Fatalf("identity differs from saved file: %v", err)
	}
	if identity, err := SaveWithIdentity("", defaults()); err != nil || identity != nil {
		t.Fatalf("empty path: identity=%v err=%v", identity, err)
	}
}

func TestDefaultConfigHasFiveDistinctPresets(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	want := defaults()
	err := Save(path, want)
	if err != nil {
		t.Fatal(err)
	}
	presets := savedPresets(t, path)
	if len(presets) != 5 {
		t.Fatalf("default config has %d presets, want 5", len(presets))
	}
	names := make(map[string]bool)
	for i, preset := range presets {
		if preset.Label == "" || names[preset.Label] {
			t.Fatalf("preset %d has an empty or duplicate name: %q", i+1, preset.Label)
		}
		names[preset.Label] = true
	}
	got := defaults()
	err = decode(path, &got)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatal("default settings changed after save and reload")
	}
}

func TestConfiguredPresetsKeepEmptyEntriesAndSettings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	want := defaults()
	want.Language = "zh-CN"
	want.Port.Name, want.Port.Baud = "COM12", "1000000"
	want.SendText, want.SendHexText = "发送内容\r\n", "00 FF"
	want.ParseSendEscapes = true
	want.ChecksumAlgorithm = "CRC16-CCITT-FALSE"
	want.ChecksumHeaderBytes, want.ChecksumTailBytes = "1", "2"
	want.Presets[0] = Preset{Label: "自定义", Data: "01 03", Interval: "25", Hex: true, CRLF: true, Enabled: true}
	want.Presets[2].Label = ""
	want.Presets = append(want.Presets, NewPreset())
	if err := Save(path, want); err != nil {
		t.Fatal(err)
	}
	if presets := savedPresets(t, path); !slices.Equal(presets, want.Presets) {
		t.Fatal("saving removed a configured empty entry or changed its position")
	}
	got := defaults()
	if err := decode(path, &got); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatal("configured entries or other settings changed after reload")
	}
}

func TestLegacyFullPresetArrayKeepsLastSlot(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	var legacy struct{ Presets [PresetCount]Preset }
	for i := range legacy.Presets {
		legacy.Presets[i].Interval = defaultSettings.Presets[0].Interval
	}
	legacy.Presets[PresetCount-1].CRLF = true
	legacy.Presets[PresetCount-1].Enabled = true
	var source bytes.Buffer
	err := toml.NewEncoder(&source).Encode(legacy)
	if err != nil {
		t.Fatal(err)
	}
	err = os.WriteFile(path, source.Bytes(), 0600)
	if err != nil {
		t.Fatal(err)
	}
	loaded := defaults()
	err = decode(path, &loaded)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(loaded.Presets, legacy.Presets[:]) {
		t.Fatal("legacy preset contents or positions changed during loading")
	}
	err = Save(path, loaded)
	if err != nil {
		t.Fatal(err)
	}
	presets := savedPresets(t, path)
	if len(presets) != PresetCount || presets[PresetCount-1] != legacy.Presets[PresetCount-1] {
		t.Fatal("the flags-only last preset was trimmed or moved")
	}
}

func TestShortPresetConfigOnlyLoadsConfiguredEntries(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	err := os.WriteFile(path, []byte("[[Presets]]\nLabel = \"custom\"\n[[Presets]]\nData = \"payload\"\n"), 0600)
	if err != nil {
		t.Fatal(err)
	}
	want := defaults()
	want.Presets = want.Presets[:2]
	want.Presets[0].Label = "custom"
	want.Presets[1].Data = "payload"
	got := defaults()
	err = decode(path, &got)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatal("short configuration lost entry defaults or added unconfigured entries")
	}
}

func TestTooManyPresetsAreRejected(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	err := os.WriteFile(path, []byte(strings.Repeat("[[Presets]]\n", PresetCount+1)), 0600)
	if err != nil {
		t.Fatal(err)
	}
	settings := defaults()
	err = decode(path, &settings)
	if err == nil {
		t.Fatal("accepted a preset beyond the supported slot count")
	}
}

func TestRemovedPresetsDoNotReturnAfterReload(t *testing.T) {
	for _, source := range []string{"Presets = []\n", "SendText = \"keep\"\n"} {
		t.Run(source, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.toml")
			if err := os.WriteFile(path, []byte(source), 0600); err != nil {
				t.Fatal(err)
			}
			for range 2 {
				settings, _, err := load(path)
				if err != nil {
					t.Fatal(err)
				}
				if len(settings.Presets) != 0 {
					t.Fatalf("deleted presets returned: %+v", settings.Presets)
				}
				if err := Save(path, settings); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

func TestUnsupportedLanguageUsesEnglishWithoutChangingUserText(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	err := os.WriteFile(path, []byte("Language = \"fr\"\nSendText = \"user payload\"\n"), 0600)
	if err != nil {
		t.Fatal(err)
	}
	got := defaults()
	err = decode(path, &got)
	if err != nil {
		t.Fatal(err)
	}
	if got.Language != "en" || got.SendText != "user payload" {
		t.Fatalf("invalid language fallback changed the payload or selected an unsupported language: %q, %q", got.Language, got.SendText)
	}
}

func TestBaudRatesRoundTripWithDefaultAndCustomSelection(t *testing.T) {
	for _, selected := range []string{"115200", "250000", "38400"} {
		t.Run(selected, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.toml")
			want := defaults()
			want.BaudRates = []string{"250000", "38400", "500000"}
			want.Port.Baud = selected
			if err := Save(path, want); err != nil {
				t.Fatal(err)
			}
			got := defaults()
			if err := decode(path, &got); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("baud rates and selected baud did not round trip: got %+v, want %+v", got, want)
			}
			var keys map[string]any
			if _, err := toml.DecodeFile(path, &keys); err != nil {
				t.Fatal(err)
			}
			for _, key := range []string{"CustomBaud", "CustomBauds"} {
				if _, exists := keys[key]; exists {
					t.Fatalf("saving wrote the obsolete %s key", key)
				}
			}
		})
	}
}

func TestDeletedDefaultBaudRatesStayDeleted(t *testing.T) {
	remaining := defaults().BaudRates
	remaining = slices.DeleteFunc(remaining, func(value string) bool { return value == "115200" })
	for _, test := range []struct {
		name string
		list []string
	}{
		{"one default deleted", remaining},
		{"all deleted", []string{}},
		{"nil saved as empty", nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.toml")
			settings := defaults()
			settings.BaudRates = test.list
			settings.Port.Baud = "115200"
			for range 2 {
				if err := Save(path, settings); err != nil {
					t.Fatal(err)
				}
				var stored struct{ BaudRates []string }
				metadata, err := toml.DecodeFile(path, &stored)
				if err != nil {
					t.Fatal(err)
				}
				if !metadata.IsDefined("BaudRates") || !slices.Equal(stored.BaudRates, test.list) {
					t.Fatalf("saved baud rates omitted the authoritative list: %v", stored.BaudRates)
				}
				settings, _, err = load(path)
				if err != nil {
					t.Fatal(err)
				}
				if !slices.Equal(settings.BaudRates, test.list) {
					t.Fatalf("deleted baud rates revived: got %v, want %v", settings.BaudRates, test.list)
				}
			}
		})
	}
}

func TestLegacyCustomBaudsMigrateWithoutWriting(t *testing.T) {
	for _, test := range []struct {
		name, source string
		extra        []string
	}{
		{"missing old fields", "[Port]\nBaud = \"38400\"\n", []string{"38400"}},
		{"old array", "CustomBauds = [\"0250000\", \"38400\", \"250000\", \"115200\"]\n[Port]\nBaud = \"500000\"\n", []string{"250000", "38400", "500000"}},
		{"old scalar and distinct selected", "CustomBaud = \"0250000\"\n[Port]\nBaud = \"0038400\"\n", []string{"250000", "38400"}},
		{"all old fields", "CustomBauds = [\"250000\"]\nCustomBaud = \"38400\"\n[Port]\nBaud = \"500000\"\n", []string{"250000", "38400", "500000"}},
		{"duplicate selected", "CustomBauds = [\"0038400\"]\nCustomBaud = \"38400\"\n[Port]\nBaud = \"38400\"\n", []string{"38400"}},
		{"empty old array retains template", "CustomBauds = []\n[Port]\nBaud = \"115200\"\n", nil},
		{"default not duplicated", "CustomBaud = \"00115200\"\n[Port]\nBaud = \"115200\"\n", nil},
		{"invalid old values", "CustomBauds = [\"0\", \"4294967296\", \"1.5\", \"+38400\", \"baud\", \"\"]\nCustomBaud = \"4294967296\"\n[Port]\nBaud = \"-1\"\n", nil},
		{"valid bounds and whitespace", "CustomBauds = [\" 00001 \"]\nCustomBaud = \"4294967295\"\n[Port]\nBaud = \"0\"\n", []string{"1", "4294967295"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.toml")
			source := []byte(test.source)
			if err := os.WriteFile(path, source, 0600); err != nil {
				t.Fatal(err)
			}
			before, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			got := defaults()
			if err := decode(path, &got); err != nil {
				t.Fatal(err)
			}
			want := append(defaults().BaudRates, test.extra...)
			if !slices.Equal(got.BaudRates, want) {
				t.Fatalf("migrated baud rates = %v, want %v", got.BaudRates, want)
			}
			after, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			contents, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if !os.SameFile(before, after) || !bytes.Equal(contents, source) {
				t.Fatal("decoding a legacy configuration rewrote it")
			}
			if err := Save(path, got); err != nil {
				t.Fatal(err)
			}
			var keys map[string]any
			if _, err := toml.DecodeFile(path, &keys); err != nil {
				t.Fatal(err)
			}
			for _, key := range []string{"CustomBaud", "CustomBauds"} {
				if _, exists := keys[key]; exists {
					t.Fatalf("saving migrated settings retained the obsolete %s key", key)
				}
			}
			if _, exists := keys["BaudRates"]; !exists {
				t.Fatal("saving migrated settings omitted the new list key")
			}
		})
	}
}

func TestExplicitBaudRatesOverrideLegacyAndDoNotReviveDeletedSelection(t *testing.T) {
	for _, list := range []string{"[]", "[\"250000\"]"} {
		t.Run(list, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.toml")
			source := "BaudRates = " + list + "\nCustomBauds = [\"750000\"]\nCustomBaud = \"500000\"\n[Port]\nBaud = \"38400\"\n"
			if err := os.WriteFile(path, []byte(source), 0600); err != nil {
				t.Fatal(err)
			}
			got := defaults()
			if err := decode(path, &got); err != nil {
				t.Fatal(err)
			}
			var want []string
			if list != "[]" {
				want = []string{"250000"}
			}
			if !slices.Equal(got.BaudRates, want) || got.Port.Baud != "38400" {
				t.Fatalf("explicit list was overridden during decoding: %+v", got)
			}
			if err := Save(path, got); err != nil {
				t.Fatal(err)
			}
			reloaded := defaults()
			if err := decode(path, &reloaded); err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(reloaded.BaudRates, want) {
				t.Fatalf("deleted baud revived after save/reload: %v", reloaded.BaudRates)
			}
		})
	}
}

func useDefaultTemplate(t *testing.T, settings Settings, err error) {
	t.Helper()
	previous, previousErr := defaultSettings, defaultSettingsErr
	defaultSettings, defaultSettingsErr = settings, err
	t.Cleanup(func() {
		defaultSettings, defaultSettingsErr = previous, previousErr
	})
}

func TestTemplateValuesDriveMissingConfigAndIndependentDefaults(t *testing.T) {
	want := defaults()
	want.Port.Baud, want.Port.DataBits, want.Port.DTR = "38400", "7", false
	want.BaudRates = []string{"38400", "250000"}
	want.Language, want.Interval, want.SendText = "zh-CN", "275", "template payload"
	want.CRLF = false
	for i := range want.Presets {
		want.Presets[i].Interval = "4321"
	}
	want.Presets[0].Label = "Template preset"
	var source bytes.Buffer
	if err := toml.NewEncoder(&source).Encode(fileSettings{Settings: &want, Presets: want.Presets[:defaultPresetCount]}); err != nil {
		t.Fatal(err)
	}
	parsed, err := parseDefaultSettings(source.String())
	if err != nil {
		t.Fatal(err)
	}
	useDefaultTemplate(t, parsed, nil)
	path := filepath.Join(t.TempDir(), "config.toml")
	got, gotPath, err := load(path)
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != path || !reflect.DeepEqual(got, want) {
		t.Fatalf("missing config did not use the parsed template: path=%q settings=%+v", gotPath, got)
	}
	stored := defaults()
	if err := decode(path, &stored); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(stored, want) || len(savedPresets(t, path)) != defaultPresetCount {
		t.Fatal("template-based defaults or template interval trimming changed while saving")
	}
	legacyPath := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(legacyPath, []byte("CustomBauds = [\"500000\"]\n"), 0600); err != nil {
		t.Fatal(err)
	}
	migrated, _, err := load(legacyPath)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(migrated.BaudRates, []string{"38400", "250000", "500000"}) {
		t.Fatalf("legacy migration did not inherit the replacement template: %v", migrated.BaudRates)
	}
	migrated.BaudRates[0] = "1"
	got.BaudRates[0] = "500000"
	got.Presets[0].Interval = "1"
	fresh := defaults()
	if !reflect.DeepEqual(fresh, want) {
		t.Fatal("changing returned defaults mutated the template or a subsequent settings value")
	}
	reloaded, _, err := load(path)
	if err != nil || !reflect.DeepEqual(reloaded, want) {
		t.Fatalf("caller mutations changed the saved template defaults: settings=%+v err=%v", reloaded, err)
	}
}

func TestInvalidDefaultTemplatePreventsLoadingAndWriting(t *testing.T) {
	for _, source := range []string{"Port = [", "Language = \"en\"\n"} {
		t.Run(source, func(t *testing.T) {
			parsed, parseErr := parseDefaultSettings(source)
			if parseErr == nil {
				t.Fatal("invalid default template was accepted")
			}
			useDefaultTemplate(t, parsed, parseErr)
			if _, path, err := Load(); err == nil || path != "" {
				t.Fatalf("Load did not report the template error: path=%q err=%v", path, err)
			}
			path := filepath.Join(t.TempDir(), "config.toml")
			source := []byte("SendText = \"preserve existing config\"\n")
			if err := os.WriteFile(path, source, 0600); err != nil {
				t.Fatal(err)
			}
			if _, _, err := load(path); err == nil {
				t.Fatal("loading with an invalid template succeeded")
			}
			if err := Save(path, Settings{}); err == nil {
				t.Fatal("saving with an invalid template succeeded")
			}
			got, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, source) {
				t.Fatal("invalid template handling overwrote an existing config")
			}
			missing := filepath.Join(t.TempDir(), "config.toml")
			if _, _, err := load(missing); err == nil {
				t.Fatal("missing configuration ignored the invalid template")
			}
			if _, err := os.Stat(missing); !os.IsNotExist(err) {
				t.Fatalf("invalid template handling created a config: %v", err)
			}
		})
	}
}

func TestInvalidConfigDoesNotOverwriteFileOrDefaults(t *testing.T) {
	for _, test := range []struct {
		name, source string
	}{
		{"syntax", "BaudRates = ["},
		{"invalid baud list", "BaudRates = 115200\n"},
		{"partially decoded", "BaudRates = [\"500000\"]\nSendText = \"not a default\"\n[Port]\nBaud = 115200\n"},
		{"too many presets", "BaudRates = [\"500000\"]\n" + strings.Repeat("[[Presets]]\n", PresetCount+1)},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.toml")
			source := []byte(test.source)
			if err := os.WriteFile(path, source, 0600); err != nil {
				t.Fatal(err)
			}
			want := defaults()
			got, gotPath, err := load(path)
			if err == nil || gotPath != "" || !reflect.DeepEqual(got, want) {
				t.Fatalf("invalid configuration did not return unchanged defaults: path=%q settings=%+v err=%v", gotPath, got, err)
			}
			contents, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(contents, source) || !reflect.DeepEqual(defaults(), want) {
				t.Fatal("loading invalid configuration changed its file or the default settings")
			}
		})
	}
}

func TestDisplayBufferLimitRoundTrip(t *testing.T) {
	for _, preset := range DisplayBufferPresets() {
		t.Run(preset.Label, func(t *testing.T) {
			settings := defaults()
			settings.DisplayBufferLimit = preset.Bytes
			path := filepath.Join(t.TempDir(), "config.toml")
			if err := Save(path, settings); err != nil {
				t.Fatal(err)
			}
			got, _, err := load(path)
			if err != nil || got.DisplayBufferLimit != preset.Bytes {
				t.Fatalf("limit after reload = %d; want %d (err=%v)", got.DisplayBufferLimit, preset.Bytes, err)
			}
		})
	}
}

func TestMissingAndInvalidDisplayBufferLimitsUseTemplateWithoutWriting(t *testing.T) {
	template := defaults()
	template.DisplayBufferLimit = DisplayBufferPresets()[0].Bytes
	useDefaultTemplate(t, template, nil)
	for _, source := range []string{
		"SendText = \"legacy config\"\n",
		"DisplayBufferLimit = 0\n",
		"DisplayBufferLimit = -1\n",
		"DisplayBufferLimit = 512000\n",
		"DisplayBufferLimit = 999999999\n",
	} {
		path := filepath.Join(t.TempDir(), "config.toml")
		if err := os.WriteFile(path, []byte(source), 0600); err != nil {
			t.Fatal(err)
		}
		got, gotPath, err := load(path)
		if err != nil || gotPath != path || got.DisplayBufferLimit != template.DisplayBufferLimit {
			t.Fatalf("loading %q: limit=%d path=%q err=%v", source, got.DisplayBufferLimit, gotPath, err)
		}
		contents, err := os.ReadFile(path)
		if err != nil || string(contents) != source {
			t.Fatalf("loading rewrote config: contents=%q err=%v", contents, err)
		}
	}
	path := filepath.Join(t.TempDir(), "config.toml")
	template.DisplayBufferLimit = -1
	if err := Save(path, template); err != nil {
		t.Fatal(err)
	}
	got, _, err := load(path)
	if err != nil || got.DisplayBufferLimit != DisplayBufferPresets()[0].Bytes {
		t.Fatalf("saving invalid limit did not normalize it: limit=%d err=%v", got.DisplayBufferLimit, err)
	}
}

func TestWindowSettingsDefaultAndRoundTrip(t *testing.T) {
	wantDefault := WindowSettings{Width: 800, Height: 600}
	if got := defaults().Window; got != wantDefault {
		t.Fatalf("default window settings = %+v, want %+v", got, wantDefault)
	}
	settings := defaults()
	settings.Window = WindowSettings{X: 720, Y: 169, Width: 1000, Height: 700, PixelX: 1080, PixelY: 254, PixelWidth: 1500, PixelHeight: 1050, Maximized: true, PositionSet: true}
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := Save(path, settings); err != nil {
		t.Fatal(err)
	}
	var stored struct {
		Window WindowSettings
	}
	if _, err := toml.DecodeFile(path, &stored); err != nil {
		t.Fatal(err)
	}
	if stored.Window != settings.Window {
		t.Fatalf("stored [Window] = %+v, want %+v", stored.Window, settings.Window)
	}
	got, _, err := load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.Window != settings.Window {
		t.Fatalf("window settings after reload = %+v, want %+v", got.Window, settings.Window)
	}
}

func TestLegacyConfigUsesWindowDefaultsWithoutRewrite(t *testing.T) {
	source := "SendText = \"legacy payload\"\n"
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	got, gotPath, err := load(path)
	if err != nil || gotPath != path {
		t.Fatalf("loading legacy config: path=%q err=%v", gotPath, err)
	}
	if got.ParseSendEscapes {
		t.Fatal("legacy config enabled text escape parsing by default")
	}
	if got.ChecksumAlgorithm != "None" || got.ChecksumHeaderBytes != "0" || got.ChecksumTailBytes != "0" {
		t.Fatalf("legacy checksum defaults = %q/%q/%q, want None/0/0", got.ChecksumAlgorithm, got.ChecksumHeaderBytes, got.ChecksumTailBytes)
	}
	if got.Window != defaults().Window {
		t.Fatalf("legacy config did not receive default window settings: %+v", got.Window)
	}
	wantPlot := WindowSettings{Width: 1040, Height: 600}
	if got.CurveWindow != wantPlot {
		t.Fatalf("legacy config plot geometry = %+v, want %+v", got.CurveWindow, wantPlot)
	}
	contents, err := os.ReadFile(path)
	if err != nil || string(contents) != source {
		t.Fatalf("loading legacy config rewrote it: contents=%q err=%v", contents, err)
	}
}

func TestLegacyWindowSettingsInferSavedPosition(t *testing.T) {
	source := "[Window]\nX = 720\nY = 169\nWidth = 1000\nHeight = 700\nMaximized = false\n"
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	got, _, err := load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Window.PositionSet {
		t.Fatal("legacy window coordinates were not recognized as an explicit position")
	}
}

func TestCenteredWindowPositionFlagRoundTrip(t *testing.T) {
	settings := defaults()
	settings.Window = WindowSettings{Width: 1000, Height: 700}
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := Save(path, settings); err != nil {
		t.Fatal(err)
	}
	got, _, err := load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.Window != settings.Window {
		t.Fatalf("centered window settings = %+v, want %+v", got.Window, settings.Window)
	}
}

func TestLegacyWindowSettingsInferSavedPixelPosition(t *testing.T) {
	source := "[Window]\nX = 0\nY = 0\nWidth = 800\nHeight = 600\nPixelX = 0\nPixelY = 0\nPixelWidth = 1200\nPixelHeight = 900\n"
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	got, _, err := load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Window.PositionSet {
		t.Fatal("legacy native pixel bounds were not recognized as a saved window position")
	}
	if got.Window.PixelWidth != 1200 || got.Window.PixelHeight != 900 {
		t.Fatalf("legacy pixel bounds = %dx%d, want 1200x900", got.Window.PixelWidth, got.Window.PixelHeight)
	}
}

func TestStoreDetectsExternalChanges(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "windows" {
		t.Skip("store unsupported")
	}
	for _, change := range []string{"in-place", "replace", "remove"} {
		t.Run(change, func(t *testing.T) {
			dir := t.TempDir()
			store, err := openStore(dir)
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			settings, path, file, err := store.LoadWithIdentity()
			if err != nil {
				t.Fatal(err)
			}
			file.Close()
			external := []byte("SendText = \"external\"\n")
			switch change {
			case "in-place":
				err = os.WriteFile(path, external, 0600)
			case "replace":
				tmp := filepath.Join(dir, "replacement")
				if err = os.WriteFile(tmp, external, 0600); err == nil {
					var root *os.Root
					root, err = os.OpenRoot(dir)
					if err == nil {
						err = root.Rename("replacement", "config.toml")
						root.Close()
					}
				}
			case "remove":
				err = os.Remove(path)
			}
			if err != nil {
				t.Fatal(err)
			}
			settings.SendText = "stale"
			file, err = store.SaveWithIdentity(path, settings)
			if file != nil {
				file.Close()
			}
			if err == nil {
				t.Fatal("stale save succeeded")
			}
			if zh, en := i18n.ErrorText(err, false), i18n.ErrorText(err, true); !strings.Contains(zh, "重启") || !strings.Contains(en, "restart") {
				t.Fatalf("conflict recovery is not localized: zh=%q en=%q", zh, en)
			}
			got, err := os.ReadFile(path)
			if change == "remove" {
				if !os.IsNotExist(err) {
					t.Fatalf("removed config recreated: %v", err)
				}
			} else if err != nil || !bytes.Equal(got, external) {
				t.Fatalf("external content overwritten: %q, %v", got, err)
			}
		})
	}
}

func TestStoreConcurrentProcesses(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "windows" {
		t.Skip("store unsupported")
	}
	if dir := os.Getenv("SERIALGO_STORE_TEST_DIR"); dir != "" {
		id := os.Getenv("SERIALGO_STORE_TEST_ID")
		store, err := openStore(dir)
		if err != nil {
			t.Fatal(err)
		}
		defer store.Close()
		settings, path, file, err := store.LoadWithIdentity()
		if err != nil {
			t.Fatal(err)
		}
		file.Close()
		if err = os.WriteFile(filepath.Join(dir, "ready-"+id), nil, 0600); err != nil {
			t.Fatal(err)
		}
		deadline := time.Now().Add(15 * time.Second)
		for {
			if _, err = os.Stat(filepath.Join(dir, "go")); err == nil {
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("timed out waiting for start")
			}
			time.Sleep(10 * time.Millisecond)
		}
		settings.SendText = id
		file, err = store.SaveWithIdentity(path, settings)
		if file != nil {
			file.Close()
		}
		if err != nil && !strings.Contains(i18n.ErrorText(err, true), "Configuration changed") {
			t.Fatal(err)
		}
		result := "conflict"
		if err == nil {
			result = "saved"
		}
		if err = os.WriteFile(filepath.Join(dir, "result-"+id), []byte(result), 0600); err != nil {
			t.Fatal(err)
		}
		return
	}
	dir := t.TempDir()
	store, err := openStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	_, _, file, err := store.LoadWithIdentity()
	if err != nil {
		t.Fatal(err)
	}
	file.Close()
	store.Close()
	commands := make([]*exec.Cmd, 2)
	for i, id := range []string{"one", "two"} {
		cmd := exec.Command(os.Args[0], "-test.run=^TestStoreConcurrentProcesses$")
		cmd.Env = append(os.Environ(), "SERIALGO_STORE_TEST_DIR="+dir, "SERIALGO_STORE_TEST_ID="+id)
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		commands[i] = cmd
		defer func() {
			if cmd.ProcessState == nil {
				cmd.Process.Kill()
				cmd.Wait()
			}
		}()
	}
	deadline := time.Now().Add(15 * time.Second)
	for {
		_, one := os.Stat(filepath.Join(dir, "ready-one"))
		_, two := os.Stat(filepath.Join(dir, "ready-two"))
		if one == nil && two == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for children")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := os.WriteFile(filepath.Join(dir, "go"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	for _, cmd := range commands {
		if err := cmd.Wait(); err != nil {
			t.Fatal(err)
		}
	}
	one, err := os.ReadFile(filepath.Join(dir, "result-one"))
	if err != nil {
		t.Fatal(err)
	}
	two, err := os.ReadFile(filepath.Join(dir, "result-two"))
	if err != nil {
		t.Fatal(err)
	}
	if !((string(one) == "saved" && string(two) == "conflict") || (string(two) == "saved" && string(one) == "conflict")) {
		t.Fatalf("results: %s, %s", one, two)
	}
	data, err := os.ReadFile(filepath.Join(dir, "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	winner := "one"
	if string(two) == "saved" {
		winner = "two"
	}
	if !bytes.Contains(data, []byte("SendText = \""+winner+"\"")) {
		t.Fatalf("winner not persisted: %s", data)
	}
}

func TestStoreTracksLinkTargetAndOwnSaves(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "windows" {
		t.Skip("store unsupported")
	}
	dir := t.TempDir()
	first, second := filepath.Join(dir, "first.toml"), filepath.Join(dir, "second.toml")
	if err := os.WriteFile(first, []byte("SendText = \"first\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(second, []byte("SendText = \"second\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "config.toml")
	makeConfigSymlink(t, "first.toml", link)
	store, err := openStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	settings, path, file, err := store.LoadWithIdentity()
	if err != nil {
		t.Fatal(err)
	}
	file.Close()
	for _, value := range []string{"one", "two"} {
		settings.SendText = value
		file, err = store.SaveWithIdentity(path, settings)
		if err != nil {
			t.Fatal(err)
		}
		file.Close()
	}
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	makeConfigSymlink(t, "second.toml", link)
	file, err = store.SaveWithIdentity(path, settings)
	if file != nil {
		file.Close()
	}
	if err == nil {
		t.Fatal("save accepted a changed link target")
	}
	data, err := os.ReadFile(second)
	if err != nil || string(data) != "SendText = \"second\"\n" {
		t.Fatalf("new target changed: %q, %v", data, err)
	}
}

func TestStoreLockFailurePreservesConfig(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "windows" {
		t.Skip("store unsupported")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	original := []byte("SendText = \"untouched\"\n")
	if err := os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, ".config.toml.lock"), 0700); err != nil {
		t.Fatal(err)
	}
	store, err := openStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	settings, _, file, err := store.LoadWithIdentity()
	if err != nil {
		t.Fatal(err)
	}
	file.Close()
	if settings.SendText != "untouched" {
		t.Fatalf("loaded %q", settings.SendText)
	}
	file, err = store.SaveWithIdentity(path, defaults())
	if file != nil {
		file.Close()
	}
	if err == nil {
		t.Fatal("save succeeded without the lock")
	}
	data, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(data, original) {
		t.Fatalf("configuration changed: %q, %v", data, err)
	}
}

func TestStoreLockContentionIsBounded(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "windows" {
		t.Skip("store unsupported")
	}
	dir := t.TempDir()
	first, err := openStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := openStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	_, path, file, err := second.LoadWithIdentity()
	if err != nil {
		t.Fatal(err)
	}
	file.Close()
	unlock, err := lockStore(first)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	start := time.Now()
	file, err = second.SaveWithIdentity(path, defaults())
	if file != nil {
		file.Close()
	}
	if err == nil || time.Since(start) > 5*time.Second {
		t.Fatalf("contended save: err=%v elapsed=%v", err, time.Since(start))
	}
	if zh, en := i18n.ErrorText(err, false), i18n.ErrorText(err, true); !strings.Contains(zh, "超时") || !strings.Contains(en, "Timed out") {
		t.Fatalf("lock timeout is not localized: zh=%q en=%q", zh, en)
	}
}

func TestStoreConcurrentFirstLoad(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "windows" {
		t.Skip("store unsupported")
	}
	for round := 0; round < 8; round++ {
		dir := t.TempDir()
		start := make(chan struct{})
		errs := make(chan error, 8)
		for i := 0; i < cap(errs); i++ {
			go func() {
				store, err := openStore(dir)
				if err != nil {
					errs <- err
					return
				}
				<-start
				_, path, file, err := store.LoadWithIdentity()
				if file != nil {
					file.Close()
				}
				if err == nil && path != filepath.Join(dir, "config.toml") {
					err = errors.New("load returned an incorrect configuration path")
				}
				closeErr := store.Close()
				if err == nil {
					err = closeErr
				}
				errs <- err
			}()
		}
		close(start)
		var firstErr error
		for i := 0; i < cap(errs); i++ {
			err := <-errs
			if firstErr == nil {
				firstErr = err
			}
		}
		if firstErr != nil {
			t.Fatalf("round %d: simultaneous first load: %v", round, firstErr)
		}
	}
}
