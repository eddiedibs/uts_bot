package jobs

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"uts_bot/internal/browser"
	"uts_bot/internal/config"
	"uts_bot/internal/httpclient"
)

type Jobs struct {
	b      *browser.Browser
	client *httpclient.Client
}

func New(b *browser.Browser) *Jobs {
	return &Jobs{
		b:      b,
		client: httpclient.New(config.APIBaseURL),
	}
}

func (j *Jobs) GetJobPostings() error {
	if err := j.b.OpenPage(config.TargetPage); err != nil {
		return fmt.Errorf("open page: %w", err)
	}

	if err := j.b.TypeData("#search_term", "Python", true); err != nil {
		return fmt.Errorf("type search: %w", err)
	}
	if err := j.b.Click(`[aria-label="Sign in"]`); err != nil {
		return fmt.Errorf("click sign in: %w", err)
	}
	// Minimize messages overlay (last button in header controls)
	if err := j.b.ClickElementAtIndex(`.msg-overlay-bubble-header__controls button`, -1); err != nil {
		slog.Warn("minimize messages failed", "err", err)
	}

	if err := j.b.OpenPage(config.TargetPage + "/jobs/collections/recommended"); err != nil {
		return fmt.Errorf("navigate to jobs: %w", err)
	}
	time.Sleep(2 * time.Second)

	jobCount, err := j.b.CountElements(".jobs-search-results__list-item")
	if err != nil {
		return fmt.Errorf("count jobs: %w", err)
	}

	jobList := make([]string, 0, jobCount)
	for i := 0; i < jobCount; i++ {
		title, err := j.b.GetChildText(".jobs-search-results__list-item", i, "strong", 0)
		if err != nil {
			slog.Warn("get job title failed", "index", i, "err", err)
			continue
		}
		jobList = append(jobList, title)
	}
	slog.Info("jobs found", "count", len(jobList), "jobs", jobList)

	// Authenticate with local API
	tokenResp, err := j.client.Post("token", map[string]any{
		"username": "johnnytest",
		"password": "123456789",
		"userType": "client",
	}, map[string]string{
		"Cookie": "sessionid=vtyoe1lkbayltpjhedh5zz9peb2as7yl",
	})
	if err != nil {
		return fmt.Errorf("auth: %w", err)
	}
	defer tokenResp.Body.Close()

	var tokenData map[string]any
	if err := json.NewDecoder(tokenResp.Body).Decode(&tokenData); err != nil {
		return fmt.Errorf("decode token: %w", err)
	}
	accessToken, _ := tokenData["access_token"].(string)

	// Send AI request
	aiResp, err := j.client.Post("send-ai-request", map[string]any{
		"username": "johnnytest",
		"aiModel":  "llama3:instruct",
		"instruction": fmt.Sprintf(
			"Do any of these job titles correspond to your skills?, if so, return back in a list the job titles that DO correspond: %v",
			jobList,
		),
	}, map[string]string{
		"Authorization": "Bearer " + accessToken,
		"Cookie":        "sessionid=vtyoe1lkbayltpjhedh5zz9peb2as7yl",
	})
	if err != nil {
		return fmt.Errorf("ai request: %w", err)
	}
	defer aiResp.Body.Close()

	var aiData map[string]any
	if err := json.NewDecoder(aiResp.Body).Decode(&aiData); err != nil {
		return fmt.Errorf("decode ai response: %w", err)
	}
	slog.Info("AI response", "data", aiData)
	return nil
}
