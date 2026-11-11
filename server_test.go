package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeNotifier records requests instead of sending them.
type fakeNotifier struct {
	mu   sync.Mutex
	reqs []Request
	err  error
}

func (f *fakeNotifier) Notify(ctx context.Context, req Request) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	f.reqs = append(f.reqs, req)
	return nil
}

func newServer(n Notifier) *Server {
	return &Server{
		Notifier: n,
		Limiter:  &Limiter{PerClient: 3, Total: 30, Window: time.Hour, Now: time.Now},
		Page:     Page{Workspace: "OONI", ContactEmail: "contact@example.org", SignInURL: "https://example.slack.com/"},
	}
}

func submit(t *testing.T, s *Server, form url.Values, remote string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest("POST", "/", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if remote != "" {
		r.RemoteAddr = remote
	}
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	return w
}

func TestIndexShowsTheForm(t *testing.T) {
	w := httptest.NewRecorder()
	newServer(&fakeNotifier{}).Handler().ServeHTTP(w, httptest.NewRequest("GET", "/", nil))
	body := w.Body.String()
	if w.Code != 200 || !strings.Contains(body, `<form method="post" action="/">`) {
		t.Fatalf("status %d:\n%s", w.Code, body)
	}
	if !strings.Contains(w.Header().Get("Content-Security-Policy"), "form-action 'self'") {
		t.Fatal("the form may post elsewhere")
	}
	for _, want := range []string{`src="/assets/org-logo.png"`, `src="/assets/slack.svg"`, `href="https://example.slack.com/"`} {
		if !strings.Contains(body, want) {
			t.Errorf("no %s in the page", want)
		}
	}
}

func TestAssets(t *testing.T) {
	h := newServer(&fakeNotifier{}).Handler()
	for path, ctype := range map[string]string{
		"/assets/org-logo.png":   "image/png",
		"/assets/slack.svg":      "image/svg+xml",
		"/assets/favicon-32.png": "image/png",
	} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != 200 || w.Header().Get("Content-Type") != ctype || w.Body.Len() == 0 {
			t.Errorf("%s: status %d, %q, %d bytes", path, w.Code, w.Header().Get("Content-Type"), w.Body.Len())
		}
		if !strings.HasPrefix(w.Header().Get("Cache-Control"), "public") {
			t.Errorf("%s: not cacheable", path)
		}
	}
	for _, path := range []string{"/assets/", "/assets/missing.png", "/assets/../main.go"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code == 200 {
			t.Errorf("%s: status 200", path)
		}
	}
}

func TestRequestIsPassedOn(t *testing.T) {
	n := &fakeNotifier{}
	w := submit(t, newServer(n), url.Values{"email": {" ada@example.org "}, "note": {"I run probes"}}, "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "ada@example.org") {
		t.Fatalf("status %d:\n%s", w.Code, w.Body.String())
	}
	if len(n.reqs) != 1 || n.reqs[0] != (Request{Email: "ada@example.org", Note: "I run probes"}) {
		t.Fatalf("passed on %+v", n.reqs)
	}
}

func TestInvalidRequestsAreRefused(t *testing.T) {
	cases := map[string]url.Values{
		"no email":      {},
		"not an email":  {"email": {"ada"}},
		"no domain dot": {"email": {"ada@localhost"}},
		"two addresses": {"email": {"ada@example.org, bob@example.org"}},
		"display name":  {"email": {"Ada <ada@example.org>"}},
		"long note":     {"email": {"ada@example.org"}, "note": {strings.Repeat("x", 1001)}},
	}
	for name, form := range cases {
		n := &fakeNotifier{}
		w := submit(t, newServer(n), form, "")
		if w.Code != http.StatusUnprocessableEntity || len(n.reqs) != 0 {
			t.Errorf("%s: status %d, passed on %d", name, w.Code, len(n.reqs))
		}
	}
}

func TestHoneypotIsNotPassedOn(t *testing.T) {
	n := &fakeNotifier{}
	w := submit(t, newServer(n), url.Values{"email": {"bot@example.org"}, "website": {"http://spam"}}, "")
	if w.Code != 200 || len(n.reqs) != 0 {
		t.Fatalf("status %d, passed on %d", w.Code, len(n.reqs))
	}
}

