//go:build ios

package main

// Local notifications on iPhone through UNUserNotificationCenter. (Wails'
// notification service is only a stub on iOS: nothing was ever shown.)
// The app posts them itself while it runs; with "Работать в фоне" it keeps
// running in the background (background_ios.go), so no Apple push service
// is involved. A tap opens the chat; a banner is also shown while the app
// is open on another chat.

/*
#cgo CFLAGS: -x objective-c -fobjc-arc
#cgo LDFLAGS: -framework UserNotifications -framework Foundation -framework UIKit
#include <stdlib.h>

void kNotifSetup(void);
int kNotifAllowed(void);
void kNotifShow(const char *ident, const char *title, const char *body, const char *key, const char *image);
void kNotifClear(const char *ident);
*/
import "C"

import (
	"io"
	"os"
	"path/filepath"
	"sync/atomic"
	"unsafe"
)

// tapped receives the chat key of a tapped notification.
var tapped atomic.Pointer[func(string)]

//export kantraNotificationTapped
func kantraNotificationTapped(key *C.char) {
	k := C.GoString(key)
	if f := tapped.Load(); f != nil {
		go (*f)(k)
	}
}

//export kantraForeground
func kantraForeground(fg C.int) {
	go sharedMessenger().SetForeground(fg != 0)
}

func cstr(s string) (*C.char, func()) {
	c := C.CString(s)
	return c, func() { C.free(unsafe.Pointer(c)) }
}

type iosNotifier struct{}

func newNotifier(*Messenger) notifier { return iosNotifier{} }

// notifySetup asks for permission (once) and routes taps to onTap.
func notifySetup(onTap func(key string)) {
	tapped.Store(&onTap)
	C.kNotifSetup()
}

// notifyAllowed: 1 allowed, 0 denied in iOS Settings, -1 not asked yet.
func notifyAllowed() int { return int(C.kNotifAllowed()) }

func (iosNotifier) Show(x Notice) {
	ident, f1 := cstr("chat-" + x.Key)
	defer f1()
	title, f2 := cstr(x.Title)
	defer f2()
	body, f3 := cstr(x.Body)
	defer f3()
	key, f4 := cstr(x.Key)
	defer f4()
	// iOS moves the attachment into its own store: hand it a copy.
	img := ""
	if x.Image != "" {
		img = copyForAttachment(x.Image)
	}
	image, f5 := cstr(img)
	defer f5()
	C.kNotifShow(ident, title, body, key, image)
}

func (iosNotifier) Clear(key string) {
	ident, free := cstr("chat-" + key)
	defer free()
	C.kNotifClear(ident)
}

func copyForAttachment(src string) string {
	in, err := os.Open(src)
	if err != nil {
		return ""
	}
	defer in.Close()
	dir := filepath.Join(os.TempDir(), "notif")
	_ = os.MkdirAll(dir, 0o700)
	ext := filepath.Ext(src)
	if ext == "" {
		ext = ".jpg"
	}
	out, err := os.CreateTemp(dir, "preview-*"+ext)
	if err != nil {
		return ""
	}
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		os.Remove(out.Name())
		return ""
	}
	return out.Name()
}
