//go:build darwin

package main

/*
#cgo CFLAGS: -x objective-c -fobjc-arc -fblocks
#cgo LDFLAGS: -framework Cocoa -framework WebKit
#import <Cocoa/Cocoa.h>
#import <WebKit/WebKit.h>
#import <objc/runtime.h>

// go-shell intentionally exposes a very small window API. Install the window
// styling before it starts its AppKit event loop so the app's web content can
// occupy the title-bar area while macOS keeps the native traffic lights.
static id wingmanTitlebarObserver;
static id wingmanTitlebarResizeObserver;
static char wingmanTrafficLightOriginalFrameKey;
static char wingmanWindowDragHandlerKey;

@interface WingmanWindowDragHandler : NSObject <WKScriptMessageHandler>
@property(weak) NSWindow *window;
@end

@implementation WingmanWindowDragHandler
- (void)userContentController:(WKUserContentController *)userContentController
      didReceiveScriptMessage:(WKScriptMessage *)message {
	NSWindow *window = self.window;
	if (window == nil) {
		return;
	}

	NSEvent *event = NSApp.currentEvent;
	if (event.type != NSEventTypeLeftMouseDown || event.window != window) {
		event = [NSEvent mouseEventWithType:NSEventTypeLeftMouseDown
							 location:window.mouseLocationOutsideOfEventStream
						modifierFlags:0
							timestamp:NSProcessInfo.processInfo.systemUptime
						 windowNumber:window.windowNumber
							  context:nil
						  eventNumber:0
						   clickCount:1
							 pressure:1.0];
	}
	[window performWindowDragWithEvent:event];
}
@end

static NSString *WingmanWindowDragScript(void) {
	return @"(() => {"
		"if (window.__wingmanWindowDragInstalled) return;"
		"window.__wingmanWindowDragInstalled = true;"
		"document.addEventListener('mousedown', (event) => {"
			"if (event.button !== 0 || !(event.target instanceof Element)) return;"
			"const region = getComputedStyle(event.target)"
				".getPropertyValue('--wingman-window-drag').trim();"
			"if (region !== 'drag') return;"
			"event.preventDefault();"
			"window.webkit.messageHandlers.wingmanWindowDrag.postMessage(null);"
		"}, true);"
	"})();";
}

static void WingmanInstallWindowDragging(NSWindow *window) {
	if (![window.contentView isKindOfClass:[WKWebView class]]) {
		return;
	}

	WKWebView *webView = (WKWebView *)window.contentView;
	if (objc_getAssociatedObject(webView, &wingmanWindowDragHandlerKey) != nil) {
		return;
	}

	WingmanWindowDragHandler *handler = [WingmanWindowDragHandler new];
	handler.window = window;
	objc_setAssociatedObject(
		webView,
		&wingmanWindowDragHandlerKey,
		handler,
		OBJC_ASSOCIATION_RETAIN_NONATOMIC
	);

	WKUserContentController *controller = webView.configuration.userContentController;
	[controller addScriptMessageHandler:handler name:@"wingmanWindowDrag"];
	[controller addUserScript:[[WKUserScript alloc]
		initWithSource:WingmanWindowDragScript()
		injectionTime:WKUserScriptInjectionTimeAtDocumentStart
		forMainFrameOnly:YES]];
	[webView evaluateJavaScript:WingmanWindowDragScript() completionHandler:nil];
}

static void WingmanPositionTrafficLights(NSWindow *window) {
	NSArray<NSNumber *> *buttonTypes = @[
		@(NSWindowCloseButton),
		@(NSWindowMiniaturizeButton),
		@(NSWindowZoomButton),
	];
	for (NSNumber *value in buttonTypes) {
		NSButton *button = [window standardWindowButton:value.unsignedIntegerValue];
		if (button == nil || button.superview == nil) {
			continue;
		}
		NSValue *storedFrame = objc_getAssociatedObject(
			button,
			&wingmanTrafficLightOriginalFrameKey
		);
		if (storedFrame == nil) {
			storedFrame = [NSValue valueWithRect:button.frame];
			objc_setAssociatedObject(
				button,
				&wingmanTrafficLightOriginalFrameKey,
				storedFrame,
				OBJC_ASSOCIATION_RETAIN_NONATOMIC
			);
		}
		NSRect frame = storedFrame.rectValue;
		frame.origin.x += 4.0;
		frame.origin.y += button.superview.isFlipped ? 4.0 : -4.0;
		button.frame = frame;
	}
}

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
			WingmanInstallWindowDragging(window);

			if (@available(macOS 11.0, *)) {
				window.titlebarSeparatorStyle = NSTitlebarSeparatorStyleNone;
			}

			dispatch_async(dispatch_get_main_queue(), ^{
				WingmanPositionTrafficLights(window);
			});
		}];

	wingmanTitlebarResizeObserver = [[NSNotificationCenter defaultCenter]
		addObserverForName:NSWindowDidResizeNotification
		object:nil
		queue:[NSOperationQueue mainQueue]
		usingBlock:^(NSNotification *notification) {
			NSWindow *window = notification.object;
			if (![window.title isEqualToString:@"Wingman Agent"]) {
				return;
			}
			// AppKit lays out the standard window buttons again during every
			// live-resize step. Restore our offset in the same update so the
			// traffic lights do not jump to their default position while dragging.
			WingmanPositionTrafficLights(window);
			dispatch_async(dispatch_get_main_queue(), ^{
				WingmanPositionTrafficLights(window);
			});
		}];
}
*/
import "C"

func prepareCustomTitlebar() {
	C.WingmanPrepareCustomTitlebar()
}
