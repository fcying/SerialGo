package serialport

import serial "github.com/albenik/go-serial/v2"

// List returns the UART devices enumerated by the serial library.
func List() ([]string, error) { return serial.GetPortsList() }
