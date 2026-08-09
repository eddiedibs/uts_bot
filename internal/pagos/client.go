package pagos

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Plain Chrome UA — the upstream WAF returns an HTML shell for custom bot User-Agents.
const userAgent = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"

// Client talks to the UFT pagos backend (JWT after login).
type Client struct {
	hc       *http.Client
	baseURL  string
	ci       string
	password string
}

// New builds a pagos API client. baseURL should end with /.
func New(baseURL, ci, password string) *Client {
	base := strings.TrimRight(strings.TrimSpace(baseURL), "/") + "/"
	return &Client{
		hc: &http.Client{
			Timeout: 60 * time.Second,
		},
		baseURL:  base,
		ci:       strings.TrimSpace(ci),
		password: password,
	}
}

type apiEnvelope struct {
	Status  int             `json:"status"`
	Message string          `json:"message"`
	Error   json.RawMessage `json:"error"`
	Object  json.RawMessage `json:"object"`
}

type loginObject struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	ExpiresAt   string `json:"expires_at"`
}

// Cuota is one student installment / fee from api/estudiante/cuotasAll.
type Cuota struct {
	ID               int             `json:"id"`
	Nombre           string          `json:"nombre"`
	Monto            string          `json:"monto"`
	FechaVencimiento string          `json:"fecha_vencimiento"`
	Description      *string         `json:"description"`
	Estado           string          `json:"estado"`
	Cedula           string          `json:"cedula"`
	Tipo             string          `json:"tipo"`
	Facturado        json.RawMessage `json:"facturado,omitempty"`
	CELApso          int             `json:"c_e_lapso"`
	CreatedAt        string          `json:"created_at"`
	UpdatedAt        string          `json:"updated_at"`
	CarreraLapso     json.RawMessage `json:"rp_carreraestudiante_lapso,omitempty"`
	Pagos            json.RawMessage `json:"pagos,omitempty"`
}

// PaymentFees is the payload returned by GetPaymentFees.
type PaymentFees struct {
	Cuotas []Cuota `json:"cuotas"`
}

// Login authenticates with ci/password and returns a Bearer access token.
func (c *Client) Login(ctx context.Context) (string, error) {
	if c.ci == "" || c.password == "" {
		return "", fmt.Errorf("set PAGOS_CI and PAGOS_PASSWORD (e.g. in .env)")
	}
	body, err := c.postJSON(ctx, "api/login", map[string]string{
		"ci":       c.ci,
		"password": c.password,
	}, "")
	if err != nil {
		return "", fmt.Errorf("pagos login: %w", err)
	}
	var env apiEnvelope
	if err := json.Unmarshal(body, &env); err != nil {
		return "", fmt.Errorf("pagos login: decode: %w", err)
	}
	if env.Status != 200 {
		msg := strings.TrimSpace(env.Message)
		if msg == "" {
			msg = "login failed"
		}
		return "", fmt.Errorf("pagos login: status %d: %s", env.Status, msg)
	}
	var obj loginObject
	if err := json.Unmarshal(env.Object, &obj); err != nil {
		return "", fmt.Errorf("pagos login: decode object: %w", err)
	}
	if obj.AccessToken == "" {
		return "", fmt.Errorf("pagos login: empty access_token")
	}
	return obj.AccessToken, nil
}

// CuotasAll returns all student installments for the authenticated user.
func (c *Client) CuotasAll(ctx context.Context, token string) ([]Cuota, error) {
	body, err := c.postJSON(ctx, "api/estudiante/cuotasAll", map[string]any{}, token)
	if err != nil {
		return nil, fmt.Errorf("pagos cuotasAll: %w", err)
	}
	var env apiEnvelope
	if err := json.Unmarshal(body, &env); err != nil {
		return nil, fmt.Errorf("pagos cuotasAll: decode: %w", err)
	}
	if env.Status != 200 {
		msg := strings.TrimSpace(env.Message)
		if msg == "" {
			msg = "cuotasAll failed"
		}
		return nil, fmt.Errorf("pagos cuotasAll: status %d: %s", env.Status, msg)
	}
	var cuotas []Cuota
	if err := json.Unmarshal(env.Object, &cuotas); err != nil {
		return nil, fmt.Errorf("pagos cuotasAll: decode object: %w", err)
	}
	return cuotas, nil
}

// GetPaymentFees logs in and fetches all installments.
func (c *Client) GetPaymentFees(ctx context.Context) (*PaymentFees, error) {
	token, err := c.Login(ctx)
	if err != nil {
		return nil, err
	}
	cuotas, err := c.CuotasAll(ctx, token)
	if err != nil {
		return nil, err
	}
	return &PaymentFees{Cuotas: cuotas}, nil
}

func (c *Client) postJSON(ctx context.Context, path string, payload any, bearer string) ([]byte, error) {
	const maxAttempts = 3
	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		body, err := c.postJSONOnce(ctx, path, payload, bearer)
		if err == nil {
			return body, nil
		}
		lastErr = err
		if attempt == maxAttempts || !isRetryableUpstream(err) {
			break
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(time.Duration(attempt) * 400 * time.Millisecond):
		}
	}
	return nil, lastErr
}

func (c *Client) postJSONOnce(ctx context.Context, path string, payload any, bearer string) ([]byte, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal: %w", err)
	}
	url := c.baseURL + strings.TrimPrefix(path, "/")
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("new request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Origin", "https://pagos.uft.edu.ve")
	req.Header.Set("Referer", "https://pagos.uft.edu.ve/rp/")
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("POST %s: HTTP %d: %s", path, resp.StatusCode, truncate(body))
	}
	ct := strings.ToLower(resp.Header.Get("Content-Type"))
	trim := bytes.TrimSpace(body)
	if len(trim) == 0 {
		return nil, fmt.Errorf("POST %s: empty body", path)
	}
	if strings.Contains(ct, "text/html") || trim[0] == '<' {
		return nil, fmt.Errorf("POST %s: non-JSON upstream response (html shell)", path)
	}
	return body, nil
}

func isRetryableUpstream(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "non-JSON upstream response") ||
		strings.Contains(msg, "connection reset") ||
		strings.Contains(msg, "EOF")
}

func truncate(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) > 512 {
		return s[:512] + "…"
	}
	return s
}
