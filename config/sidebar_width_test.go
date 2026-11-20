package config

import (
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/BurntSushi/toml"
)

func TestNormalizeCurveSidebarWidth(t *testing.T) {
	for _, test := range []struct {
		name        string
		width, want float32
	}{
		{name: "zero", width: 0, want: 260},
		{name: "negative", width: -12, want: 260},
		{name: "nan", width: float32(math.NaN()), want: 260},
		{name: "positive infinity", width: float32(math.Inf(1)), want: 260},
		{name: "negative infinity", width: float32(math.Inf(-1)), want: 260},
		{name: "fraction", width: 312.75, want: 312.75},
		{name: "small positive", width: .125, want: .125},
		{name: "large positive", width: math.MaxFloat32, want: math.MaxFloat32},
	} {
		t.Run(test.name, func(t *testing.T) {
			got := NormalizeCurveSidebarWidth(test.width)
			if got != test.want {
				t.Fatalf("NormalizeCurveSidebarWidth(%v) = %v, want %v", test.width, got, test.want)
			}
		})
	}
}

func TestCurveSidebarWidthLoadDefaultsAndNormalizationWithoutWriting(t *testing.T) {
	for _, test := range []struct {
		name, source string
		want         float32
	}{
		{name: "legacy missing width", source: "SendText = \"legacy\"\n", want: 260},
		{name: "zero", source: "CurveSidebarWidth = 0\n", want: 260},
		{name: "negative", source: "CurveSidebarWidth = -30.5\n", want: 260},
		{name: "nan", source: "CurveSidebarWidth = nan\n", want: 260},
		{name: "positive infinity", source: "CurveSidebarWidth = +inf\n", want: 260},
		{name: "negative infinity", source: "CurveSidebarWidth = -inf\n", want: 260},
		{name: "fraction", source: "CurveSidebarWidth = 417.25\n", want: 417.25},
		{name: "wider than window", source: "CurveSidebarWidth = 1600.5\n[Window]\nWidth = 400\n", want: 1600.5},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.toml")
			err := os.WriteFile(path, []byte(test.source), 0600)
			if err != nil {
				t.Fatal(err)
			}
			settings, gotPath, err := load(path)
			if err != nil || gotPath != path {
				t.Fatalf("load: path=%q err=%v", gotPath, err)
			}
			if settings.CurveSidebarWidth != test.want {
				t.Fatalf("loaded sidebar width = %v, want %v", settings.CurveSidebarWidth, test.want)
			}
			contents, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if string(contents) != test.source {
				t.Fatal("loading rewrote the existing configuration")
			}
		})
	}
}

func TestCurveSidebarWidthNewConfigurationDefault(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	settings, gotPath, err := load(path)
	if err != nil || gotPath != path {
		t.Fatalf("load: path=%q err=%v", gotPath, err)
	}
	if settings.CurveSidebarWidth != 260 {
		t.Fatalf("initial sidebar width = %v, want 260", settings.CurveSidebarWidth)
	}
	var stored struct{ CurveSidebarWidth float32 }
	_, err = toml.DecodeFile(path, &stored)
	if err != nil {
		t.Fatal(err)
	}
	if stored.CurveSidebarWidth != 260 {
		t.Fatalf("initial saved sidebar width = %v, want 260", stored.CurveSidebarWidth)
	}
}

func TestCurveSidebarWidthSaveAndLoad(t *testing.T) {
	for _, test := range []struct {
		name        string
		width, want float32
	}{
		{name: "fraction", width: 333.625, want: 333.625},
		{name: "wider than window", width: 1800.25, want: 1800.25},
		{name: "zero", width: 0, want: 260},
		{name: "negative", width: -1, want: 260},
		{name: "nan", width: float32(math.NaN()), want: 260},
		{name: "positive infinity", width: float32(math.Inf(1)), want: 260},
		{name: "negative infinity", width: float32(math.Inf(-1)), want: 260},
	} {
		t.Run(test.name, func(t *testing.T) {
			settings := defaults()
			settings.CurveSidebarWidth = test.width
			settings.Window.Width = 400
			path := filepath.Join(t.TempDir(), "config.toml")
			err := Save(path, settings)
			if err != nil {
				t.Fatal(err)
			}
			var stored struct{ CurveSidebarWidth float32 }
			_, err = toml.DecodeFile(path, &stored)
			if err != nil {
				t.Fatal(err)
			}
			if stored.CurveSidebarWidth != test.want {
				t.Fatalf("saved sidebar width = %v, want %v", stored.CurveSidebarWidth, test.want)
			}
			loaded, _, err := load(path)
			if err != nil {
				t.Fatal(err)
			}
			if loaded.CurveSidebarWidth != test.want {
				t.Fatalf("reloaded sidebar width = %v, want %v", loaded.CurveSidebarWidth, test.want)
			}
		})
	}
}
