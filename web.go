package main

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"embed"
	"encoding/hex"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata"
)

//go:embed templates/*.html static/style.css
var assets embed.FS

type App struct {
	store        *Store
	username     [32]byte
	password     [32]byte
	secureCookie bool
	templates    *template.Template
}

func newApp(store *Store, username, password [32]byte, secure bool) *App {
	return &App{store: store, username: username, password: password, secureCookie: secure,
		templates: template.Must(template.ParseFS(assets, "templates/*.html"))}
}

type cell struct {
	Text, URL string
}
type row struct {
	Cells []cell
}
type option struct {
	Value, Label string
	Selected     bool
}
type field struct {
	Name, Label, Type, Value, Hint string
	Required                       bool
	Options                        []option
}
type detail struct {
	Label, Value, URL string
}
type section struct {
	Title, AddURL string
	Headers       []string
	Rows          []row
}
type page struct {
	Title, Subtitle, Kind, Error, CSRF, Query, Action, RecordURL string
	Fields                                                       []field
	Headers                                                      []string
	Rows                                                         []row
	Details                                                      []detail
	Sections                                                     []section
	Counts                                                       []detail
	Filters                                                      []option
	CanDelete                                                    bool
}

var titles = map[string]string{
	"customers": "Klienci", "motorcycles": "Magazyn motocykli",
	"owned": "Motocykle klientów", "contracts": "Umowy sprzedaży",
}

func pathFor(kind string, id int64) string {
	return fmt.Sprintf("/%s/%d", kind, id)
}

func (a *App) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Referrer-Policy", "same-origin")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self'; img-src 'self'; script-src 'none'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
	w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
	if a.secureCookie || r.TLS != nil {
		w.Header().Set("Strict-Transport-Security", "max-age=31536000")
	}
	username, password, ok := r.BasicAuth()
	userHash, passHash := sha256.Sum256([]byte(username)), sha256.Sum256([]byte(password))
	valid := subtle.ConstantTimeCompare(userHash[:], a.username[:]) & subtle.ConstantTimeCompare(passHash[:], a.password[:])
	if !ok || valid != 1 {
		w.Header().Set("WWW-Authenticate", `Basic realm="Triumph Polska CRM", charset="UTF-8"`)
		http.Error(w, "Wymagane logowanie.", http.StatusUnauthorized)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodPost && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD, POST")
		http.Error(w, "Niedozwolona metoda.", http.StatusMethodNotAllowed)
		return
	}
	if r.URL.Path == "/static/style.css" {
		if r.Method == http.MethodPost {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "Niedozwolona metoda.", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "text/css; charset=utf-8")
		css, _ := assets.ReadFile("static/style.css")
		if r.Method != http.MethodHead {
			_, _ = w.Write(css)
		}
		return
	}
	token, err := a.csrf(w, r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	if r.URL.Path == "/" {
		if r.Method == http.MethodPost {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "Niedozwolona metoda.", http.StatusMethodNotAllowed)
			return
		}
		a.dashboard(w, r, token)
		return
	}
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	kind := parts[0]
	if _, ok := titles[kind]; !ok || len(parts) > 3 {
		http.NotFound(w, r)
		return
	}
	if len(parts) == 1 {
		if r.Method == http.MethodPost {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "Niedozwolona metoda.", http.StatusMethodNotAllowed)
			return
		}
		a.list(w, r, kind, token)
		return
	}
	if parts[1] == "new" && len(parts) == 2 {
		a.form(w, r, kind, 0, token)
		return
	}
	id, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || id < 1 || !exists(a.store.snapshot(), kind, id) {
		http.NotFound(w, r)
		return
	}
	if len(parts) == 3 && parts[2] == "edit" {
		a.form(w, r, kind, id, token)
		return
	}
	if len(parts) == 3 && parts[2] == "delete" && r.Method == http.MethodPost {
		err := a.store.update(func(s *state) error {
			if !exists(*s, kind, id) {
				return fmt.Errorf("Rekord już nie istnieje.")
			}
			switch kind {
			case "customers":
				delete(s.Customers, id)
			case "motorcycles":
				delete(s.Motorcycles, id)
			case "owned":
				delete(s.Owned, id)
			case "contracts":
				delete(s.Contracts, id)
			}
			return nil
		})
		if err != nil {
			a.show(w, r, kind, id, token, err.Error(), http.StatusConflict)
			return
		}
		http.Redirect(w, r, "/"+kind+"?saved=1", http.StatusSeeOther)
		return
	}
	if len(parts) == 3 && parts[2] == "delete" {
		w.Header().Set("Allow", "POST")
		http.Error(w, "Usuwanie wymaga formularza POST.", http.StatusMethodNotAllowed)
		return
	}
	if len(parts) != 2 {
		http.NotFound(w, r)
		return
	}
	if r.Method == http.MethodPost {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "Niedozwolona metoda.", http.StatusMethodNotAllowed)
		return
	}
	a.show(w, r, kind, id, token, "", http.StatusOK)
}

