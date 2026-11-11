package main

import (
	"embed"
	"html/template"
	"log"
	"net/http"
	"net/mail"
	"net/netip"
	"strings"
	"unicode/utf8"
)

//go:embed templates/*.html
var templates embed.FS

var pages = template.Must(template.ParseFS(templates, "templates/*.html"))

const (
	maxEmail = 254
	maxNote  = 1000
)

// Page is what the templates show.
type Page struct {
	Workspace    string
	ContactEmail string
	Email        string
	Note         string
	Error        string
}

// Server takes requests to join the workspace and passes them to its admins.
type Server struct {
	Notifier Notifier
	Limiter  *Limiter
	Page     Page
	// ClientIPHeader, when set, is the header the reverse proxy puts the
	// client's address in (X-Forwarded-For behind an AWS load balancer).
	// Otherwise the connection's address is used.
	ClientIPHeader string
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.index)
	mux.HandleFunc("POST /{$}", s.request)
	mux.HandleFunc("GET /health", s.healthz)
	return securityHeaders(mux)
}

func (s *Server) index(w http.ResponseWriter, r *http.Request) {
	s.render(w, http.StatusOK, "index.html", s.Page)
}

func (s *Server) request(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	page := s.Page
	page.Email = strings.TrimSpace(r.PostFormValue("email"))
	page.Note = strings.TrimSpace(r.PostFormValue("note"))

	// Bots fill in the hidden field; act as if it worked
	if r.PostFormValue("website") != "" {
		s.render(w, http.StatusOK, "requested.html", page)
		return
	}

	if msg := validate(page.Email, page.Note); msg != "" {
		page.Error = msg
		s.render(w, http.StatusUnprocessableEntity, "index.html", page)
		return
	}

	if !s.Limiter.Allow(s.clientIP(r)) {
		page.Error = "There have been too many requests. Please try again in an hour."
		s.render(w, http.StatusTooManyRequests, "index.html", page)
		return
	}

	if err := s.Notifier.Notify(r.Context(), Request{Email: page.Email, Note: page.Note}); err != nil {
		log.Printf("passing on a request: %v", err)
		page.Error = "Your request couldn't be sent. Please try again later."
		s.render(w, http.StatusBadGateway, "index.html", page)
		return
	}
	s.render(w, http.StatusOK, "requested.html", page)
}

func (s *Server) healthz(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Write([]byte("ok\n"))
}

// validate returns what's wrong with a request, for the visitor, or "".
func validate(email, note string) string {
	if email == "" {
		return "Please enter your email address."
	}
	addr, err := mail.ParseAddress(email)
	if err != nil || addr.Address != email || addr.Name != "" ||
		len(email) > maxEmail || !strings.Contains(email[strings.LastIndex(email, "@")+1:], ".") {
		return "Please enter a valid email address, like name@example.org."
	}
	if utf8.RuneCountInString(note) > maxNote {
		return "Please keep the note under 1000 characters."
	}
	return ""
}

// clientIP is the address requests are rate limited by.
func (s *Server) clientIP(r *http.Request) string {
	if s.ClientIPHeader != "" {
		// The last address is the one the proxy saw; a client can make up
		// the ones before it (X-Forwarded-For)
		values := r.Header.Values(s.ClientIPHeader)
		if len(values) > 0 {
			last := values[len(values)-1]
			last = strings.TrimSpace(last[strings.LastIndex(last, ",")+1:])
			if addr, err := netip.ParseAddr(last); err == nil {
				return addr.Unmap().String()
			}
		}
	}
	if addrPort, err := netip.ParseAddrPort(r.RemoteAddr); err == nil {
		return addrPort.Addr().Unmap().String()
	}
	return r.RemoteAddr
}

func (s *Server) render(w http.ResponseWriter, status int, name string, page Page) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	if err := pages.ExecuteTemplate(w, name, page); err != nil {
		log.Printf("rendering %s: %v", name, err)
	}
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; form-action 'self'; frame-ancestors 'none'; base-uri 'none'")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r)
	})
}
