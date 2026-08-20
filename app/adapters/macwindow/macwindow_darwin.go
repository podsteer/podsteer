//go:build darwin

// Package macwindow adjusts native macOS window chrome that Wails has no
// public API for.
//
// The one thing it does today — nudging the traffic lights — reaches past
// Wails into AppKit directly via NSWindow.standardWindowButton, which is
// public AppKit API, not a private one. Wails itself has no equivalent: see
// https://github.com/wailsapp/wails/issues/4227, open at the time this was
// written, where a maintainer confirms there is currently no supported way
// to reposition them.
package macwindow

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework Cocoa
#import <Cocoa/Cocoa.h>
#import <math.h>

// Each button's natural, AppKit-assigned Y position, captured once on first
// touch and never overwritten again.
//
// This is what makes every later reapplication idempotent. Without it,
// re-applying "add deltaY to whatever the frame says right now" more than
// once — which this file deliberately does, repeatedly, to survive AppKit's
// own layout passes — compounds: N reapplications drift the button N times
// further than intended, which is exactly what pushed every button off the
// top of the window the first time this was written. Anchoring every
// application to the one original position instead means any number of
// reapplications all land on the same, correct, final spot.
static CGFloat gCloseBaseY = NAN;
static CGFloat gMiniaturizeBaseY = NAN;
static CGFloat gZoomBaseY = NAN;

// applyNudge moves one button to baseY + deltaY, capturing the button's
// current position into *baseY first if that has not happened yet.
static void applyNudge(NSButton *button, CGFloat *baseY, CGFloat deltaY) {
    if (button == nil) {
        return;
    }
    if (isnan(*baseY)) {
        *baseY = button.frame.origin.y;
    }

    NSRect frame = button.frame;
    frame.origin.y = *baseY + deltaY;
    button.frame = frame;
}

// applyToAllButtons re-applies deltaY to all three traffic lights, each
// relative to its own captured baseline rather than to whatever AppKit or a
// previous call last left it at.
//
// Called more than once by design: AppKit's own titlebar layout — governed
// by Auto Layout constraints on the button views, not by anything we can
// see or touch from here — runs again shortly after startup and again on
// every resize, and each time it does it silently resets frame.origin.y
// back to its own answer. A single application therefore does not stick;
// only reapplying after every occasion AppKit might have relaid things out
// does — which is safe specifically because applyNudge is idempotent.
static void applyToAllButtons(NSWindow *window, CGFloat deltaY) {
    applyNudge([window standardWindowButton:NSWindowCloseButton], &gCloseBaseY, deltaY);
    applyNudge([window standardWindowButton:NSWindowMiniaturizeButton], &gMiniaturizeBaseY, deltaY);
    applyNudge([window standardWindowButton:NSWindowZoomButton], &gZoomBaseY, deltaY);
}

// `assign`, not `weak`: cgo compiles Objective-C without ARC by default, and
// `weak` needs it. `assign` is the correct MRC-mode default here regardless
// — this observer and the window it points at both live for the app's
// entire lifetime, so there is no dangling-pointer window to worry about.
@interface PodSteerTrafficLightObserver : NSObject
@property (nonatomic, assign) NSWindow *window;
@property (nonatomic) CGFloat deltaY;
@end

@implementation PodSteerTrafficLightObserver
- (void)reapply:(NSNotification *)notification {
    NSWindow *window = self.window;
    if (window != nil) {
        applyToAllButtons(window, self.deltaY);
    }
}
@end

static PodSteerTrafficLightObserver *gObserver = nil;

// nudgeTrafficLights shifts the close/miniaturize/zoom buttons of the app's
// (single) window by deltaY points, relative to wherever AppKit originally
// put them, and keeps them there.
//
// Dispatched onto the main queue because AppKit views may only be touched
// from the main thread, and this can be called from a Go goroutine that is
// not it.
static void nudgeTrafficLights(CGFloat deltaY) {
    dispatch_async(dispatch_get_main_queue(), ^{
        NSWindow *window = nil;
        for (NSWindow *candidate in [[NSApplication sharedApplication] windows]) {
            if ([candidate standardWindowButton:NSWindowCloseButton] != nil) {
                window = candidate;
                break;
            }
        }
        if (window == nil) {
            return;
        }

        applyToAllButtons(window, deltaY);

        // A short burst of retries covers the layout pass (or passes) that
        // run just after startup and would otherwise undo the change above.
        // Safe to repeat: applyToAllButtons always targets the same
        // captured baseline, so this converges rather than compounds.
        for (NSInteger i = 1; i <= 6; i++) {
            int64_t delayNs = (int64_t)(i * 0.15 * (double)NSEC_PER_SEC);
            dispatch_after(dispatch_time(DISPATCH_TIME_NOW, delayNs), dispatch_get_main_queue(), ^{
                applyToAllButtons(window, deltaY);
            });
        }

        // Beyond startup, resizing — which a fullscreen toggle or a manual
        // drag both are — re-triggers the same titlebar layout, so the
        // observer keeps the nudge applied for the life of the window
        // rather than only at the moment this function was called.
        if (gObserver == nil) {
            gObserver = [PodSteerTrafficLightObserver new];
        }
        gObserver.window = window;
        gObserver.deltaY = deltaY;
        [[NSNotificationCenter defaultCenter] removeObserver:gObserver
                                                          name:NSWindowDidResizeNotification
                                                        object:window];
        [[NSNotificationCenter defaultCenter] addObserver:gObserver
                                                  selector:@selector(reapply:)
                                                      name:NSWindowDidResizeNotification
                                                    object:window];
    });
}
*/
import "C"

// NudgeTrafficLights shifts the traffic lights vertically by deltaY points,
// relative to wherever AppKit originally placed them, and keeps them there
// for the life of the window.
//
// AppKit centres them assuming a standard ~28pt title bar; this app draws
// its own, taller tab bar in its place, so left alone the lights read as
// sitting low within it. Positive moves them up the window (towards the top
// edge); negative moves them down — AppKit's view coordinate space is
// bottom-up, the reverse of CSS.
//
// Every reapplication (the retry burst right after startup, and every
// later resize) targets the same captured original position, so calling
// this — or letting it re-fire on its own — any number of times always
// converges on the same final spot rather than drifting further each time.
// Safe to call as soon as the window exists, which for Wails means from
// OnStartup — the buttons are created together with the window, before it
// is first shown.
func NudgeTrafficLights(deltaY float64) {
	C.nudgeTrafficLights(C.CGFloat(deltaY))
}
