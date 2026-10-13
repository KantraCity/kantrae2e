//go:build ios

package main

import (
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// iOS: local notifications while the app runs. iOS suspends apps in the
// background shortly after they leave the screen; with "Работать в фоне"
// on, background_ios.go keeps the app alive (silent background audio), so
// the connection stays open without Apple's push service (APNs).
const platformName = "ios"

// Notifications: notify_ios.go.

func platformServices(*Messenger) []application.Service { return nil }

func platformSetup(_ *application.App, _ *application.WebviewWindow, m *Messenger) {
	// Background mode (on unless switched off in Settings).
	go func() {
		_ = m.start()
		platformApplyBackground(m.pref(prefBackground))
	}()
	notifySetup(m.showChat)
}

// platformStatus: notifications switched off for Kantra in iOS Settings.
func platformStatus(s *Settings) {
	s.NotificationsAllowed = notifyAllowed() != 0
}

// platformOpenSettings opens Kantra's page in the iPhone Settings app.
func platformOpenSettings(string) { application.IOS.OpenURL("app-settings:") }

func titlebarStyle() string { return "" }

func platformOpenURL(u string) error {
	application.IOS.OpenURL(u)
	return nil
}

// platformSaveMedia hands the file to the share sheet, which offers
// "Сохранить изображение/видео" (Photos) and "Сохранить в Файлы".
func platformSaveMedia(name, _ string, data []byte) (string, error) {
	dir := filepath.Join(os.TempDir(), "share")
	_ = os.RemoveAll(dir) // the previous shared file
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return "", err
	}
	b, _ := json.Marshal(map[string]string{"url": (&url.URL{Scheme: "file", Path: path}).String()})
	application.IOS.Share(string(b))
	return "", errSaveShared
}
