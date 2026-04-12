package httpclient

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
)

type Client struct {
	baseURL string
}

func New(baseURL string) *Client {
	return &Client{baseURL: baseURL}
}

func (c *Client) Post(path string, data interface{}, headers map[string]string) (*http.Response, error) {
	body, err := json.Marshal(data)
	if err != nil {
		return nil, fmt.Errorf("marshal: %w", err)
	}
	req, err := http.NewRequest(http.MethodPost, fmt.Sprintf("%s/%s", c.baseURL, path), bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	return http.DefaultClient.Do(req)
}
