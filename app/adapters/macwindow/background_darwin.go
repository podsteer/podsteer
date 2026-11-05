//go:build darwin

package macwindow

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework Cocoa -framework WebKit
#import <Cocoa/Cocoa.h>
#import <WebKit/WebKit.h>
#import <objc/message.h>

// The activity token, held while background running is on: ending it hands
// the app back to App Nap. Touched only on the main queue.
static id gActivity = nil;

static WKWebView *findWebView(NSView *view) {
    if ([view isKindOfClass:[WKWebView class]]) {
        return (WKWebView *)view;
    }
    for (NSView *child in view.subviews) {
        WKWebView *found = findWebView(child);
        if (found != nil) {
            return found;
        }
    }
    return nil;
}

// setPrivateBool calls a private WKPreferences BOOL setter if this WebKit has
// it, and does nothing if it does not. Never assumed: a WebKit that renamed
// one must cost the setting, not the launch.
static void setPrivateBool(WKPreferences *preferences, SEL selector, BOOL value) {
    if ([preferences respondsToSelector:selector]) {
        ((void (*)(id, SEL, BOOL))objc_msgSend)(preferences, selector, value);
    }
}

// setKeepRunningWhenHidden turns both mechanisms off (enabled) or hands them
// back to macOS's defaults (!enabled). All on the main queue, which is both
// where AppKit and WebKit must be touched and what serialises gActivity.
static void setKeepRunningWhenHidden(int enabledFlag) {
    BOOL enabled = enabledFlag != 0;
    dispatch_async(dispatch_get_main_queue(), ^{
        // 1. App Nap, for this process: timers and network in Go. The variant
        //    that still lets the machine sleep on its own schedule.
        if (enabled && gActivity == nil) {
            gActivity = [[[NSProcessInfo processInfo]
                beginActivityWithOptions:NSActivityUserInitiatedAllowingIdleSystemSleep
                                  reason:@"Refreshing cluster state while the window is hidden"] retain];
        } else if (!enabled && gActivity != nil) {
            [[NSProcessInfo processInfo] endActivity:gActivity];
            [gActivity release];
            gActivity = nil;
        }

        // 2. WebKit, for the web content process, where the refresh timer
        //    actually lives. App Nap does not reach it; WebKit throttles and
        //    suspends a hidden page by its own rules.
        for (NSWindow *window in [[NSApplication sharedApplication] windows]) {
            WKWebView *webView = findWebView(window.contentView);
            if (webView == nil) {
                continue;
            }
            // Shared with the live view, not a copy: Wails changes it the
            // same way after creation for the inspector.
            WKPreferences *preferences = webView.configuration.preferences;
            @try {
#if __MAC_OS_X_VERSION_MAX_ALLOWED >= 140000
                if (@available(macOS 14.0, *)) {
                    preferences.inactiveSchedulingPolicy =
                        enabled ? WKInactiveSchedulingPolicyNone : WKInactiveSchedulingPolicySuspend;
                }
#endif
                // Both default to YES; "off" restores that.
                setPrivateBool(preferences, NSSelectorFromString(@"_setHiddenPageDOMTimerThrottlingEnabled:"), !enabled);
                setPrivateBool(preferences, NSSelectorFromString(@"_setPageVisibilityBasedProcessSuppressionEnabled:"), !enabled);
            } @catch (NSException *exception) {
                NSLog(@"[PodSteer] could not change background running: %@", exception.reason);
            }
        }
    });
}
*/
import "C"

// SetKeepRunningWhenHidden decides whether macOS may throttle or suspend
// PodSteer while its window is hidden, occluded, minimised or behind a locked
// screen. It is the Settings option "Keep refreshing while the window is
// hidden", on by default.
//
// The refresh tick is a JavaScript timer, and two separate mechanisms pause
// it when nobody is looking. App Nap throttles this process. WebKit throttles
// a hidden page's DOM timers and suspends its web content process (on macOS
// 14+, WKPreferences.inactiveSchedulingPolicy defaults to suspend). The
// result was gaps in the sampled metrics, and once a poll left the screen
// showing hours-old numbers until somebody refreshed by hand. Enabled turns
// both off; disabled hands them back to macOS, trading the gaps for battery.
//
// It NEVER keeps the machine awake. A closed lid or a system sleep still
// stops everything, and the frontend's resume path (Workspace.resume)
// restarts the poll at once on wake.
//
// Safe to call from any goroutine and any number of times; call it after
// the window exists, which for Wails means ApplicationStarted.
func SetKeepRunningWhenHidden(enabled bool) {
	// An int across the boundary: BOOL is _Bool on arm64 but signed char on
	// amd64, and no single Go conversion suits both.
	var flag C.int
	if enabled {
		flag = 1
	}
	C.setKeepRunningWhenHidden(flag)
}
