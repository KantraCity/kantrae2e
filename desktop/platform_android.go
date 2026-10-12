//go:build android

package main

import (
	"path/filepath"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
)

const platformName = "android"

// On Android the server is reached through USB: `adb reverse tcp:8080 tcp:8080`
// makes the phone's localhost:8080 the PC's 127.0.0.1:8080 (dev Caddy, plain
// HTTP over the cable; messages are MLS-encrypted end-to-end anyway).
const defaultServer = "http://localhost:8080"

const deviceKind = "Android"

// keepAlive: the server sends a heartbeat after 4 minutes of silence — the
// only regular radio wake-up while idle. Halved automatically (down to a
// minute) if the mobile network drops idle connections sooner.
const keepAlive = 4 * time.Minute

// dbPath is in the app's private files directory, known once the Java side
// attached (activity or background service).
func dbPath() string {
	dir, _ := storageDir.Load().(string)
	if dir == "" {
		dir = application.Android.StoragePath()
	}
	return filepath.Join(dir, "kantra.db")
}

func newNotifier(*Messenger) notifier { return androidNotifier{} }

func platformServices(*Messenger) []application.Service                      { return nil }
func platformSetup(*application.App, *application.WebviewWindow, *Messenger) {}

func titlebarStyle() string { return "" }
