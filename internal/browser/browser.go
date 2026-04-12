package browser

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/chromedp/chromedp"

	"uts_bot/internal/config"
)

const defaultTimeout = 300 * time.Second

type Browser struct {
	ctx    context.Context
	cancel context.CancelFunc

	// First chromedp.Run must use b.ctx, not a short-lived child context, or the
	// browser's CDP read loop is cancelled when that child ends (see chromedp.Run docs).
	allocOnce sync.Once
	allocErr  error
}

func (b *Browser) ensureAllocated() error {
	b.allocOnce.Do(func() {
		b.allocErr = chromedp.Run(b.ctx)
	})
	return b.allocErr
}

func New() *Browser {
	slog.Info("INITIALIZING", "browser_debug", config.BrowserDebug)
	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.Flag("remote-debugging-port", "9222"),
		chromedp.NoFirstRun,
		chromedp.NoDefaultBrowserCheck,
	)
	if config.BrowserDebug {
		// Default options include chromedp.Headless; override for a normal window.
		opts = append(opts,
			chromedp.Flag("headless", false),
			chromedp.Flag("hide-scrollbars", false),
			chromedp.Flag("mute-audio", false),
		)
	}
	if config.ChromeNoSandbox {
		opts = append(opts,
			chromedp.Flag("no-sandbox", true),
			chromedp.Flag("disable-dev-shm-usage", true),
			chromedp.Flag("disable-gpu", true),
		)
	}
	if config.ChromeBin != "" {
		opts = append(opts, chromedp.ExecPath(config.ChromeBin))
	}
	allocCtx, _ := chromedp.NewExecAllocator(context.Background(), opts...)
	ctx, cancel := chromedp.NewContext(allocCtx)
	return &Browser{ctx: ctx, cancel: cancel}
}

func (b *Browser) Close() {
	slog.Info("CLOSING BROWSER")
	b.cancel()
}

func (b *Browser) OpenPage(url string) error {
	if err := b.ensureAllocated(); err != nil {
		return err
	}
	slog.Info("OPENING", "url", url)
	ctx, cancel := context.WithTimeout(b.ctx, defaultTimeout)
	defer cancel()
	return chromedp.Run(ctx, chromedp.Navigate(url))
}

func (b *Browser) Click(sel string) error {
	if err := b.ensureAllocated(); err != nil {
		return err
	}
	slog.Info("CLICKING", "sel", sel)
	ctx, cancel := context.WithTimeout(b.ctx, defaultTimeout)
	defer cancel()
	return chromedp.Run(ctx,
		chromedp.WaitVisible(sel),
		chromedp.Click(sel),
	)
}

// ClickElementAtIndex clicks the nth element matching sel. Supports negative index (-1 = last).
func (b *Browser) ClickElementAtIndex(sel string, index int) error {
	if err := b.ensureAllocated(); err != nil {
		return err
	}
	slog.Info("CLICKING ELEMENT", "sel", sel, "index", index)
	ctx, cancel := context.WithTimeout(b.ctx, defaultTimeout)
	defer cancel()
	if err := chromedp.Run(ctx, chromedp.WaitVisible(sel)); err != nil {
		return fmt.Errorf("wait %s: %w", sel, err)
	}
	var script string
	if index < 0 {
		script = fmt.Sprintf(
			`(function(){var e=document.querySelectorAll('%s');e[e.length%d].click()})()`,
			escapeJS(sel), index,
		)
	} else {
		script = fmt.Sprintf(
			`document.querySelectorAll('%s')[%d].click()`,
			escapeJS(sel), index,
		)
	}
	return chromedp.Run(ctx, chromedp.Evaluate(script, nil))
}

// ClickChild clicks the childIdx-th childSel inside the parentIdx-th parentSel.
func (b *Browser) ClickChild(parentSel string, parentIdx int, childSel string, childIdx int) error {
	if err := b.ensureAllocated(); err != nil {
		return err
	}
	slog.Info("CLICKING CHILD", "parent", parentSel, "parentIdx", parentIdx, "child", childSel, "childIdx", childIdx)
	ctx, cancel := context.WithTimeout(b.ctx, defaultTimeout)
	defer cancel()
	if err := chromedp.Run(ctx, chromedp.WaitVisible(parentSel)); err != nil {
		return fmt.Errorf("wait %s: %w", parentSel, err)
	}
	script := fmt.Sprintf(
		`document.querySelectorAll('%s')[%d].querySelectorAll('%s')[%d].click()`,
		escapeJS(parentSel), parentIdx, escapeJS(childSel), childIdx,
	)
	return chromedp.Run(ctx, chromedp.Evaluate(script, nil))
}

