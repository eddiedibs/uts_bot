package moodlehttp

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
)

// UserAgent identifies this client; Moodle may block empty UA.
const userAgent = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36 uts_bot/1.0"

// Client is an HTTP client with a cookie jar for Moodle session auth.
type Client struct {
	hc *http.Client
}

// New returns a client suitable for Moodle scraping (cookies, timeouts).
func New() *Client {
	jar, _ := cookiejar.New(nil)
	return &Client{
		hc: &http.Client{
			Jar:     jar,
			Timeout: 120 * time.Second,
		},
	}
}

func (c *Client) do(req *http.Request) (*http.Response, error) {
	if req.Header.Get("User-Agent") == "" {
		req.Header.Set("User-Agent", userAgent)
	}
	return c.hc.Do(req)
}

// Get performs GET and returns the response body on 2xx.
func (c *Client) Get(ctx context.Context, rawURL string) ([]byte, error) {
	body, _, err := c.GetFinal(ctx, rawURL)
	return body, err
}

// GetFinal performs GET (following redirects) and returns the body and final request URL.
func (c *Client) GetFinal(ctx context.Context, rawURL string) ([]byte, *url.URL, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("new request: %w", err)
	}
	resp, err := c.do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, nil, fmt.Errorf("GET %s: status %d: %s", rawURL, resp.StatusCode, truncateErrBody(body))
	}
	u := resp.Request.URL
	if u == nil {
		pu, _ := url.Parse(rawURL)
		u = pu
	}
	return body, u, nil
}

// PostForm posts application/x-www-form-urlencoded data and returns the body on 2xx.
func (c *Client) PostForm(ctx context.Context, postURL string, data url.Values) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, postURL, strings.NewReader(data.Encode()))
	if err != nil {
		return nil, fmt.Errorf("new request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", userAgent)
	resp, err := c.do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("POST %s: status %d: %s", postURL, resp.StatusCode, truncateErrBody(body))
	}
	return body, nil
}

// LoginMoodle loads the start page, submits the Moodle login form, and keeps session cookies.
func (c *Client) LoginMoodle(ctx context.Context, startURL, username, password string) error {
	if username == "" || password == "" {
		return fmt.Errorf("set UTS_USERNAME and UTS_PASSWORD (e.g. in .env)")
	}
	body, pageURL, err := c.GetFinal(ctx, startURL)
	if err != nil {
		return fmt.Errorf("get login page: %w", err)
	}
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("parse login page: %w", err)
	}
	// Already authenticated (no password field on page).
	if doc.Find(`input[type="password"][name="password"]`).Length() == 0 &&
		doc.Find(`input#password[type="password"]`).Length() == 0 {
		return nil
	}
	form := doc.Find("form#login").First()
	if form.Length() == 0 {
		doc.Find("form").Each(func(_ int, fs *goquery.Selection) {
			if form.Length() > 0 {
				return
			}
			if fs.Find(`input[name="username"]`).Length() > 0 && fs.Find(`input[name="password"]`).Length() > 0 {
				form = fs
			}
		})
	}
	if form.Length() == 0 {
		return fmt.Errorf("no Moodle login form found at %s", startURL)
	}
	vals := url.Values{}
	form.Find(`input[type="hidden"]`).Each(func(_ int, s *goquery.Selection) {
		name, _ := s.Attr("name")
		if name == "" {
			return
		}
		v, _ := s.Attr("value")
		vals.Set(name, v)
	})
	vals.Set("username", username)
	vals.Set("password", password)

	action, _ := form.Attr("action")
	postURL, err := resolveFormAction(pageURL, action)
	if err != nil {
		return fmt.Errorf("login form action: %w", err)
	}
	postBody, err := c.PostForm(ctx, postURL, vals)
	if err != nil {
		return fmt.Errorf("post login: %w", err)
	}
	doc2, err := goquery.NewDocumentFromReader(bytes.NewReader(postBody))
	if err != nil {
		return fmt.Errorf("parse post-login page: %w", err)
	}
	if doc2.Find(".loginerrors, .errorbox").Length() > 0 {
		msg := strings.TrimSpace(doc2.Find(".loginerrors, .errorbox").First().Text())
		if msg != "" {
			return fmt.Errorf("login failed: %s", msg)
		}
		return fmt.Errorf("login failed: invalid credentials")
	}
	// Still showing password field usually means failed login without a styled error.
	if doc2.Find(`input[type="password"][name="password"]`).Length() > 0 &&
		doc2.Find(`input[name="username"]`).Length() > 0 {
		return fmt.Errorf("login failed: still on login page (check UTS_USERNAME / UTS_PASSWORD)")
	}
	return nil
}

func resolveFormAction(pageURL *url.URL, action string) (string, error) {
	if action == "" {
		return pageURL.String(), nil
	}
	ref, err := url.Parse(action)
	if err != nil {
		return "", err
	}
	return pageURL.ResolveReference(ref).String(), nil
}

func truncateErrBody(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) > 512 {
		return s[:512] + "…"
	}
	return s
}
