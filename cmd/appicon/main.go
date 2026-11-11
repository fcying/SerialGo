// Command appicon generates application icon assets from the SVG source.
package main

import (
	"bytes"
	"encoding/binary"
	"flag"
	"fmt"
	"image/png"
	"log"
	"os"
	"path/filepath"

	"github.com/akavel/rsrc/rsrc"
	"github.com/egoist/mygo/ui"
)

func main() {
	arch := flag.String("arch", "amd64", "Windows resource architecture")
	flag.Parse()
	if err := generate(*arch); err != nil {
		log.Fatal(err)
	}
}

func generate(arch string) error {
	source, err := os.ReadFile("assets/icon.svg")
	if err != nil {
		return err
	}
	svg, err := ui.ParseSVG(source)
	if err != nil {
		return err
	}
	sizes := [...]int{16, 20, 24, 32, 40, 48, 64, 128, 256}
	images := make([][]byte, 0, len(sizes))
	for _, size := range sizes {
		var image bytes.Buffer
		err = png.Encode(&image, svg.Image(size, size, nil))
		if err != nil {
			return err
		}
		images = append(images, image.Bytes())
	}
	if err = writeChanged("assets/icon.png", images[len(images)-1]); err != nil {
		return err
	}
	var ico bytes.Buffer
	header := [...]uint16{0, 1, uint16(len(sizes))}
	if err = binary.Write(&ico, binary.LittleEndian, header); err != nil {
		return err
	}
	offset := uint32(6 + 16*len(sizes))
	for i, size := range sizes {
		entry := struct {
			Width, Height, Colors, Reserved byte
			Planes, Bits                    uint16
			Length, Offset                  uint32
		}{byte(size), byte(size), 0, 0, 1, 32, uint32(len(images[i])), offset}
		if err = binary.Write(&ico, binary.LittleEndian, entry); err != nil {
			return err
		}
		offset += uint32(len(images[i]))
	}
	for _, image := range images {
		ico.Write(image)
	}
	if err = writeChanged("assets/icon.ico", ico.Bytes()); err != nil {
		return err
	}
	resource := filepath.Join("assets", fmt.Sprintf("icon_windows_%s.syso", arch))
	return rsrc.Embed(resource, arch, "", "assets/icon.ico")
}

func writeChanged(path string, data []byte) error {
	previous, err := os.ReadFile(path)
	if err == nil && bytes.Equal(previous, data) {
		return nil
	}
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return os.WriteFile(path, data, 0644)
}
