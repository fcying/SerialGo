package serialport

import (
	"errors"
	"slices"

	"golang.org/x/sys/windows/registry"
)

// List returns the COM names published by installed serial drivers.
func List() ([]string, error) {
	key, err := registry.OpenKey(registry.LOCAL_MACHINE, `HARDWARE\DEVICEMAP\SERIALCOMM`, registry.QUERY_VALUE)
	if errors.Is(err, registry.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer key.Close()
	names, err := key.ReadValueNames(-1)
	if err != nil {
		return nil, err
	}
	ports := make([]string, 0, len(names))
	for _, name := range names {
		port, _, err := key.GetStringValue(name)
		if errors.Is(err, registry.ErrNotExist) {
			continue // The device was removed during enumeration.
		}
		if err != nil {
			return nil, err
		}
		ports = append(ports, port)
	}
	slices.Sort(ports)
	return slices.Compact(ports), nil
}
