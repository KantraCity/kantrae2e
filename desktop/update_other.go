//go:build ios || (server && !android)

package main

import "errors"

// No self-update here: web mode updates with the server, iPhone through
// AltStore / SideStore.
func platformStageUpdate(string, string) error { return errors.New("no self-update on this platform") }
func platformApplyUpdate(string) error         { return errors.New("no self-update on this platform") }
func earlyUpdate()                             {}
func onQuit()                                  {}
