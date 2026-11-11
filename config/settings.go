package config

import (
	"errors"
	"os"
	"path/filepath"

	"github.com/fcying/SerialGo/communication/serialport"

	"github.com/BurntSushi/toml"
)

const PresetCount = 99

type Preset struct {
	Label, Data, Interval string
	Hex, CRLF, Enabled    bool
}

type Settings struct {
	Port                                    serialport.Settings
	SendText, SendHexText                   string
	Encoding, Checksum, CheckFrom, CheckEnd string
	Interval, PacketGap, FileDelay          string
	SendHex, CRLF, ReceiveHex, ShowSent     bool
	Timestamp, Packet, ShowPresets          bool
	Presets                                 [PresetCount]Preset
}

func defaults() Settings {
	s := Settings{
		Port:     serialport.Settings{Baud: "115200", DataBits: "8", Parity: "None", StopBits: "1", DTR: true},
		Encoding: "UTF-8", Checksum: "None", CheckFrom: "1", CheckEnd: "0", Interval: "1000", PacketGap: "20", FileDelay: "1",
		CRLF: true, Packet: true,
	}
	for i := range s.Presets {
		s.Presets[i].Interval = "1000"
	}
	return s
}

func Load() (Settings, string, error) {
	s := defaults()
	exe, err := os.Executable()
	if err != nil {
		return s, "", err
	}
	exe, err = filepath.EvalSymlinks(exe)
	if err != nil {
		return s, "", err
	}
	path := filepath.Join(filepath.Dir(exe), "config.toml")
	_, err = toml.DecodeFile(path, &s)
	if errors.Is(err, os.ErrNotExist) {
		return s, path, nil
	}
	if err != nil {
		// Do not overwrite a configuration we could not read.
		return defaults(), "", err
	}
	return s, path, nil
}

func Save(path string, s Settings) error {
	if path == "" {
		return nil
	}
	dir := filepath.Dir(path)
	file, err := os.CreateTemp(dir, ".settings-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	writeErr := toml.NewEncoder(file).Encode(s)
	closeErr := file.Close()
	if err := errors.Join(writeErr, closeErr); err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}
