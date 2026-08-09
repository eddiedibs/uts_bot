package config

import (
	"testing"
	"time"
)

func TestGetEnvDurationRejectsZero(t *testing.T) {
	const key = "TEST_SCRAPE_REST"
	fallback := 2 * time.Minute

	tests := []struct {
		name  string
		value string
		want  time.Duration
	}{
		{name: "unset", value: "", want: fallback},
		{name: "valid", value: "45s", want: 45 * time.Second},
		{name: "zero would be a hot loop", value: "0", want: fallback},
		{name: "negative", value: "-1m", want: fallback},
		{name: "malformed", value: "30", want: fallback},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(key, tt.value)
			if got := getEnvDuration(key, fallback); got != tt.want {
				t.Errorf("getEnvDuration(%q) = %v, want %v", tt.value, got, tt.want)
			}
		})
	}
}

func TestGetEnvDurationAllowZero(t *testing.T) {
	const key = "TEST_SCRAPE_STARTUP_DELAY"
	fallback := 2 * time.Minute

	tests := []struct {
		name  string
		value string
		want  time.Duration
	}{
		{name: "unset", value: "", want: fallback},
		{name: "valid", value: "30s", want: 30 * time.Second},
		{name: "zero means scrape immediately", value: "0", want: 0},
		{name: "negative", value: "-1m", want: fallback},
		{name: "malformed", value: "soon", want: fallback},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(key, tt.value)
			if got := getEnvDurationAllowZero(key, fallback); got != tt.want {
				t.Errorf("getEnvDurationAllowZero(%q) = %v, want %v", tt.value, got, tt.want)
			}
		})
	}
}
