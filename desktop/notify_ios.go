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
#import <Foundation/Foundation.h>
#import <UserNotifications/UserNotifications.h>
#import <UIKit/UIKit.h>
#include <stdlib.h>

extern void kantraNotificationTapped(char *key);
extern void kantraForeground(int fg);

@interface KantraNotifDelegate : NSObject <UNUserNotificationCenterDelegate>
@end

@implementation KantraNotifDelegate
// The app is open (on another chat): still show the banner and play the sound.
- (void)userNotificationCenter:(UNUserNotificationCenter *)center
       willPresentNotification:(UNNotification *)notification
         withCompletionHandler:(void (^)(UNNotificationPresentationOptions))completionHandler {
	if (@available(iOS 14.0, *)) {
		completionHandler(UNNotificationPresentationOptionBanner | UNNotificationPresentationOptionList | UNNotificationPresentationOptionSound);
	} else {
		completionHandler(UNNotificationPresentationOptionAlert | UNNotificationPresentationOptionSound);
	}
}

- (void)userNotificationCenter:(UNUserNotificationCenter *)center
didReceiveNotificationResponse:(UNNotificationResponse *)response
         withCompletionHandler:(void (^)(void))completionHandler {
	NSString *key = response.notification.request.content.userInfo[@"key"];
	if ([key isKindOfClass:[NSString class]] && key.length) {
		kantraNotificationTapped((char *)key.UTF8String);
	}
	completionHandler();
}
@end

static KantraNotifDelegate *kNotifDelegate;

static void kNotifSetup(void) {
	// Foreground or not: a message of the chat on screen is not notified
	// only while the app is actually on screen.
	NSNotificationCenter *nc = [NSNotificationCenter defaultCenter];
	[nc addObserverForName:UIApplicationDidEnterBackgroundNotification object:nil queue:nil usingBlock:^(NSNotification *n) { kantraForeground(0); }];
	[nc addObserverForName:UIApplicationDidBecomeActiveNotification object:nil queue:nil usingBlock:^(NSNotification *n) { kantraForeground(1); }];
	dispatch_async(dispatch_get_main_queue(), ^{
		UNUserNotificationCenter *c = [UNUserNotificationCenter currentNotificationCenter];
		if (!kNotifDelegate) kNotifDelegate = [KantraNotifDelegate new];
		c.delegate = kNotifDelegate;
		[c requestAuthorizationWithOptions:(UNAuthorizationOptionAlert | UNAuthorizationOptionSound | UNAuthorizationOptionBadge)
		                 completionHandler:^(BOOL granted, NSError *error) {
			if (!granted) NSLog(@"kantra: notifications not allowed (%@)", error);
		}];
	});
}

// 1 allowed, 0 denied, -1 not asked yet.
static int kNotifAllowed(void) {
	__block int out = -1;
	dispatch_semaphore_t sem = dispatch_semaphore_create(0);
	[[UNUserNotificationCenter currentNotificationCenter] getNotificationSettingsWithCompletionHandler:^(UNNotificationSettings *s) {
		switch (s.authorizationStatus) {
		case UNAuthorizationStatusDenied: out = 0; break;
		case UNAuthorizationStatusNotDetermined: out = -1; break;
		default: out = 1;
		}
		dispatch_semaphore_signal(sem);
	}];
	dispatch_semaphore_wait(sem, dispatch_time(DISPATCH_TIME_NOW, 2 * NSEC_PER_SEC));
	return out;
}

static void kNotifShow(const char *ident, const char *title, const char *body, const char *key, const char *image) {
	UNMutableNotificationContent *n = [UNMutableNotificationContent new];
	n.title = [NSString stringWithUTF8String:title];
	n.body = [NSString stringWithUTF8String:body];
	n.sound = [UNNotificationSound defaultSound];
	n.threadIdentifier = [NSString stringWithUTF8String:ident];
	n.userInfo = @{@"key": [NSString stringWithUTF8String:key]};
	if (image && image[0]) {
		NSURL *url = [NSURL fileURLWithPath:[NSString stringWithUTF8String:image]];
		NSError *err = nil;
		UNNotificationAttachment *a = [UNNotificationAttachment attachmentWithIdentifier:@"preview" URL:url options:nil error:&err];
		if (a) n.attachments = @[a];
	}
	UNNotificationRequest *r = [UNNotificationRequest requestWithIdentifier:[NSString stringWithUTF8String:ident] content:n trigger:nil];
	[[UNUserNotificationCenter currentNotificationCenter] addNotificationRequest:r withCompletionHandler:^(NSError *error) {
		if (error) NSLog(@"kantra: notification failed: %@", error);
	}];
}

static void kNotifClear(const char *ident) {
	NSString *i = [NSString stringWithUTF8String:ident];
	UNUserNotificationCenter *c = [UNUserNotificationCenter currentNotificationCenter];
	[c removeDeliveredNotificationsWithIdentifiers:@[i]];
	[c removePendingNotificationRequestsWithIdentifiers:@[i]];
}
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
