package main

import (
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"
)

const testUsername = "test-operator"
const testPassword = "test-only-password"
const testOrigin = "http://crm.example.test"

func testApp(t *testing.T) *App {
	return newApp(testStore(t), sha256.Sum256([]byte(testUsername)), sha256.Sum256([]byte(testPassword)), false)
}

type browser struct {
	app    *App
	cookie *http.Cookie
}

func (b *browser) request(method, path string, values url.Values) *httptest.ResponseRecorder {
	if values == nil {
		values = url.Values{}
	}
	if method == http.MethodPost && b.cookie != nil {
		values.Set("csrf_token", b.cookie.Value)
	}
	req := httptest.NewRequest(method, testOrigin+path, strings.NewReader(values.Encode()))
	req.SetBasicAuth(testUsername, testPassword)
	if b.cookie != nil {
		req.AddCookie(b.cookie)
	}
	if method == http.MethodPost {
		req.Header.Set("Origin", testOrigin)
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	rec := httptest.NewRecorder()
	b.app.ServeHTTP(rec, req)
	for _, cookie := range rec.Result().Cookies() {
		if cookie.Name == "crm_csrf" {
			b.cookie = cookie
		}
	}
	return rec
}

func wantStatus(t *testing.T, response *httptest.ResponseRecorder, want int) {
	t.Helper()
	if response.Code != want {
		t.Fatalf("status = %d, want %d\n%s", response.Code, want, response.Body.String())
	}
}

func wantHTML(t *testing.T, response *httptest.ResponseRecorder, snippets ...string) {
	t.Helper()
	wantStatus(t, response, http.StatusOK)
	for _, snippet := range snippets {
		if !strings.Contains(response.Body.String(), snippet) {
			t.Fatalf("missing HTML %q\n%s", snippet, response.Body.String())
		}
	}
	if response.Header().Get("Content-Type") != "text/html; charset=utf-8" {
		t.Fatal("HTML content type missing")
	}
}

func createViaWeb(t *testing.T, b *browser, kind string, values url.Values) (int64, string) {
	t.Helper()
	wantHTML(t, b.request("GET", "/"+kind+"/new", nil), `name="csrf_token"`, `method="post"`)
	result := b.request("POST", "/"+kind+"/new", values)
	wantStatus(t, result, http.StatusSeeOther)
	target := strings.TrimSuffix(result.Header().Get("Location"), "?saved=1")
	parts := strings.Split(target, "/")
	if len(parts) != 3 || parts[1] != kind || parseID(parts[2]) == 0 {
		t.Fatalf("invalid redirect %q", target)
	}
	wantStatus(t, b.request("GET", target, nil), http.StatusOK)
	return parseID(parts[2]), target
}

func TestAuthenticationRequired(t *testing.T) {
	app := testApp(t)
	for _, path := range []string{"/", "/customers", "/customers/new", "/contracts", "/static/style.css", "/unknown"} {
		for _, credentials := range [][2]string{{"", ""}, {testUsername, "incorrect"}, {"incorrect", testPassword}} {
			req := httptest.NewRequest("GET", testOrigin+path, nil)
			if credentials[0] != "" {
				req.SetBasicAuth(credentials[0], credentials[1])
			}
			rec := httptest.NewRecorder()
			app.ServeHTTP(rec, req)
			if rec.Code != http.StatusUnauthorized || rec.Header().Get("WWW-Authenticate") == "" {
				t.Fatalf("unauthenticated route %s: %d", path, rec.Code)
			}
		}
	}
	b := &browser{app: app}
	response := b.request("GET", "/", nil)
	wantStatus(t, response, http.StatusOK)
	for _, header := range []string{"Content-Security-Policy", "X-Frame-Options", "X-Content-Type-Options", "Referrer-Policy", "Cache-Control", "Permissions-Policy"} {
		if response.Header().Get(header) == "" {
			t.Errorf("missing security header %s", header)
		}
	}
	if b.cookie == nil || !b.cookie.HttpOnly || b.cookie.SameSite != http.SameSiteStrictMode || b.cookie.Secure || len(b.cookie.Value) != 64 {
		t.Fatalf("invalid development CSRF cookie: %#v", b.cookie)
	}
	req := httptest.NewRequest("POST", testOrigin+"/customers/new", strings.NewReader(customerForm().Encode()))
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	wantStatus(t, rec, http.StatusUnauthorized)
	if len(app.store.snapshot().Customers) != 0 {
		t.Fatal("unauthenticated mutation occurred")
	}
}

func TestCSRFProtection(t *testing.T) {
	tests := []struct {
		name string
		edit func(*http.Request, url.Values, *http.Cookie)
	}{
		{"missing cookie", func(r *http.Request, v url.Values, c *http.Cookie) { r.Header.Del("Cookie") }},
		{"mismatched token", func(r *http.Request, v url.Values, c *http.Cookie) { v.Set("csrf_token", strings.Repeat("0", 64)) }},
		{"missing token", func(r *http.Request, v url.Values, c *http.Cookie) { v.Del("csrf_token") }},
		{"foreign origin", func(r *http.Request, v url.Values, c *http.Cookie) { r.Header.Set("Origin", "http://evil.example") }},
		{"foreign origin with same referer", func(r *http.Request, v url.Values, c *http.Cookie) {
			r.Header.Set("Origin", "http://evil.example")
			r.Header.Set("Referer", testOrigin+"/customers/new")
		}},
		{"null origin", func(r *http.Request, v url.Values, c *http.Cookie) { r.Header.Set("Origin", "null") }},
		{"wrong scheme", func(r *http.Request, v url.Values, c *http.Cookie) {
			r.Header.Set("Origin", "https://crm.example.test")
		}},
		{"origin path", func(r *http.Request, v url.Values, c *http.Cookie) { r.Header.Set("Origin", testOrigin+"/path") }},
		{"missing origin and referer", func(r *http.Request, v url.Values, c *http.Cookie) { r.Header.Del("Origin") }},
		{"foreign referer", func(r *http.Request, v url.Values, c *http.Cookie) {
			r.Header.Del("Origin")
			r.Header.Set("Referer", "http://crm.example.test.evil/customers")
		}},
		{"cross site fetch", func(r *http.Request, v url.Values, c *http.Cookie) { r.Header.Set("Sec-Fetch-Site", "cross-site") }},
		{"wrong content type", func(r *http.Request, v url.Values, c *http.Cookie) { r.Header.Set("Content-Type", "application/json") }},
		{"query token only", func(r *http.Request, v url.Values, c *http.Cookie) {
			r.URL.RawQuery = "csrf_token=" + c.Value
			v.Del("csrf_token")
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := &browser{app: testApp(t)}
			wantStatus(t, b.request("GET", "/customers/new", nil), http.StatusOK)
			values := customerForm()
			values.Set("csrf_token", b.cookie.Value)
			req := httptest.NewRequest("POST", testOrigin+"/customers/new", nil)
			req.SetBasicAuth(testUsername, testPassword)
			req.Header.Set("Origin", testOrigin)
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			req.AddCookie(b.cookie)
			tt.edit(req, values, b.cookie)
			req.Body = io.NopCloser(strings.NewReader(values.Encode()))
			rec := httptest.NewRecorder()
			b.app.ServeHTTP(rec, req)
			wantStatus(t, rec, http.StatusForbidden)
			if len(b.app.store.snapshot().Customers) != 0 {
				t.Fatal("CSRF request modified data")
			}
		})
	}
	t.Run("same origin referer fallback", func(t *testing.T) {
		b := &browser{app: testApp(t)}
		b.request("GET", "/customers/new", nil)
		values := customerForm()
		values.Set("csrf_token", b.cookie.Value)
		req := httptest.NewRequest("POST", testOrigin+"/customers/new", strings.NewReader(values.Encode()))
		req.SetBasicAuth(testUsername, testPassword)
		req.AddCookie(b.cookie)
		req.Header.Set("Referer", testOrigin+"/customers/new")
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rec := httptest.NewRecorder()
		b.app.ServeHTTP(rec, req)
		wantStatus(t, rec, http.StatusSeeOther)
	})
	t.Run("form body limit", func(t *testing.T) {
		b := &browser{app: testApp(t)}
		b.request("GET", "/customers/new", nil)
		values := customerForm()
		values.Set("first_name", strings.Repeat("a", 33<<10))
		wantStatus(t, b.request("POST", "/customers/new", values), http.StatusForbidden)
		if len(b.app.store.snapshot().Customers) != 0 {
			t.Fatal("oversized request modified data")
		}
	})
}

func TestSecureCookieAndOrigin(t *testing.T) {
	app := testApp(t)
	app.secureCookie = true
	b := &browser{app: app}
	response := b.request("GET", "/customers/new", nil)
	if b.cookie == nil || !b.cookie.Secure || response.Header().Get("Strict-Transport-Security") == "" {
		t.Fatal("production cookie/HSTS not secure")
	}
	wantStatus(t, b.request("POST", "/customers/new", customerForm()), http.StatusForbidden)
	values := customerForm()
	values.Set("csrf_token", b.cookie.Value)
	req := httptest.NewRequest("POST", "https://crm.example.test/customers/new", strings.NewReader(values.Encode()))
	req.SetBasicAuth(testUsername, testPassword)
	req.AddCookie(b.cookie)
	req.Header.Set("Origin", "https://crm.example.test")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	wantStatus(t, rec, http.StatusSeeOther)
}

func TestCRMEndToEnd(t *testing.T) {
	b := &browser{app: testApp(t)}
	wantHTML(t, b.request("GET", "/", nil), "TRIUMPH", "Dobry dzień", "Ostatnio dodane umowy", "Brak rekordów")
	for _, kind := range []string{"customers", "motorcycles", "owned", "contracts"} {
		wantHTML(t, b.request("GET", "/"+kind, nil), `href="/`+kind+`/new"`, "Brak rekordów")
	}
	customer, customerURL := createViaWeb(t, b, "customers", customerForm())
	motorcycle, motorcycleURL := createViaWeb(t, b, "motorcycles", motorcycleForm())
	wantHTML(t, b.request("GET", "/owned/new?customer_id="+fmt.Sprint(customer), nil), `value="`+fmt.Sprint(customer)+`" selected`, "Dowolna marka")
	owned, ownedURL := createViaWeb(t, b, "owned", ownedForm(customer))
	contract, contractURL := createViaWeb(t, b, "contracts", contractForm(customer, motorcycle))
	wantHTML(t, b.request("GET", customerURL, nil), "Anna Kowalska", "Motocykle klienta", "Honda CB500F", "TP/2026/001",
		`href="`+ownedURL+`"`, `href="`+contractURL+`"`, `href="/owned/new?customer_id=`+fmt.Sprint(customer)+`"`)
	wantHTML(t, b.request("GET", motorcycleURL, nil), "Zarezerwowany", "54 900,00 zł", `href="`+contractURL+`"`)
	wantHTML(t, b.request("GET", contractURL, nil), "Projekt", "5 000,25 zł", "47 999,75 zł",
		`href="`+customerURL+`"`, `href="`+motorcycleURL+`"`)
	wantHTML(t, b.request("GET", "/customers?q=anna", nil), "Anna Kowalska", "1 rekordów")
	wantHTML(t, b.request("GET", "/owned?q=honda", nil), "Honda CB500F", "Anna Kowalska")
	wantHTML(t, b.request("GET", "/motorcycles?q=NT123456", nil), "Bonneville T120")
	wantHTML(t, b.request("GET", "/contracts?status=active", nil), "TP/2026/001", "1 rekordów")
	wantHTML(t, b.request("GET", "/contracts?status=cancelled", nil), "Brak rekordów")
	wantHTML(t, b.request("GET", "/customers?q=unmatched", nil), "Brak rekordów")

	customerValues := customerForm()
	customerValues.Set("city", "Kraków")
	wantHTML(t, b.request("GET", customerURL+"/edit", nil), `value="Anna"`)
	wantStatus(t, b.request("POST", customerURL+"/edit", customerValues), http.StatusSeeOther)
	wantHTML(t, b.request("GET", customerURL, nil), "Kraków", "Utworzono", "Zaktualizowano")
	motorcycleValues := motorcycleForm()
	motorcycleValues.Set("color", "Red")
	wantStatus(t, b.request("POST", motorcycleURL+"/edit", motorcycleValues), http.StatusSeeOther)
	ownedValues := ownedForm(customer)
	ownedValues.Set("registration", "KR 4321")
	wantStatus(t, b.request("POST", ownedURL+"/edit", ownedValues), http.StatusSeeOther)
	wantHTML(t, b.request("GET", ownedURL, nil), "KR 4321")
	contractValues := contractForm(customer, motorcycle)
	contractValues.Set("status", "signed")
	wantStatus(t, b.request("POST", contractURL+"/edit", contractValues), http.StatusSeeOther)
	wantHTML(t, b.request("GET", motorcycleURL, nil), "Sprzedany")
	wantStatus(t, b.request("POST", customerURL+"/delete", nil), http.StatusConflict)
	wantStatus(t, b.request("POST", motorcycleURL+"/delete", nil), http.StatusConflict)
	contractValues.Set("status", "cancelled")
	wantStatus(t, b.request("POST", contractURL+"/edit", contractValues), http.StatusSeeOther)
	wantHTML(t, b.request("GET", motorcycleURL, nil), "Dostępny")
	wantStatus(t, b.request("POST", motorcycleURL+"/delete", nil), http.StatusConflict)
	replacement := contractForm(customer, motorcycle)
	replacement.Set("number", "TP/2026/002")
	_, replacementURL := createViaWeb(t, b, "contracts", replacement)
	wantHTML(t, b.request("GET", "/contracts?status=cancelled", nil), "TP/2026/001")

	before := b.app.store.snapshot()
	reopened, err := openStore(b.app.store.path)
	if err != nil {
		t.Fatal(err)
	}
	b.app = newApp(reopened, sha256.Sum256([]byte(testUsername)), sha256.Sum256([]byte(testPassword)), false)
	if !reflect.DeepEqual(before, b.app.store.snapshot()) {
		t.Fatal("restart lost CRM state")
	}
	wantHTML(t, b.request("GET", customerURL, nil), "Honda CB500F", "TP/2026/002", "Kraków")
	for _, path := range []string{replacementURL, contractURL, ownedURL, motorcycleURL, customerURL} {
		wantStatus(t, b.request("POST", path+"/delete", nil), http.StatusSeeOther)
		wantStatus(t, b.request("GET", path, nil), http.StatusNotFound)
	}
	after := b.app.store.snapshot()
	if len(after.Customers)+len(after.Motorcycles)+len(after.Contracts)+len(after.Owned) != 0 ||
		before.Owned[owned].ID == 0 || before.Contracts[contract].ID == 0 {
		t.Fatal("CRUD lifecycle did not complete")
	}
	t.Log("HTML lifecycle verified: all four entities, search/filter, related navigation, edit, sign/cancel/resale, protected deletion, restart, final deletion")
}

func TestWebValidationAndEscaping(t *testing.T) {
	b := &browser{app: testApp(t)}
	customer, _ := createViaWeb(t, b, "customers", customerForm())
	motorcycle, _ := createViaWeb(t, b, "motorcycles", motorcycleForm())
	b.request("GET", "/contracts/new", nil)
	values := contractForm(customer, motorcycle)
	values.Set("deposit", "53000.01")
	response := b.request("POST", "/contracts/new", values)
	wantStatus(t, response, http.StatusUnprocessableEntity)
	if !strings.Contains(response.Body.String(), "zaliczka nie może przekroczyć ceny") ||
		!strings.Contains(response.Body.String(), `value="53000.01"`) {
		t.Fatal("validation error or submitted value not rendered")
	}
	values.Set("deposit", "-1")
	wantStatus(t, b.request("POST", "/contracts/new", values), http.StatusUnprocessableEntity)
	values = contractForm(customer, motorcycle)
	createViaWeb(t, b, "contracts", values)
	values.Set("number", "TP/2026/002")
	response = b.request("POST", "/contracts/new", values)
	wantStatus(t, response, http.StatusUnprocessableEntity)
	if !strings.Contains(response.Body.String(), "ma już aktywną umowę") {
		t.Fatal("active-contract conflict not rendered")
	}
	values = customerForm()
	values.Set("first_name", `<script>alert("x")</script>`)
	_, path := createViaWeb(t, b, "customers", values)
	response = b.request("GET", path, nil)
	if strings.Contains(response.Body.String(), `<script>`) || !strings.Contains(response.Body.String(), "&lt;script&gt;") {
		t.Fatal("user HTML was not escaped")
	}
}

func TestSafeMethodsAndUnknownRoutes(t *testing.T) {
	b := &browser{app: testApp(t)}
	customer, path := createViaWeb(t, b, "customers", customerForm())
	before := b.app.store.snapshot()
	for _, target := range []string{path + "/delete", "/customers/new", path + "/edit"} {
		got := b.request("GET", target, nil)
		if strings.HasSuffix(target, "/delete") {
			wantStatus(t, got, http.StatusMethodNotAllowed)
		} else {
			wantStatus(t, got, http.StatusOK)
		}
	}
	wantStatus(t, b.request("POST", path, nil), http.StatusMethodNotAllowed)
	wantStatus(t, b.request("PUT", path, nil), http.StatusMethodNotAllowed)
	for _, target := range []string{"/unknown", "/customers/9999", "/customers/no-id", "/customers/0", "/static/store.go", "/static/../store.go", "/customers/new/extra", path + "/unknown"} {
		wantStatus(t, b.request("GET", target, nil), http.StatusNotFound)
	}
	head := b.request("HEAD", "/", nil)
	wantStatus(t, head, http.StatusOK)
	if head.Body.Len() != 0 {
		t.Fatal("HEAD returned a body")
	}
	css := b.request("GET", "/static/style.css", nil)
	wantStatus(t, css, http.StatusOK)
	if !strings.Contains(css.Header().Get("Content-Type"), "text/css") {
		t.Fatal("stylesheet not served")
	}
	if !reflect.DeepEqual(before, b.app.store.snapshot()) || b.app.store.snapshot().Customers[customer].ID != customer {
		t.Fatal("safe/invalid requests changed data")
	}
}
