package apiauth

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestValidAPIKey_bearerAndHeader(t *testing.T) {
	t.Parallel()
	secret := "test-secret-key-12345"
	cases := []struct {
		name   string
		hdr    map[string]string
		wantOK bool
	}{
		{
			"bearer",
			map[string]string{"Authorization": "Bearer " + secret},
			true,
		},
		{
			"x-api-key",
			map[string]string{"X-API-Key": secret},
			true,
		},
		{
			"wrong",
			map[string]string{"Authorization": "Bearer wrong"},
			false,
		},
		{
			"empty",
			map[string]string{},
			false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			for k, v := range tc.hdr {
				req.Header.Set(k, v)
			}
			if got := ValidAPIKey(req, secret); got != tc.wantOK {
				t.Fatalf("ValidAPIKey = %v, want %v", got, tc.wantOK)
			}
		})
	}
}
