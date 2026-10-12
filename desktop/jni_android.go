//go:build android

package main

/*
#include <stdlib.h>
#include "jni_android.h"
*/
import "C"

import (
	"encoding/json"
	"log"
	"sync/atomic"
	"unsafe"
)

// JNI side of com.wails.app.KantraNative: the Android shell calls these, and
// Go calls back into KantraNative's static methods (notifications, the
// background service, system settings).

var storageDir atomic.Value // string: the app's files directory

func goString(env *C.JNIEnv, s C.jstring) string {
	cs := C.kantra_jstring(env, s)
	if cs == nil {
		return ""
	}
	defer C.free(unsafe.Pointer(cs))
	return C.GoString(cs)
}

func javaCall(method, arg string) {
	cm, ca := C.CString(method), C.CString(arg)
	defer C.free(unsafe.Pointer(cm))
	defer C.free(unsafe.Pointer(ca))
	C.kantra_call(cm, ca)
}

func javaCallString(method, arg string) string {
	cm, ca := C.CString(method), C.CString(arg)
	defer C.free(unsafe.Pointer(cm))
	defer C.free(unsafe.Pointer(ca))
	out := C.kantra_call_str(cm, ca)
	if out == nil {
		return ""
	}
	defer C.free(unsafe.Pointer(out))
	return C.GoString(out)
}

//export Java_com_wails_app_KantraNative_nativeAttach
func Java_com_wails_app_KantraNative_nativeAttach(env *C.JNIEnv, cls C.jclass, dir C.jstring) {
	C.kantra_store(env, cls)
	storageDir.Store(goString(env, dir))
}

// nativeStartBackground: the foreground service (re)started; open the
// profile and connect even if the UI never starts.
//
//export Java_com_wails_app_KantraNative_nativeStartBackground
func Java_com_wails_app_KantraNative_nativeStartBackground(env *C.JNIEnv, cls C.jclass) {
	go func() {
		if err := sharedMessenger().start(); err != nil {
			log.Printf("background start: %v", err)
		}
	}()
}

//export Java_com_wails_app_KantraNative_nativeOpenChat
func Java_com_wails_app_KantraNative_nativeOpenChat(env *C.JNIEnv, cls C.jclass, chat C.jstring) {
	id := goString(env, chat)
	go sharedMessenger().showChat(id)
}

//export Java_com_wails_app_KantraNative_nativeSetForeground
func Java_com_wails_app_KantraNative_nativeSetForeground(env *C.JNIEnv, cls C.jclass, fg C.jboolean) {
	v := fg != 0
	go sharedMessenger().SetForeground(v)
}

//export Java_com_wails_app_KantraNative_nativeNetworkAvailable
func Java_com_wails_app_KantraNative_nativeNetworkAvailable(env *C.JNIEnv, cls C.jclass) {
	go sharedMessenger().reconnect()
}

// androidNotifier posts notifications from Go through KantraNative.
type androidNotifier struct{}

func (androidNotifier) Show(n Notice) {
	// chatId is the notice key ("<account>/<chat>"), handed back on tap.
	b, _ := json.Marshal(map[string]any{"chatId": n.Key, "title": n.Title, "body": n.Body, "count": n.Count, "image": n.Image})
	javaCall("notify", string(b))
}

func (androidNotifier) Clear(key string) { javaCall("cancel", key) }

func platformStatus(s *Settings) {
	var st struct {
		NotificationsAllowed bool  `json:"notificationsAllowed"`
		BatteryUnrestricted  bool  `json:"batteryUnrestricted"`
		Background           *bool `json:"background"`
	}
	if json.Unmarshal([]byte(javaCallString("status", "")), &st) == nil {
		s.NotificationsAllowed = st.NotificationsAllowed
		s.BatteryUnrestricted = st.BatteryUnrestricted
		if st.Background != nil {
			s.Background = *st.Background // the service's own flag is authoritative
		}
	}
}

func platformApplyBackground(on bool) { javaCall("setBackground", boolPref(on)) }

func platformOpenSettings(what string) { javaCall("openSettings", what) }
