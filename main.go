package main

import (
	"log"

	"github.com/fcying/SerialGo/ui"
)

const (
	appName    = "SerialGo"
	appVersion = "0.1.0"
)

func main() {
	err := ui.Run(appName + " v" + appVersion)
	if err != nil {
		log.Fatal(err)
	}
}
