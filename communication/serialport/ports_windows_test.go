package serialport

import "testing"

func TestPortDescription(t *testing.T) {
	for _, test := range []struct {
		name, description, want string
	}{
		{"COM3", "USB Serial Device (COM3)", "USB Serial Device"},
		{"COM3", "USB Serial Device (COM30)", "USB Serial Device (COM30)"},
		{"COM3", "Adapter (COM3) channel A", "Adapter (COM3) channel A"},
		{"COM3", "Adapter (COM3) (COM3)", "Adapter (COM3)"},
		{"COM3", "Adapter (com3)", "Adapter (com3)"},
		{"COM3", " (COM3)", ""},
		{"COM3", "", ""},
	} {
		t.Run(test.description, func(t *testing.T) {
			if got := portDescription(test.name, test.description); got != test.want {
				t.Errorf("portDescription(%q, %q) = %q, want %q", test.name, test.description, got, test.want)
			}
		})
	}
}