func (a *App) csrf(w http.ResponseWriter, r *http.Request) (string, error) {
	cookie, err := r.Cookie("crm_csrf")
	token := ""
	if err == nil {
		decoded, decodeErr := hex.DecodeString(cookie.Value)
		if decodeErr == nil && len(decoded) == 32 {
			token = cookie.Value
		}
	}
	if r.Method != http.MethodPost {
		if token == "" {
			bytes := make([]byte, 32)
			if _, err := rand.Read(bytes); err != nil {
				return "", fmt.Errorf("Nie udało się utworzyć bezpiecznego formularza.")
			}
			token = hex.EncodeToString(bytes)
			http.SetCookie(w, &http.Cookie{Name: "crm_csrf", Value: token, Path: "/", HttpOnly: true,
				Secure: a.secureCookie || r.TLS != nil, SameSite: http.SameSiteStrictMode})
		}
		return token, nil
	}
	origin := r.Header.Get("Origin")
	isOrigin := origin != ""
	if !isOrigin {
		origin = r.Header.Get("Referer")
	}
	u, parseErr := url.Parse(origin)
	scheme := "http"
	if a.secureCookie || r.TLS != nil {
		scheme = "https"
	}
	if parseErr != nil || u == nil || u.Scheme != scheme || u.Host != r.Host ||
		u.User != nil || u.Opaque != "" || (isOrigin && (u.Path != "" || u.RawQuery != "" || u.Fragment != "")) ||
		r.Header.Get("Sec-Fetch-Site") == "cross-site" {
		return "", fmt.Errorf("Odrzucono formularz z innego źródła. Odśwież stronę i spróbuj ponownie.")
	}
	if r.Header.Get("Content-Type") != "application/x-www-form-urlencoded" {
		return "", fmt.Errorf("Nieprawidłowy format formularza.")
	}
	r.Body = http.MaxBytesReader(w, r.Body, 32<<10)
	if err := r.ParseForm(); err != nil {
		return "", fmt.Errorf("Formularz jest nieprawidłowy lub zbyt duży (limit 32 KB).")
	}
	submitted := r.PostForm.Get("csrf_token")
	if token == "" || len(submitted) != len(token) || subtle.ConstantTimeCompare([]byte(token), []byte(submitted)) != 1 {
		return "", fmt.Errorf("Nieprawidłowy token bezpieczeństwa. Odśwież stronę i spróbuj ponownie.")
	}
	return token, nil
}

func exists(s state, kind string, id int64) bool {
	switch kind {
	case "customers":
		_, ok := s.Customers[id]
		return ok
	case "motorcycles":
		_, ok := s.Motorcycles[id]
		return ok
	case "owned":
		_, ok := s.Owned[id]
		return ok
	case "contracts":
		_, ok := s.Contracts[id]
		return ok
	}
	return false
}

func (a *App) render(w http.ResponseWriter, r *http.Request, name string, p page, status int) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if r.Method != http.MethodHead {
		_ = a.templates.ExecuteTemplate(w, name, p)
	}
}

func warsawNow() time.Time {
	location, _ := time.LoadLocation("Europe/Warsaw")
	return time.Now().In(location)
}
