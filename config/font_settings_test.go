package config

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestReceiveFontDefaultsAndSettingsRoundTrip(t *testing.T) {
	wantDefault := struct {
		family string
		size   int
		color  string
	}{family: "宋体", size: 9, color: "#000000"}
	settings := defaults()
	if settings.ReceiveFont != wantDefault.family || settings.ReceiveFontSize != wantDefault.size || settings.ReceiveFontColor != wantDefault.color {
		t.Fatalf("original display font defaults = (%q, %d pt, %q), want (%q, %d pt, %q)",
			settings.ReceiveFont, settings.ReceiveFontSize, settings.ReceiveFontColor,
			wantDefault.family, wantDefault.size, wantDefault.color)
	}

	settings.ReceiveFont = "monospace"
	settings.ReceiveFontSize = 16
	settings.ReceiveFontColor = "#3A6BC4"
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := Save(path, settings); err != nil {
		t.Fatal(err)
	}
	got, _, err := load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.ReceiveFont != settings.ReceiveFont || got.ReceiveFontSize != settings.ReceiveFontSize || got.ReceiveFontColor != settings.ReceiveFontColor {
		t.Fatalf("receive font did not round-trip: got (%q, %d pt, %q)", got.ReceiveFont, got.ReceiveFontSize, got.ReceiveFontColor)
	}
}

func TestMissingAndInvalidReceiveFontSettingsUseTemplateWithoutWriting(t *testing.T) {
	want := defaults()
	for _, test := range []struct {
		name, source, font, color string
		size                      int
	}{
		{name: "missing", source: "SendText = \"legacy config\"\n", font: want.ReceiveFont, size: want.ReceiveFontSize, color: want.ReceiveFontColor},
		{name: "empty family", source: "ReceiveFont = \"\"\nReceiveFontSize = 12\nReceiveFontColor = \"#123456\"\n", font: want.ReceiveFont, size: 12, color: "#123456"},
		{name: "small size", source: "ReceiveFont = \"monospace\"\nReceiveFontSize = 4\nReceiveFontColor = \"#123456\"\n", font: "monospace", size: want.ReceiveFontSize, color: "#123456"},
		{name: "large size", source: "ReceiveFont = \"monospace\"\nReceiveFontSize = 73\nReceiveFontColor = \"#123456\"\n", font: "monospace", size: want.ReceiveFontSize, color: "#123456"},
		{name: "invalid color", source: "ReceiveFont = \"monospace\"\nReceiveFontSize = 12\nReceiveFontColor = \"#12XX56\"\n", font: "monospace", size: 12, color: want.ReceiveFontColor},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.toml")
			if err := os.WriteFile(path, []byte(test.source), 0600); err != nil {
				t.Fatal(err)
			}
			got, gotPath, err := load(path)
			if err != nil || gotPath != path {
				t.Fatalf("loading %q: path=%q err=%v", test.source, gotPath, err)
			}
			if got.ReceiveFont != test.font || got.ReceiveFontSize != test.size || got.ReceiveFontColor != test.color {
				t.Fatalf("font settings did not normalize for %q: got (%q, %d pt, %q)", test.source, got.ReceiveFont, got.ReceiveFontSize, got.ReceiveFontColor)
			}
			contents, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(contents, []byte(test.source)) {
				t.Fatalf("loading rewrote existing configuration %q", test.source)
			}
		})
	}
}

func TestReceiveFontSaveNormalizesColorAndInvalidValues(t *testing.T) {
	settings := defaults()
	settings.ReceiveFont = "  monospace  "
	settings.ReceiveFontSize = 100
	settings.ReceiveFontColor = "#a1b2c3"
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := Save(path, settings); err != nil {
		t.Fatal(err)
	}
	got, _, err := load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.ReceiveFont != "monospace" || got.ReceiveFontSize != DefaultReceiveFontSize || got.ReceiveFontColor != "#A1B2C3" {
		t.Fatalf("saved font settings were not normalized: got (%q, %d pt, %q)", got.ReceiveFont, got.ReceiveFontSize, got.ReceiveFontColor)
	}
}
