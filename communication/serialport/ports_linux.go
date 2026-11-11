package serialport

import serial "github.com/albenik/go-serial/v2"

// List returns the UART devices enumerated by the serial library.
func List() ([]Info, error) {
	names, err := serial.GetPortsList()
	if err != nil || names == nil {
		return nil, err
	}
	ports := make([]Info, len(names))
	for i, name := range names {
		ports[i].Name = name
	}
	return ports, nil
}