func TestRateLimits(t *testing.T) {
	n := &fakeNotifier{}
	s := newServer(n)
	form := url.Values{"email": {"ada@example.org"}}
	for i := 0; i < 3; i++ {
		if w := submit(t, s, form, "192.0.2.1:1234"); w.Code != 200 {
			t.Fatalf("request %d: status %d", i, w.Code)
		}
	}
	if w := submit(t, s, form, "192.0.2.1:1234"); w.Code != http.StatusTooManyRequests {
		t.Fatalf("4th request from one client: status %d", w.Code)
	}
	if w := submit(t, s, form, "192.0.2.2:1234"); w.Code != 200 {
		t.Fatalf("another client: status %d", w.Code)
	}

	// Behind a load balancer, clients are told apart by the last address in
	// X-Forwarded-For: the ones before it are whatever the client sent
	s = newServer(n)
	s.ClientIPHeader = "X-Forwarded-For"
	post := func(xff string) int {
		r := httptest.NewRequest("POST", "/", strings.NewReader(form.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.Header.Set("X-Forwarded-For", xff)
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		return w.Code
	}
	for i, xff := range []string{"1.1.1.1, 198.51.100.1", "2.2.2.2, 198.51.100.1", "198.51.100.1"} {
		if code := post(xff); code != 200 {
			t.Fatalf("request %d (%s): status %d", i, xff, code)
		}
	}
	if code := post("3.3.3.3, 198.51.100.1"); code != http.StatusTooManyRequests {
		t.Fatalf("a made-up first address got past the limit: status %d", code)
	}
	if code := post("198.51.100.2"); code != 200 {
		t.Fatalf("another client: status %d", code)
	}
}

func TestClientIP(t *testing.T) {
	s := &Server{ClientIPHeader: "X-Forwarded-For"}
	cases := []struct{ remote, xff, want string }{
		{"192.0.2.1:1234", "", "192.0.2.1"},
		{"[2001:db8::1]:1234", "", "2001:db8::1"},
		{"[::ffff:192.0.2.1]:1234", "", "192.0.2.1"},
		{"10.0.0.1:1234", "1.1.1.1, 198.51.100.1", "198.51.100.1"},
		{"10.0.0.1:1234", "2001:db8::2", "2001:db8::2"},
		// Not an address: the connection's address is used
		{"10.0.0.1:1234", "1.1.1.1, garbage", "10.0.0.1"},
	}
	for _, c := range cases {
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = c.remote
		if c.xff != "" {
			r.Header.Set("X-Forwarded-For", c.xff)
		}
		if got := s.clientIP(r); got != c.want {
			t.Errorf("%s, %q: got %s, want %s", c.remote, c.xff, got, c.want)
		}
	}
}

func TestLimiterTotalAndWindow(t *testing.T) {
	now := time.Unix(0, 0)
	l := &Limiter{PerClient: 10, Total: 2, Window: time.Hour, Now: func() time.Time { return now }}
	if !l.Allow("a") || !l.Allow("b") || l.Allow("c") {
		t.Fatal("the total limit wasn't applied")
	}
	now = now.Add(time.Hour)
	if !l.Allow("c") {
		t.Fatal("the limit wasn't reset after the window")
	}
}

func TestNotifierFailureIsReported(t *testing.T) {
	w := submit(t, newServer(&fakeNotifier{err: errors.New("down")}), url.Values{"email": {"ada@example.org"}}, "")
	if w.Code != http.StatusBadGateway || !strings.Contains(w.Body.String(), "Please try again later") {
		t.Fatalf("status %d:\n%s", w.Code, w.Body.String())
	}
	// What they typed is kept, so they can send it again
	if !strings.Contains(w.Body.String(), `value="ada@example.org"`) {
		t.Fatal("the form lost the email address")
	}
}

func TestSlackWebhookMessage(t *testing.T) {
	var got map[string]any
	slack := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("%s with %q", r.Method, r.Header.Get("Content-Type"))
		}
		json.NewDecoder(r.Body).Decode(&got)
		w.Write([]byte("ok"))
	}))
	defer slack.Close()

	n := &SlackWebhook{URL: slack.URL, Workspace: "OONI", Client: slack.Client()}
	err := n.Notify(context.Background(), Request{
		Email: "ada@example.org",
		Note:  "hi <!channel> see <https://evil.example|this>\nsecond line",
	})
	if err != nil {
		t.Fatal(err)
	}
	text, _ := got["text"].(string)
	if !strings.Contains(text, "ada@example.org") || !strings.Contains(text, ">second line") {
		t.Fatalf("text: %q", text)
	}
	// What visitors type can't ping the channel or make links
	if strings.Contains(text, "<!channel>") || strings.Contains(text, "<https://") {
		t.Fatalf("not escaped: %q", text)
	}
	if got["unfurl_links"] != false {
		t.Fatalf("links unfurl: %v", got)
	}
}

func TestSlackWebhookError(t *testing.T) {
	slack := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "invalid_token", http.StatusForbidden)
	}))
	defer slack.Close()
	n := &SlackWebhook{URL: slack.URL, Workspace: "OONI", Client: slack.Client()}
	if err := n.Notify(context.Background(), Request{Email: "ada@example.org"}); err == nil ||
		!strings.Contains(err.Error(), "invalid_token") {
		t.Fatalf("error: %v", err)
	}
}

func TestFromEnv(t *testing.T) {
	env := func(m map[string]string) func(string) string {
		return func(k string) string { return m[k] }
	}
	hook := "https://hooks.slack.com/services/T000/B000/XXXX"
	bad := []map[string]string{
		nil,
		{"SLACK_WEBHOOK_URL": hook, "SLACK_WEBHOOK_URL_FILE": "/x"},
		{"SLACK_WEBHOOK_URL": "https://evil.example/hook"},
		{"SLACK_WEBHOOK_URL": "http://hooks.slack.com/services/x"},
		{"SLACK_WEBHOOK_URL_FILE": "/does/not/exist"},
		{"SLACK_WEBHOOK_URL": hook, "SLACK_SIGNIN_URL": "javascript:alert(1)"},
	}
	for _, m := range bad {
		if _, _, err := fromEnv(env(m)); err == nil {
			t.Errorf("accepted %v", m)
		}
	}
	s, addr, err := fromEnv(env(map[string]string{"SLACK_WEBHOOK_URL": hook + "\n"}))
	if err != nil || addr != ":3000" || s.Page.Workspace != "OONI" {
		t.Fatalf("defaults: %v %q %+v", err, addr, s)
	}
}
