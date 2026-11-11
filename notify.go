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
	"unicode"
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
	body, err := json.Marshal(s.message(req))
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, s.URL, bytes.NewReader(body))
	if err != nil {
		return errors.New("making the request to Slack")
	}
	httpReq.Header.Set("Content-Type", "application/json")
	resp, err := s.Client.Do(httpReq)
	if err != nil {
		// The error names the URL, which holds the webhook's secret: keep it
		// out of the logs
		var urlErr *url.Error
		if errors.As(err, &urlErr) {
			err = urlErr.Err
		}
		return fmt.Errorf("posting to Slack: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 200))
		return fmt.Errorf("Slack answered %d: %s", resp.StatusCode, msg)
	}
	return nil
}

// message builds the Slack message. What visitors typed goes in plain_text
// objects, which Slack shows as typed: no formatting, links or mentions, so
// an admin sees the address exactly as it will be invited.
func (s *SlackWebhook) message(req Request) map[string]any {
	plain := func(text string) map[string]any {
		return map[string]any{"type": "plain_text", "text": text, "emoji": false}
	}
	title := fmt.Sprintf("Request to join %s on Slack", s.Workspace)
	blocks := []map[string]any{
		{"type": "header", "text": plain(title)},
		{"type": "section", "text": plain("Email: " + req.Email)},
	}
	if req.Note != "" {
		blocks = append(blocks, map[string]any{"type": "section", "text": plain("Note: " + req.Note)})
	}
	blocks = append(blocks, map[string]any{
		"type":     "context",
		"elements": []any{plain("To invite them: workspace menu → Invite people. To decline, do nothing.")},
	})
	return map[string]any{
		// For notifications; nothing the visitor typed
		"text":         title,
		"blocks":       blocks,
		"unfurl_links": false,
		"unfurl_media": false,
	}
}

// hidden reports whether r is a control or formatting character, such as
// the bidi overrides that can hide or reorder text.
func hidden(r rune) bool {
	return unicode.IsControl(r) || unicode.Is(unicode.Cf, r)
}

// cleanNote removes control and formatting characters from a note, keeping
// line breaks.
func cleanNote(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '\n' {
			return r
		}
		if hidden(r) {
			return -1
		}
		return r
	}, strings.ReplaceAll(s, "\r\n", "\n"))
}
