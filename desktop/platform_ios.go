//go:build ios

package main

import (
	"context"
	"log"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/services/notifications"
)

// iOS: local notifications while the app runs. iOS suspends apps in the
// background shortly after they leave the screen; with "Работать в фоне"
// on, background_ios.go keeps the app alive (silent background audio), so
// the connection stays open without Apple's push service (APNs).
const platformName = "ios"

type iosNotifier struct {
	ns *notifications.NotificationService
}

func newNotifier(*Messenger) notifier { return &iosNotifier{ns: notifications.New()} }

func (n *iosNotifier) Show(x Notice) {
	opts := notifications.NotificationOptions{ID: "chat-" + x.Key, Title: x.Title, Body: x.Body,
		Data: map[string]any{"key": x.Key}}
	if x.Image != "" {
		opts.Attachments = []notifications.NotificationAttachment{{Path: x.Image}}
	}
	if err := n.ns.SendNotification(opts); err != nil && x.Image != "" {
		opts.Attachments = nil
		_ = n.ns.SendNotification(opts)
	}
}

func (n *iosNotifier) Clear(key string) { _ = n.ns.RemoveNotification("chat-" + key) }

type iosNotifications struct {
	*notifications.NotificationService
}

func (o *iosNotifications) ServiceStartup(ctx context.Context, opts application.ServiceOptions) error {
	if err := o.NotificationService.ServiceStartup(ctx, opts); err != nil {
		log.Printf("notifications unavailable: %v", err)
	}
	return nil
}

func platformServices(m *Messenger) []application.Service {
	return []application.Service{application.NewService(&iosNotifications{m.notif.(*iosNotifier).ns})}
}

func platformSetup(_ *application.App, _ *application.WebviewWindow, m *Messenger) {
	// Background mode (on unless switched off in Settings).
	go func() {
		_ = m.start()
		platformApplyBackground(m.pref(prefBackground))
	}()
	ns := m.notif.(*iosNotifier).ns
	_, _ = ns.RequestNotificationAuthorization()
	ns.OnNotificationResponse(func(r notifications.NotificationResult) {
		if key, _ := r.Response.UserInfo["key"].(string); key != "" {
			m.showChat(key)
		}
	})
}

func platformStatus(*Settings)    {}
func platformOpenSettings(string) {}

func titlebarStyle() string { return "" }

func platformSaveMedia(string, string, []byte) (string, error) { return "", errSaveUnsupported }
