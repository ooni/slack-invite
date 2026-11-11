package main

import (
	"embed"
	"html/template"
	"log"
	"net/http"
	"net/mail"
	"net/netip"
	"strconv"
	"strings"
	"unicode/utf8"
)

//go:embed templates/*.html
var templates embed.FS

// The logos and favicon, served by the service so pages load nothing from
// other sites
//
//go:embed assets
var assets embed.FS

var pages = template.Must(template.ParseFS(templates, "templates/*.html"))

const (
	maxEmail = 254
	maxNote  = 1000
)

// Page is what the templates show.
type Page struct {
	Workspace    string
	ContactEmail string
	SignInURL    string
	// AskNote shows the "What brings you" box and passes on what is
	// typed in it. Off by default: nothing visitors write reaches the admins
	// except their email address.
	AskNote bool
	Email   string
	Note    string
	Error   string
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
	mux.Handle("GET /assets/{file}", cacheFor(86400, http.FileServerFS(assets)))
	// slackin's badge, which READMEs embed; this one has no member count
	mux.Handle("GET /badge.svg", cacheFor(86400, http.HandlerFunc(badge)))
	// Refuse forms posted from other sites, which could otherwise send
	// requests through their visitors' browsers
	return securityHeaders(http.NewCrossOriginProtection().Handler(mux))
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
	if s.Page.AskNote {
		page.Note = strings.TrimSpace(cleanNote(r.PostFormValue("note")))
	}

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
	if err != nil || addr.Address != email || addr.Name != "" || strings.ContainsFunc(email, hidden) ||
		len(email) > maxEmail || !strings.Contains(email[strings.LastIndex(email, "@")+1:], ".") {
		return "Please enter a valid email address, like name@example.org."
	}
	if utf8.RuneCountInString(note) > maxNote {
		return "Please keep the note under 1000 characters."
	}
	return ""
}

// clientIP is what requests are rate limited by: the client's IPv4
// address, or its IPv6 /64, since one machine usually has a whole /64.
func (s *Server) clientIP(r *http.Request) string {
	if s.ClientIPHeader != "" {
		// The last address is the one the proxy saw; a client can make up
		// the ones before it (X-Forwarded-For)
		values := r.Header.Values(s.ClientIPHeader)
		if len(values) > 0 {
			last := values[len(values)-1]
			last = strings.TrimSpace(last[strings.LastIndex(last, ",")+1:])
			if addr, err := netip.ParseAddr(last); err == nil {
				return clientKey(addr)
			}
		}
	}
	if addrPort, err := netip.ParseAddrPort(r.RemoteAddr); err == nil {
		return clientKey(addrPort.Addr())
	}
	return r.RemoteAddr
}

func clientKey(addr netip.Addr) string {
	addr = addr.Unmap()
	if addr.Is6() {
		return netip.PrefixFrom(addr, 64).Masked().String()
	}
	return addr.String()
}

func (s *Server) render(w http.ResponseWriter, status int, name string, page Page) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	if err := pages.ExecuteTemplate(w, name, page); err != nil {
		log.Printf("rendering %s: %v", name, err)
	}
}

func badge(w http.ResponseWriter, r *http.Request) {
	http.ServeFileFS(w, r, assets, "assets/badge.svg")
}

func cacheFor(seconds int, next http.Handler) http.Handler {
	value := "public, max-age=" + strconv.Itoa(seconds)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", value)
		next.ServeHTTP(w, r)
	})
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", "default-src 'none'; img-src 'self'; style-src 'unsafe-inline'; form-action 'self'; frame-ancestors 'none'; base-uri 'none'")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		// The load balancer redirects http to https; browsers then stay on https
		h.Set("Strict-Transport-Security", "max-age=31536000")
		next.ServeHTTP(w, r)
	})
}
