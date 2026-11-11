//go:build !windows

package ui

import "github.com/fcying/SerialGo/assets"

func (s *controller) setWindowIcon() error {
	return s.window.SetIcon(assets.IconPNG)
}
