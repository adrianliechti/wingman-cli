package browser

import (
	"context"
	"errors"
	"os/exec"
	"runtime"
	"time"

	"github.com/adrianliechti/wingman-cli/pkg/tool"

	htmltomarkdown "github.com/JohannesKaufmann/html-to-markdown/v2"
	"github.com/chromedp/chromedp"
)

func New() (*Client, error) {
	path := findExecPath()

	if path == "" {
		return nil, errors.New("could not find a suitable Chrome or Edge executable")
	}

	opts := append(
		chromedp.DefaultExecAllocatorOptions[:],
		chromedp.ExecPath(path),
		chromedp.Flag("headless", false),
	)

	allocatorCtx, allocatorCloser := chromedp.NewExecAllocator(context.Background(), opts...)

	chromeCtx, chromeCloser := chromedp.NewContext(allocatorCtx)

	c := &Client{
		allocatorCtx:    allocatorCtx,
		allocatorCloser: allocatorCloser,

		chromeCtx:    chromeCtx,
		chromeCloser: chromeCloser,
	}

	return c, nil
}

var (
	_ tool.Provider = (*Client)(nil)
)

type Client struct {
	allocatorCtx    context.Context
	allocatorCloser context.CancelFunc

	chromeCtx    context.Context
	chromeCloser context.CancelFunc
}

func (c *Client) Close() error {
	if c.chromeCloser != nil {
		c.chromeCloser()
	}

	if c.allocatorCloser != nil {
		c.allocatorCloser()
	}

	return nil
}

func (c *Client) Tools(ctx context.Context) ([]tool.Tool, error) {
	query_tool := tool.Tool{

		Name:        "fetch_website",
		Description: "fetch and return the markdown content from a given URL, including website pages and similar sources",

		Schema: &tool.Schema{
			Type: "object",

			Properties: map[string]*tool.Schema{
				"url": {
					Type:        "string",
					Description: "the URL of the website to crawl starting with http:// or https://",
				},
			},

			Required: []string{"url"},
		},

		ToolHandler: func(ctx context.Context, params map[string]any) (any, error) {
			url, ok := params["url"].(string)

			if !ok {
				return nil, errors.New("missing url parameter")
			}

			markdown, err := c.RenderHTML(ctx, url)

			if err != nil {
				return nil, err
			}

			return markdown, nil
		},
	}

	return []tool.Tool{
		query_tool,
	}, nil
}

func (c *Client) RenderHTML(ctx context.Context, url string) (string, error) {

	var buf string

	if err := chromedp.Run(c.chromeCtx, pageOuterHTML(url, &buf)); err != nil {
		return "", err
	}

	markdown, err := htmltomarkdown.ConvertString(buf)

	if err != nil {
		return "", err
	}

	return markdown, err
}

func pageOuterHTML(urlstr string, html *string) chromedp.Tasks {
	return chromedp.Tasks{
		chromedp.Navigate(urlstr),
		chromedp.WaitReady("body", chromedp.ByQuery),
		// Add CSS to hide cookie banners and consent dialogs
		chromedp.ActionFunc(func(ctx context.Context) error {
			return chromedp.Evaluate(`
				// Add CSS to hide cookie banners and consent dialogs
				const style = document.createElement('style');
				style.textContent = `+"`"+`
					[id*="cookie" i]:not([id*="cookieless" i]),
					[class*="cookie" i]:not([class*="cookieless" i]),
					[id*="consent" i],
					[class*="consent" i],
					[id*="gdpr" i],
					[class*="gdpr" i],
					[aria-modal="true"],
					.modal-backdrop,
					.cookie-banner,
					.consent-banner,
					.cookie-notice,
					.cookie-bar,
					.gdpr-banner,
					.privacy-notice {
						display: none !important;
						visibility: hidden !important;
						opacity: 0 !important;
						z-index: -9999 !important;
						position: absolute !important;
						left: -9999px !important;
					}
				`+"`"+`;
				document.head.appendChild(style);
			`, nil).Do(ctx)
		}),
		chromedp.Sleep(500 * time.Millisecond),
		// Try to click accept buttons for cookie banners using JavaScript
		chromedp.ActionFunc(func(ctx context.Context) error {
			return chromedp.Evaluate(`
				// Try to find and click common accept buttons
				const acceptButtons = [
					...document.querySelectorAll('button, a, div'),
				].filter(el => {
					const text = el.textContent.toLowerCase().trim();
					return text.includes('accept') || 
						   text.includes('agree') || 
						   text.includes('allow all') ||
						   text.includes('ok') ||
						   el.id.toLowerCase().includes('accept') ||
						   el.className.toLowerCase().includes('accept');
				});
				
				// Click the first matching button found
				if (acceptButtons.length > 0) {
					acceptButtons[0].click();
				}
			`, nil).Do(ctx)
		}),
		// Remove cookie banners via JavaScript
		chromedp.ActionFunc(func(ctx context.Context) error {
			return chromedp.Evaluate(`
				// Remove cookie banners and consent dialogs
				const bannersToRemove = document.querySelectorAll(`+"`"+`
					[id*="cookie" i]:not([id*="cookieless" i]),
					[class*="cookie" i]:not([class*="cookieless" i]),
					[id*="consent" i],
					[class*="consent" i],
					[id*="gdpr" i],
					[class*="gdpr" i],
					[aria-modal="true"],
					.modal-backdrop,
					.cookie-banner,
					.consent-banner,
					.cookie-notice,
					.cookie-bar,
					.gdpr-banner,
					.privacy-notice
				`+"`"+`);
				bannersToRemove.forEach(el => {
					if (el && el.parentNode) {
						el.parentNode.removeChild(el);
					}
				});
				
				// Remove overlay divs that might be blocking content
				const overlays = document.querySelectorAll('div[style*="position: fixed"], div[style*="position: absolute"]');
				overlays.forEach(overlay => {
					const style = window.getComputedStyle(overlay);
					if (style.zIndex > 1000 && (style.backgroundColor === 'rgba(0, 0, 0, 0.5)' || style.backgroundColor === 'rgb(0, 0, 0)')) {
						overlay.remove();
					}
				});
			`, nil).Do(ctx)
		}),
		chromedp.Sleep(500 * time.Millisecond),
		chromedp.OuterHTML("html", html),
	}
}

func findExecPath() string {
	var locations []string
	switch runtime.GOOS {
	case "darwin":
		locations = []string{
			"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
			"/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge",
		}
	case "windows":
		locations = []string{
			`C:\Program Files (x86)\Google\Chrome\Application\chrome.exe`,
			`C:\Program Files\Google\Chrome\Application\chrome.exe`,
			`C:\Program Files (x86)\Microsoft\Edge\Application\msedge.exe`,
			`C:\Program Files\Microsoft\Edge\Application\msedge.exe`,
		}
	default:
		locations = []string{
			// Unix-like
			"headless_shell",
			"headless-shell",
			"chromium",
			"chromium-browser",
			"google-chrome",
			"google-chrome-stable",
			"google-chrome-beta",
			"google-chrome-unstable",
			"/usr/bin/google-chrome",
			"/usr/local/bin/chrome",
			"/snap/bin/chromium",
			"chrome",
		}
	}

	for _, path := range locations {
		found, err := exec.LookPath(path)
		if err == nil {
			return found
		}
	}

	return ""
}
