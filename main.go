// slack-invite takes requests to join a Slack workspace and posts them to an
// admin channel, where an admin sends the invitation from Slack.
//
// Configuration, from the environment:
//
//	SLACK_WEBHOOK_URL       incoming webhook of the admin channel
//	                        (https://hooks.slack.com/services/...)
//	SLACK_WEBHOOK_URL_FILE  or a file holding it
//	WORKSPACE_NAME          shown on the page and in requests (default "OONI")
//	CONTACT_EMAIL           shown as a fallback for people who can't use the form
//	SLACK_SIGNIN_URL        the workspace's URL, linked for members to sign in
//	                        (https://<workspace>.slack.com/)
//	ASK_NOTE                true to ask what brings people to the workspace and
//	                        pass it on with their email address (default false:
//	                        only the email address is asked for)
//	CLIENT_IP_HEADER        header the load balancer puts the client's address
//	                        in, e.g. X-Forwarded-For (its last address is used);
//	                        only for rate limiting
//	LISTEN_ADDR             address to listen on (default ":3000")
//
// Nothing is stored: requests go to Slack, and email and IP addresses aren't
// logged.
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"
)

func main() {
	srv, addr, err := fromEnv(os.Getenv)
	if err != nil {
		log.Fatal(err)
	}

	httpServer := &http.Server{
		Addr:              addr,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      20 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    16 << 10,
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		httpServer.Shutdown(shutdown)
	}()

	log.Printf("listening on %s", addr)
	if err := httpServer.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}

func fromEnv(getenv func(string) string) (*Server, string, error) {
	webhook, file := getenv("SLACK_WEBHOOK_URL"), getenv("SLACK_WEBHOOK_URL_FILE")
	switch {
	case webhook != "" && file != "":
		return nil, "", errors.New("set SLACK_WEBHOOK_URL or SLACK_WEBHOOK_URL_FILE, not both")
	case file != "":
		data, err := os.ReadFile(file)
		if err != nil {
			return nil, "", fmt.Errorf("SLACK_WEBHOOK_URL_FILE: %w", err)
		}
		webhook = string(data)
	case webhook == "":
		return nil, "", errors.New("set SLACK_WEBHOOK_URL or SLACK_WEBHOOK_URL_FILE")
	}
	webhook, err := ValidateWebhookURL(webhook)
	if err != nil {
		return nil, "", fmt.Errorf("the Slack webhook: %w", err)
	}

	page := Page{Workspace: getenv("WORKSPACE_NAME"), ContactEmail: getenv("CONTACT_EMAIL")}
	if signIn := getenv("SLACK_SIGNIN_URL"); signIn != "" {
		u, err := url.Parse(signIn)
		if err != nil || u.Scheme != "https" || u.Host == "" {
			return nil, "", errors.New("SLACK_SIGNIN_URL must be an https URL")
		}
		page.SignInURL = u.String()
	}
	if page.Workspace == "" {
		page.Workspace = "OONI"
	}
	if v := getenv("ASK_NOTE"); v != "" {
		ask, err := strconv.ParseBool(v)
		if err != nil {
			return nil, "", fmt.Errorf("ASK_NOTE: %q is not true or false", v)
		}
		page.AskNote = ask
	}
	addr := getenv("LISTEN_ADDR")
	if addr == "" {
		addr = ":3000"
	}
	return &Server{
		Notifier: &SlackWebhook{
			URL:       webhook,
			Workspace: page.Workspace,
			Client:    &http.Client{Timeout: 15 * time.Second},
		},
		// A few requests per person, and few enough overall that a flood
		// can't bury the admin channel
		Limiter:        &Limiter{PerClient: 3, Total: 30, Window: time.Hour, Now: time.Now},
		Page:           page,
		ClientIPHeader: getenv("CLIENT_IP_HEADER"),
	}, addr, nil
}
