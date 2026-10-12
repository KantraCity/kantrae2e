//go:build server && !android && !ios

package main

import "github.com/wailsapp/wails/v3/pkg/application"

const platformName = "web"

// Web mode: the browser shows the notifications (see app.svelte.ts).
func newNotifier(m *Messenger) notifier { return eventNotifier{m} }

func platformServices(*Messenger) []application.Service                      { return nil }
func platformSetup(*application.App, *application.WebviewWindow, *Messenger) {}
func platformStatus(*Settings)                                               {}
func platformApplyBackground(bool)                                           {}
func platformOpenSettings(string)                                            {}

func titlebarStyle() string { return "" }
