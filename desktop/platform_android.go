//go:build android

package main

import (
	"path/filepath"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// On Android the server is reached through USB: `adb reverse tcp:8080 tcp:8080`
// makes the phone's localhost:8080 the PC's 127.0.0.1:8080 (dev Caddy, plain
// HTTP over the cable; messages are MLS-encrypted end-to-end anyway).
const defaultServer = "http://localhost:8080"

const deviceKind = "Android"

// dbPath is the app's private files directory; it is only known once the
// activity runs, hence a function.
func dbPath() string {
	return filepath.Join(application.Android.StoragePath(), "kantra.db")
}
