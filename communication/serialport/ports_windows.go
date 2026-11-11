package serialport

import (
	"errors"
	"slices"
	"strings"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// List returns the COM names published by installed serial drivers, with
// best-effort descriptions from present devices. Metadata errors are ignored.
func List() ([]Info, error) {
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
	ports := make([]Info, 0, len(names))
	for _, name := range names {
		port, _, err := key.GetStringValue(name)
		if errors.Is(err, registry.ErrNotExist) {
			continue // The device was removed during enumeration.
		}
		if err != nil {
			return nil, err
		}
		ports = append(ports, Info{Name: port})
	}
	slices.SortFunc(ports, func(a, b Info) int { return strings.Compare(a.Name, b.Name) })
	ports = slices.CompactFunc(ports, func(a, b Info) bool { return a.Name == b.Name })
	if len(ports) != 0 {
		addPortDescriptions(ports)
	}
	return ports, nil
}

// SERIALCOMM remains authoritative: SetupAPI only enriches names already listed,
// including serial devices outside the Ports setup class (such as modems).
func addPortDescriptions(ports []Info) {
	devices, err := windows.SetupDiGetClassDevsEx(nil, "", 0, windows.DIGCF_PRESENT|windows.DIGCF_ALLCLASSES, 0, "")
	if err != nil {
		return
	}
	defer devices.Close()
	for i := 0; ; i++ {
		device, err := devices.EnumDeviceInfo(i)
		if err != nil {
			return
		}
		handle, err := devices.OpenDevRegKey(device, windows.DICS_FLAG_GLOBAL, 0, windows.DIREG_DEV, registry.QUERY_VALUE)
		if err != nil {
			continue
		}
		key := registry.Key(handle)
		name, _, err := key.GetStringValue("PortName")
		key.Close()
		if err != nil {
			continue
		}
		index, found := slices.BinarySearchFunc(ports, name, func(port Info, name string) int {
			return strings.Compare(port.Name, name)
		})
		if !found {
			continue
		}
		for _, property := range [...]windows.SPDRP{windows.SPDRP_FRIENDLYNAME, windows.SPDRP_DEVICEDESC} {
			value, err := devices.DeviceRegistryProperty(device, property)
			if err != nil {
				continue
			}
			description, ok := value.(string)
			if !ok {
				continue
			}
			description = portDescription(name, description)
			if description != "" {
				ports[index].Description = description
				break
			}
		}
	}
}

func portDescription(name, description string) string {
	return strings.TrimSuffix(description, " ("+name+")")
}
