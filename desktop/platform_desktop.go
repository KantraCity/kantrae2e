//go:build !android && !ios && !server

package main

import (
	"context"
	_ "embed"
	"log"
	"runtime"
	"strconv"
	"strings"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
	"github.com/wailsapp/wails/v3/pkg/services/notifications"
)

const platformName = "desktop"

//go:embed build/trayicon.png
var trayIcon []byte

// desktopNotifier shows native notifications (D-Bus on Linux, toasts on
// Windows, Notification Center on macOS), one per chat.
type desktopNotifier struct {
	ns *notifications.NotificationService
}

func newNotifier(*Messenger) notifier { return &desktopNotifier{ns: notifications.New()} }

func noticeID(key string) string { return "chat-" + key }

func (d *desktopNotifier) Show(n Notice) {
	body := n.Body
	if n.Count > 1 {
		body += "  (" + strconv.Itoa(n.Count) + ")"
	}
	opts := notifications.NotificationOptions{
		ID: noticeID(n.Key), Title: n.Title, Body: body,
		Data:     map[string]any{"key": n.Key},
		ThreadID: n.Key,
	}
	if n.Image != "" {
		// Windows: a large picture under the text, like Telegram Desktop.
		opts.Attachments = []notifications.NotificationAttachment{{Path: n.Image, Type: "hero"}}
	}
	if err := d.ns.SendNotification(opts); err != nil && n.Image != "" {
		opts.Attachments = nil // picture refused: the text alone
		_ = d.ns.SendNotification(opts)
	}
}

func (d *desktopNotifier) Clear(key string) { _ = d.ns.RemoveNotification(noticeID(key)) }

// optionalNotifications: no notification daemon (e.g. a bare Linux session)
// must not stop the app from starting.
type optionalNotifications struct {
	*notifications.NotificationService
}

func (o *optionalNotifications) ServiceStartup(ctx context.Context, opts application.ServiceOptions) error {
	if err := o.NotificationService.ServiceStartup(ctx, opts); err != nil {
		log.Printf("notifications unavailable: %v", err)
	}
	return nil
}

func platformServices(m *Messenger) []application.Service {
	return []application.Service{application.NewService(&optionalNotifications{m.notif.(*desktopNotifier).ns})}
}

// platformSetup: like Telegram Desktop, closing the window keeps Kantra
// running in the tray (setting "background"), so notifications keep coming.
func platformSetup(app *application.App, w *application.WebviewWindow, m *Messenger) {
	// The default application menu (macOS) has View → Zoom In/Out and
	// Reload with shortcuts: the interface must not zoom or reload.
	// (Windows and Linux show no menu: there it would add a menu bar.)
	if runtime.GOOS == "darwin" {
		appMenu := application.NewMenu()
		appMenu.AddRole(application.AppMenu)
		appMenu.AddRole(application.EditMenu) // copy / paste shortcuts
		appMenu.AddRole(application.WindowMenu)
		app.Menu.SetApplicationMenu(appMenu)
	}
	show := func() {
		w.Show()
		w.Focus()
	}
	ns := m.notif.(*desktopNotifier).ns
	_, _ = ns.RequestNotificationAuthorization()
	ns.OnNotificationResponse(func(r notifications.NotificationResult) {
		if r.Error != nil {
			return
		}
		key, _ := r.Response.UserInfo["key"].(string)
		if key == "" {
			key = strings.TrimPrefix(r.Response.ID, "chat-")
		}
		application.InvokeAsync(show)
		if key != "" {
			m.showChat(key)
		}
	})

	// Native file drops (macOS, Linux). On Windows the page receives the
	// drop itself (WebView2 DOM events), so it is not forwarded twice.
	if runtime.GOOS != "windows" {
		w.OnWindowEvent(events.Common.WindowFilesDropped, func(e *application.WindowEvent) {
			m.filesDropped(e.Context().DroppedFiles())
		})
	}
	w.OnWindowEvent(events.Common.WindowFocus, func(*application.WindowEvent) { m.SetForeground(true) })
	w.OnWindowEvent(events.Common.WindowLostFocus, func(*application.WindowEvent) { m.SetForeground(false) })
	w.RegisterHook(events.Common.WindowClosing, func(e *application.WindowEvent) {
		if m.pref(prefBackground) {
			w.Hide()
			m.SetForeground(false)
			e.Cancel()
		}
	})

	menu := app.NewMenu()
	menu.Add("Открыть Kantra").OnClick(func(*application.Context) { show() })
	menu.AddSeparator()
	menu.Add("Выход").OnClick(func(*application.Context) { app.Quit() })
	tray := app.SystemTray.New()
	tray.SetIcon(trayIcon)
	tray.SetTooltip("Kantra")
	tray.SetMenu(menu)
	tray.OnClick(show)
}

func platformStatus(*Settings)     {}
func platformApplyBackground(bool) {}
func platformOpenSettings(string)  {}

// titlebarStyle: see State.Titlebar. Linux keeps the system frame (window
// managers differ too much for a frameless window to behave everywhere).
func titlebarStyle() string {
	switch runtime.GOOS {
	case "windows":
		return "custom"
	case "darwin":
		return "mac"
	}
	return ""
}

// platformSaveMedia saves into Downloads/Kantra, like Telegram Desktop.
func platformSaveMedia(name, _ string, data []byte) (string, error) {
	return saveToDownloads(name, data)
}
