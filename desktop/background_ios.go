//go:build ios

package main

// Keeping Kantra alive in the background on iPhone without Apple's push
// service: the app declares the "audio" background mode and plays silence
// (mixed with other apps' sound, so music is not interrupted) while it is
// in the background. iOS does not suspend an app that plays audio, so the
// realtime connection stays open and messages are notified at once, like
// on Android. Costs some battery; off in Settings → Работать в фоне.
// Swiping the app away still ends it (until it is opened again).

/*
#cgo CFLAGS: -x objective-c -fobjc-arc
#cgo LDFLAGS: -framework AVFoundation -framework UIKit
#import <AVFoundation/AVFoundation.h>
#import <UIKit/UIKit.h>

static AVAudioPlayer *kPlayer;
static BOOL kEnabled = NO;
static BOOL kInBackground = NO;
static BOOL kObserving = NO;
static NSTimer *kWatchdog;

// One second of silence as an in-memory WAV (8 kHz, 16-bit, mono).
static NSData *kSilence(void) {
	const uint32_t rate = 8000, samples = 8000, dataLen = samples * 2;
	NSMutableData *d = [NSMutableData dataWithLength:44 + dataLen];
	uint8_t *p = d.mutableBytes;
	memcpy(p, "RIFF", 4); uint32_t riff = 36 + dataLen; memcpy(p + 4, &riff, 4);
	memcpy(p + 8, "WAVEfmt ", 8);
	uint32_t fmtLen = 16; memcpy(p + 16, &fmtLen, 4);
	uint16_t pcm = 1, ch = 1, bits = 16, align = 2; uint32_t byteRate = rate * 2;
	memcpy(p + 20, &pcm, 2); memcpy(p + 22, &ch, 2); memcpy(p + 24, &rate, 4);
	memcpy(p + 28, &byteRate, 4); memcpy(p + 32, &align, 2); memcpy(p + 34, &bits, 2);
	memcpy(p + 36, "data", 4); memcpy(p + 40, &dataLen, 4);
	return d;
}

static void kPlay(void) {
	if (!kEnabled || !kInBackground) return;
	AVAudioSession *s = [AVAudioSession sharedInstance];
	// A call or a recording owns the session (PlayAndRecord): leave its
	// category alone, it keeps the app alive by itself.
	if (![s.category isEqualToString:AVAudioSessionCategoryPlayAndRecord]) {
		[s setCategory:AVAudioSessionCategoryPlayback withOptions:AVAudioSessionCategoryOptionMixWithOthers error:nil];
	}
	[s setActive:YES error:nil];
	if (!kPlayer) {
		kPlayer = [[AVAudioPlayer alloc] initWithData:kSilence() error:nil];
		kPlayer.numberOfLoops = -1;
		[kPlayer prepareToPlay];
	}
	if (!kPlayer.playing) [kPlayer play];
}

static void kStop(void) {
	[kPlayer stop];
	kPlayer = nil;
	[kWatchdog invalidate];
	kWatchdog = nil;
	AVAudioSession *s = [AVAudioSession sharedInstance];
	if (![s.category isEqualToString:AVAudioSessionCategoryPlayAndRecord]) {
		[s setActive:NO withOptions:AVAudioSessionSetActiveOptionNotifyOthersOnDeactivation error:nil];
	}
}

static void kEnterBackground(void) {
	kInBackground = YES;
	kPlay();
	if (kEnabled && !kWatchdog) {
		// Restart the player if something stopped it (another app's audio
		// session, a phone call that did not report its end).
		kWatchdog = [NSTimer scheduledTimerWithTimeInterval:30 repeats:YES block:^(NSTimer *t) { kPlay(); }];
	}
}

static void kObserve(void) {
	if (kObserving) return;
	kObserving = YES;
	NSNotificationCenter *nc = [NSNotificationCenter defaultCenter];
	NSOperationQueue *q = [NSOperationQueue mainQueue];
	[nc addObserverForName:UIApplicationDidEnterBackgroundNotification object:nil queue:q usingBlock:^(NSNotification *n) {
		kEnterBackground();
	}];
	[nc addObserverForName:UIApplicationWillEnterForegroundNotification object:nil queue:q usingBlock:^(NSNotification *n) {
		kInBackground = NO;
		kStop(); // on screen the app runs anyway
	}];
	[nc addObserverForName:AVAudioSessionInterruptionNotification object:nil queue:q usingBlock:^(NSNotification *n) {
		NSNumber *type = n.userInfo[AVAudioSessionInterruptionTypeKey];
		if (type.unsignedIntegerValue == AVAudioSessionInterruptionTypeEnded) kPlay();
	}];
	[nc addObserverForName:AVAudioSessionMediaServicesWereResetNotification object:nil queue:q usingBlock:^(NSNotification *n) {
		kPlayer = nil;
		kPlay();
	}];
}

static void kantraKeepAlive(int on) {
	dispatch_async(dispatch_get_main_queue(), ^{
		kObserve();
		kEnabled = on ? YES : NO;
		if (kEnabled) {
			if ([UIApplication sharedApplication].applicationState == UIApplicationStateBackground) kEnterBackground();
		} else {
			kStop();
		}
	});
}
*/
import "C"

func platformApplyBackground(on bool) {
	v := C.int(0)
	if on {
		v = 1
	}
	C.kantraKeepAlive(v)
}
