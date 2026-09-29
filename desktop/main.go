package main

import (
	"embed"
	"log"
	"os"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// The frontend (Svelte, frontend/dist) is embedded into the binary. The same
// binary logic runs as a desktop window or, built with `-tags server`
// (task build:server), as a local web server for a browser.
//
//go:embed all:frontend/dist
var assets embed.FS

func init() {
	application.RegisterEvent[UIEvent](EventName)
}

func main() {
	hc, err := httpClientFromEnv()
	if err != nil {
		log.Fatal(err)
	}
	server := os.Getenv("KANTRA_SERVER")
	if server == "" {
		server = "https://localhost"
	}
	messenger := NewMessenger(defaultDBPath(), server, hc, webMode)

	app := application.New(application.Options{
		Name:        "Kantra",
		Description: "E2EE messenger (MLS)",
		Services: []application.Service{
			application.NewService(messenger),
		},
		Assets: application.AssetOptions{
			Handler: application.AssetFileServerFS(assets),
		},
		Mac: application.MacOptions{
			ApplicationShouldTerminateAfterLastWindowClosed: true,
		},
		// Web mode: listen on localhost only. The process holds this account's
		// MLS keys, so it must not be exposed to other people.
		Server: application.ServerOptions{Host: "localhost", Port: 8090},
	})

	app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:            "Kantra",
		Width:            1100,
		Height:           720,
		MinWidth:         380,
		MinHeight:        500,
		BackgroundColour: application.NewRGB(24, 24, 24),
		URL:              "/",
	})

	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
}
