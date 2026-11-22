package main

import (
	"log"

	"github.com/fcying/SerialGo/internal"
	"github.com/fcying/SerialGo/ui"
)

const (
	appName    = "SerialGo"
	appVersion = "1.2.1"
)

func main() {
	app := internal.NewApp()
	err := ui.Run(app, appName+" v"+appVersion)
	if err != nil {
		log.Fatal(err)
	}
}
