package main

/*
#cgo CFLAGS: -x objective-c -fobjc-arc
#cgo LDFLAGS: -framework Foundation
#import <Foundation/Foundation.h>
#include <stdlib.h>

static void *beginActivity(const char *reason) {
	id<NSObject> a = [[NSProcessInfo processInfo]
		beginActivityWithOptions:NSActivityUserInitiatedAllowingIdleSystemSleep | NSActivityLatencyCritical
		reason:[NSString stringWithUTF8String:reason]];
	return (__bridge_retained void *)a;
}

static void endActivity(void *p) {
	id<NSObject> a = (__bridge_transfer id<NSObject>)p;
	[[NSProcessInfo processInfo] endActivity:a];
}
*/
import "C"

import "unsafe"

// beginActivity tells macOS that the app is doing work the user asked for,
// until the returned function is called. Without it, App Nap throttles the
// app once its window is hidden or the display sleeps (as in an unattended
// run): its threads drop to background priority, and the built-in agent's
// forward passes, which normally take 45–150 ms, take up to seconds.
func beginActivity(reason string) (end func()) {
	cs := C.CString(reason)
	defer C.free(unsafe.Pointer(cs))
	a := C.beginActivity(cs)
	return func() { C.endActivity(a) }
}
