//go:build darwin

package main

/*
#cgo CFLAGS: -x objective-c -fobjc-arc -fblocks
#cgo LDFLAGS: -framework Cocoa
#import <Cocoa/Cocoa.h>

// go-shell intentionally exposes a very small window API. Install the window
// styling before it starts its AppKit event loop so the app's web content can
// occupy the title-bar area while macOS keeps the native traffic lights.
static id wingmanTitlebarObserver;

static void WingmanPrepareCustomTitlebar(void) {
	if (wingmanTitlebarObserver != nil) {
		return;
	}

	wingmanTitlebarObserver = [[NSNotificationCenter defaultCenter]
		addObserverForName:NSWindowDidBecomeKeyNotification
		object:nil
		queue:[NSOperationQueue mainQueue]
		usingBlock:^(NSNotification *notification) {
			NSWindow *window = notification.object;
			if (![window.title isEqualToString:@"Wingman Agent"]) {
				return;
			}

			window.styleMask |= NSWindowStyleMaskFullSizeContentView;
			window.titleVisibility = NSWindowTitleHidden;
			window.titlebarAppearsTransparent = YES;
			window.movableByWindowBackground = YES;

			if (@available(macOS 11.0, *)) {
				window.titlebarSeparatorStyle = NSTitlebarSeparatorStyleNone;
			}
		}];
}
*/
import "C"

func prepareCustomTitlebar() {
	C.WingmanPrepareCustomTitlebar()
}
