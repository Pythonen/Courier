package tui

import (
	"fmt"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"
)

type storedCookie struct {
	Name        string        `json:"name"`
	Value       string        `json:"value"`
	Domain      string        `json:"domain"`
	Path        string        `json:"path"`
	ExpiresUnix int64         `json:"expires_unix,omitempty"`
	Secure      bool          `json:"secure,omitempty"`
	HTTPOnly    bool          `json:"http_only,omitempty"`
	SameSite    http.SameSite `json:"same_site,omitempty"`
	HostOnly    bool          `json:"host_only,omitempty"`
}

type persistentCookieJar struct {
	mu      sync.Mutex
	jar     http.CookieJar
	cookies map[string]storedCookie
}

func newPersistentCookieJar() *persistentCookieJar {
	jar, _ := cookiejar.New(nil)
	return &persistentCookieJar{jar: jar, cookies: make(map[string]storedCookie)}
}

func (jar *persistentCookieJar) Cookies(target *url.URL) []*http.Cookie {
	jar.mu.Lock()
	defer jar.mu.Unlock()
	return jar.jar.Cookies(target)
}

func (jar *persistentCookieJar) SetCookies(target *url.URL, cookies []*http.Cookie) {
	if target == nil {
		return
	}
	jar.mu.Lock()
	defer jar.mu.Unlock()
	jar.jar.SetCookies(target, cookies)
	now := time.Now()
	for _, cookie := range cookies {
		if cookie == nil || !validStoredCookieName(cookie.Name) {
			continue
		}
		domain, hostOnly, ok := normalizedCookieDomain(target.Hostname(), cookie.Domain)
		if !ok {
			continue
		}
		path := cookie.Path
		if path == "" || path[0] != '/' {
			path = defaultCookiePath(target.EscapedPath())
		}
		key := storedCookieKey(domain, path, cookie.Name)
		if cookie.MaxAge < 0 || (!cookie.Expires.IsZero() && !cookie.Expires.After(now)) {
			delete(jar.cookies, key)
			continue
		}
		expires := cookie.Expires
		if cookie.MaxAge > 0 {
			expires = now.Add(time.Duration(cookie.MaxAge) * time.Second)
		}
		record := storedCookie{
			Name: cookie.Name, Value: cookie.Value, Domain: domain, Path: path,
			Secure: cookie.Secure, HTTPOnly: cookie.HttpOnly, SameSite: cookie.SameSite, HostOnly: hostOnly,
		}
		if !expires.IsZero() {
			record.ExpiresUnix = expires.Unix()
		}
		jar.cookies[key] = record
	}
	jar.purgeExpiredLocked(now)
}

func (jar *persistentCookieJar) Snapshot() []storedCookie {
	jar.mu.Lock()
	defer jar.mu.Unlock()
	jar.purgeExpiredLocked(time.Now())
	result := make([]storedCookie, 0, len(jar.cookies))
	for _, cookie := range jar.cookies {
		result = append(result, cookie)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Domain != result[j].Domain {
			return result[i].Domain < result[j].Domain
		}
		if result[i].Path != result[j].Path {
			return result[i].Path < result[j].Path
		}
		return result[i].Name < result[j].Name
	})
	return result
}

func (jar *persistentCookieJar) Restore(cookies []storedCookie) {
	for _, record := range cookies {
		if record.Name == "" || record.Domain == "" || record.Path == "" {
			continue
		}
		scheme := "http"
		if record.Secure {
			scheme = "https"
		}
		target := &url.URL{Scheme: scheme, Host: record.Domain, Path: record.Path}
		cookie := &http.Cookie{
			Name: record.Name, Value: record.Value, Path: record.Path,
			Secure: record.Secure, HttpOnly: record.HTTPOnly, SameSite: record.SameSite,
		}
		if !record.HostOnly {
			cookie.Domain = record.Domain
		}
		if record.ExpiresUnix > 0 {
			cookie.Expires = time.Unix(record.ExpiresUnix, 0)
		}
		jar.SetCookies(target, []*http.Cookie{cookie})
	}
}

func (jar *persistentCookieJar) Delete(record storedCookie) {
	scheme := "http"
	if record.Secure {
		scheme = "https"
	}
	target := &url.URL{Scheme: scheme, Host: record.Domain, Path: record.Path}
	cookie := &http.Cookie{Name: record.Name, Value: "", Path: record.Path, MaxAge: -1}
	if !record.HostOnly {
		cookie.Domain = record.Domain
	}
	jar.SetCookies(target, []*http.Cookie{cookie})
}

func (jar *persistentCookieJar) Add(rawURL, rawCookie string) error {
	target, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || target.Hostname() == "" || (target.Scheme != "http" && target.Scheme != "https") {
		return fmt.Errorf("cookie URL must be an absolute http:// or https:// URL")
	}
	response := &http.Response{Header: http.Header{"Set-Cookie": []string{rawCookie}}}
	cookies := response.Cookies()
	if len(cookies) != 1 || cookies[0].Name == "" {
		return fmt.Errorf("invalid Set-Cookie value")
	}
	jar.SetCookies(target, cookies)
	return nil
}

func (jar *persistentCookieJar) Clear() {
	jar.mu.Lock()
	defer jar.mu.Unlock()
	base, _ := cookiejar.New(nil)
	jar.jar = base
	jar.cookies = make(map[string]storedCookie)
}

func (jar *persistentCookieJar) purgeExpiredLocked(now time.Time) {
	for key, cookie := range jar.cookies {
		if cookie.ExpiresUnix > 0 && !time.Unix(cookie.ExpiresUnix, 0).After(now) {
			delete(jar.cookies, key)
		}
	}
}

func normalizedCookieDomain(host, attribute string) (string, bool, bool) {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	attribute = strings.ToLower(strings.Trim(strings.TrimSpace(attribute), "."))
	if attribute == "" {
		return host, true, host != ""
	}
	if host == attribute {
		return attribute, false, true
	}
	if net.ParseIP(host) != nil || !strings.HasSuffix(host, "."+attribute) {
		return "", false, false
	}
	return attribute, false, true
}

func defaultCookiePath(requestPath string) string {
	if requestPath == "" || requestPath[0] != '/' {
		return "/"
	}
	lastSlash := strings.LastIndexByte(requestPath, '/')
	if lastSlash <= 0 {
		return "/"
	}
	return requestPath[:lastSlash]
}

func storedCookieKey(domain, path, name string) string { return domain + "\x00" + path + "\x00" + name }

func validStoredCookieName(name string) bool {
	if name == "" {
		return false
	}
	for _, character := range name {
		if character <= 0x20 || character >= 0x7f || strings.ContainsRune("()<>@,;:\\\"/[]?={}", character) {
			return false
		}
	}
	return true
}

func (m *model) persistentJar() *persistentCookieJar {
	if m.client == nil {
		return nil
	}
	jar, _ := m.client.Jar.(*persistentCookieJar)
	return jar
}

// Cookies returns a stable snapshot of the local workspace cookie jar.
func (m *model) Cookies() []storedCookie {
	if jar := m.persistentJar(); jar != nil {
		return jar.Snapshot()
	}
	return nil
}

// SetCookie parses a Set-Cookie value for a URL and adds it to the local jar.
func (m *model) SetCookie(rawURL, rawCookie string) error {
	jar := m.persistentJar()
	if jar == nil {
		return fmt.Errorf("cookie jar is unavailable")
	}
	return jar.Add(rawURL, rawCookie)
}

// ClearCookies removes all locally stored cookies.
func (m *model) ClearCookies() {
	if jar := m.persistentJar(); jar != nil {
		jar.Clear()
	}
}
