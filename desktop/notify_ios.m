//go:build ios

// Native side of notify_ios.go (kept out of the cgo preamble: an
// Objective-C class there is compiled twice and fails to link).
#import <Foundation/Foundation.h>
#import <UserNotifications/UserNotifications.h>
#import <UIKit/UIKit.h>

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

void kNotifSetup(void) {
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
int kNotifAllowed(void) {
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

void kNotifShow(const char *ident, const char *title, const char *body, const char *key, const char *image) {
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

void kNotifClear(const char *ident) {
	NSString *i = [NSString stringWithUTF8String:ident];
	UNUserNotificationCenter *c = [UNUserNotificationCenter currentNotificationCenter];
	[c removeDeliveredNotificationsWithIdentifiers:@[i]];
	[c removePendingNotificationRequestsWithIdentifiers:@[i]];
}
