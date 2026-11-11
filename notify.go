package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Request is someone asking to be invited.
type Request struct {
	Email string
	Note  string
}

// Notifier tells the workspace admins about a Request.
type Notifier interface {
	Notify(ctx context.Context, req Request) error
}

// ValidateWebhookURL checks that raw is a Slack incoming webhook, so requests
// can't be sent anywhere else by mistake.
func ValidateWebhookURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	u, err := url.Parse(raw)
	if err != nil || raw == "" {
		return "", errors.New("not a URL")
	}
	if u.Scheme != "https" || u.Hostname() != "hooks.slack.com" {
		return "", errors.New("must be an https://hooks.slack.com/ incoming webhook")
	}
	return u.String(), nil
}

// SlackWebhook posts requests to a Slack channel through an incoming webhook.
type SlackWebhook struct {
	URL       string
	Workspace string
	Client    *http.Client
}

func (s *SlackWebhook) Notify(ctx context.Context, req Request) error {
	text := fmt.Sprintf("*Request to join %s on Slack*\n*Email:* %s\n",
		escape(s.Workspace), escape(req.Email))
	if req.Note != "" {
		text += fmt.Sprintf("*Note:*\n>%s\n", strings.ReplaceAll(escape(req.Note), "\n", "\n>"))
	}
	text += "To invite them: Slack menu → Invite people. To decline, do nothing."

	body, err := json.Marshal(map[string]any{
		"text":         text,
		"unfurl_links": false,
		"unfurl_media": false,
	})
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, s.URL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	resp, err := s.Client.Do(httpReq)
	if err != nil {
		return fmt.Errorf("posting to Slack: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 200))
		return fmt.Errorf("Slack answered %d: %s", resp.StatusCode, msg)
	}
	return nil
}

// escape makes text safe to put in a Slack message: no links, mentions or
// @channel pings from what visitors type.
func escape(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(s)
}
