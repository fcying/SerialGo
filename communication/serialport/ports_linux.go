package serialport

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// List discovers device nodes without opening or reconfiguring any UART.
func List() ([]Info, error) {
	return listLinuxPorts("/dev")
}

func listLinuxPorts(directory string) ([]Info, error) {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return nil, err
	}
	var ports []Info
	for _, entry := range entries {
		name := entry.Name()
		if !linuxPortName(name) {
			continue
		}
		path := filepath.Join(directory, name)
		info, statErr := os.Stat(path)
		if errors.Is(statErr, os.ErrNotExist) {
			continue
		}
		if statErr != nil {
			return nil, statErr
		}
		if info.Mode()&os.ModeCharDevice != 0 {
			ports = append(ports, Info{Name: path})
		}
	}
	return ports, nil
}

func linuxPortName(name string) bool {
	// Keep UART families explicit; longer ttyS names must precede ttyS itself.
	for _, prefix := range [...]string{
		"ttySAC", "ttySTM", "ttyS", "ttyUSB", "ttyACM", "ttyAMA", "rfcomm", "ttyO",
		"ttyTHS", "ttyPS", "ttymxc", "ttyLP",
	} {
		digits, found := strings.CutPrefix(name, prefix)
		if !found {
			continue
		}
		if digits == "" {
			return false
		}
		for _, digit := range digits {
			if digit < '0' || digit > '9' {
				return false
			}
		}
		return true
	}
	return false
}
