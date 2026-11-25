package main

import (
	"log"

	"github.com/fcying/SerialGo/internal"
	"github.com/fcying/SerialGo/ui"
)

const (
	appName    = "SerialGo"
	appVersion = "1.3.0"
)

func main() {
	app := internal.NewApp()
	err := ui.Run(app, appName+" v"+appVersion)
	if err != nil {
		log.Fatal(err)
	}
}