// TypeData types value into sel, optionally hitting Enter.
func (b *Browser) TypeData(sel, value string, hitEnter bool) error {
	if err := b.ensureAllocated(); err != nil {
		return err
	}
	slog.Info("TYPING", "sel", sel)
	ctx, cancel := context.WithTimeout(b.ctx, defaultTimeout)
	defer cancel()
	keys := value
	if hitEnter {
		keys += "\r"
	}
	var lastErr error
	for i := 0; i < 3; i++ {
		lastErr = chromedp.Run(ctx,
			chromedp.WaitVisible(sel),
			chromedp.SendKeys(sel, keys),
		)
		if lastErr == nil {
			return nil
		}
		time.Sleep(time.Second)
	}
	return fmt.Errorf("type_data failed after 3 retries: %w", lastErr)
}

// ClearAndType clears the field then types value (replaces existing text; e.g. Moodle prefilled username).
func (b *Browser) ClearAndType(sel, value string, hitEnter bool) error {
	if err := b.ensureAllocated(); err != nil {
		return err
	}
	slog.Info("CLEAR_AND_TYPE", "sel", sel)
	ctx, cancel := context.WithTimeout(b.ctx, defaultTimeout)
	defer cancel()
	keys := value
	if hitEnter {
		keys += "\r"
	}
	var lastErr error
	for i := 0; i < 3; i++ {
		lastErr = chromedp.Run(ctx,
			chromedp.WaitVisible(sel),
			chromedp.Clear(sel),
			chromedp.SendKeys(sel, keys),
		)
		if lastErr == nil {
			return nil
		}
		time.Sleep(time.Second)
	}
	return fmt.Errorf("clear_and_type failed after 3 retries: %w", lastErr)
}

func (b *Browser) GoBack() error {
	if err := b.ensureAllocated(); err != nil {
		return err
	}
	slog.Info("GOING TO PREVIOUS PAGE")
	ctx, cancel := context.WithTimeout(b.ctx, defaultTimeout)
	defer cancel()
	return chromedp.Run(ctx, chromedp.NavigateBack())
}

func (b *Browser) GetText(sel string) (string, error) {
	if err := b.ensureAllocated(); err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(b.ctx, defaultTimeout)
	defer cancel()
	var text string
	err := chromedp.Run(ctx,
		chromedp.WaitVisible(sel),
		chromedp.Text(sel, &text),
	)
	return text, err
}

func (b *Browser) ElementExists(sel string) bool {
	if err := b.ensureAllocated(); err != nil {
		return false
	}
	ctx, cancel := context.WithTimeout(b.ctx, 5*time.Second)
	defer cancel()
	var count int
	script := fmt.Sprintf(`document.querySelectorAll('%s').length`, escapeJS(sel))
	err := chromedp.Run(ctx, chromedp.Evaluate(script, &count))
	return err == nil && count > 0
}

// CountElements returns number of elements matching sel.
func (b *Browser) CountElements(sel string) (int, error) {
	var count int
	script := fmt.Sprintf(`document.querySelectorAll('%s').length`, escapeJS(sel))
	err := b.EvalJS(script, &count)
	return count, err
}

// CountElementsInParent counts childSel matches inside the parentIdx-th parentSel.
func (b *Browser) CountElementsInParent(parentSel string, parentIdx int, childSel string) (int, error) {
	var count int
	script := fmt.Sprintf(
		`document.querySelectorAll('%s')[%d].querySelectorAll('%s').length`,
		escapeJS(parentSel), parentIdx, escapeJS(childSel),
	)
	err := b.EvalJS(script, &count)
	return count, err
}

// GetElementText returns innerText of the nth element matching sel.
func (b *Browser) GetElementText(sel string, index int) (string, error) {
	var text string
	script := fmt.Sprintf(
		`document.querySelectorAll('%s')[%d].innerText`,
		escapeJS(sel), index,
	)
	err := b.EvalJS(script, &text)
	return text, err
}

// GetElementAttribute returns an attribute of the nth element matching sel.
func (b *Browser) GetElementAttribute(sel string, index int, attr string) (string, error) {
	var val string
	script := fmt.Sprintf(
		`document.querySelectorAll('%s')[%d].getAttribute('%s')`,
		escapeJS(sel), index, escapeJS(attr),
	)
	err := b.EvalJS(script, &val)
	return val, err
}

// GetChildText returns innerText of the childIdx-th childSel inside the parentIdx-th parentSel.
func (b *Browser) GetChildText(parentSel string, parentIdx int, childSel string, childIdx int) (string, error) {
	var text string
	script := fmt.Sprintf(
		`document.querySelectorAll('%s')[%d].querySelectorAll('%s')[%d].innerText`,
		escapeJS(parentSel), parentIdx, escapeJS(childSel), childIdx,
	)
	err := b.EvalJS(script, &text)
	return text, err
}

func (b *Browser) EvalJS(script string, result interface{}) error {
	if err := b.ensureAllocated(); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(b.ctx, defaultTimeout)
	defer cancel()
	return chromedp.Run(ctx, chromedp.Evaluate(script, result))
}

func (b *Browser) EvalJSString(script string) (string, error) {
	var result string
	err := b.EvalJS(script, &result)
	return result, err
}

func escapeJS(s string) string {
	return strings.ReplaceAll(s, "'", `\'`)
}
