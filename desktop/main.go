package main

import (
	"embed"
	"log"
	"os"
	"runtime"
	"sync"
	"sync/atomic"

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

var (
	messengerOnce sync.Once
	theMessenger  *Messenger
	mainStarted   atomic.Bool
)

// sharedMessenger is the process-wide client service. On Android the
// background service may create it before (or without) the UI.
func sharedMessenger() *Messenger {
	messengerOnce.Do(func() {
		hc, err := httpClientFromEnv()
		if err != nil {
			log.Fatal(err)
		}
		server := os.Getenv("KANTRA_SERVER")
		if server == "" {
			server = buildServer
		}
		if server == "" {
			server = defaultServer
		}
		theMessenger = NewMessenger(dbPath, server, hc, webMode)
	})
	return theMessenger
}

func main() {
	// Android re-runs main when the activity is recreated in a process
	// that is still alive (the background connection keeps it running):
	// the app and its client already exist.
	if !mainStarted.CompareAndSwap(false, true) {
		return
	}
	messenger := sharedMessenger()

	// Desktop: one running app per user. Starting it again (shortcut,
	// autostart) brings the existing window to the front instead of a
	// second copy fighting over the same database.
	var window *application.WebviewWindow
	var single *application.SingleInstanceOptions
	if !webMode && (runtime.GOOS == "windows" || runtime.GOOS == "darwin" || runtime.GOOS == "linux") {
		single = &application.SingleInstanceOptions{
			UniqueID: "city.kantra.desktop",
			OnSecondInstanceLaunch: func(application.SecondInstanceData) {
				application.InvokeAsync(func() {
					if window != nil {
						window.Show()
						window.UnMinimise()
						window.Focus()
					}
				})
			},
		}
	}

	app := application.New(application.Options{
		SingleInstance: single,
		Name:           "Kantra",
		Description:    "E2EE messenger (MLS)",
		Services: append([]application.Service{
			application.NewService(messenger),
		}, platformServices(messenger)...),
		Assets: application.AssetOptions{
			Handler: application.AssetFileServerFS(assets),
			// Decrypted media for <img>/<video> (media.go).
			Middleware: mediaMiddleware(messenger),
		},
		Mac: application.MacOptions{
			// Keep running (tray) when the window is closed.
			ApplicationShouldTerminateAfterLastWindowClosed: false,
		},
		// Web mode: listen on localhost only. The process holds this account's
		// MLS keys, so it must not be exposed to other people.
		Server: application.ServerOptions{Host: "localhost", Port: 8090},
	})

	window = app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:            "Kantra",
		Width:            1100,
		Height:           720,
		MinWidth:         380,
		MinHeight:        500,
		BackgroundColour: application.NewRGB(23, 33, 43),
		URL:              "/",
		// Drag & drop of files from the OS into a chat (drop.go).
		EnableFileDrop: true,
		// The interface does not zoom (Ctrl + wheel / Ctrl +/- in WebView2).
		ZoomControlEnabled: false,
		// Our own title bar (TitleBar.svelte): window buttons drawn by the
		// app on Windows, the native traffic lights over it on macOS.
		Frameless: titlebarStyle() == "custom",
		Mac: application.MacWindow{
			TitleBar: application.MacTitleBarHidden,
			Backdrop: application.MacBackdropNormal,
		},
		// Voice and video messages (getUserMedia).
		Permissions: map[application.PermissionType]application.Permission{
			application.PermissionMicrophone: application.PermissionAllow,
			application.PermissionCamera:     application.PermissionAllow,
		},
	})
	platformSetup(app, window, messenger)

	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
}
