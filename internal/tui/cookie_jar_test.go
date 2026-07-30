package tui

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"testing"
	"time"
)

func TestPersistentCookieJarRoundTripAttributesAndDeletion(t *testing.T) {
	jar := newPersistentCookieJar()
	target, _ := url.Parse("https://api.example.test/account/login")
	jar.SetCookies(target, []*http.Cookie{{
		Name: "session", Value: "secret", Domain: ".example.test", MaxAge: 3600,
		Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode,
	}})
	records := jar.Snapshot()
	if len(records) != 1 {
		t.Fatalf("stored cookies = %#v", records)
	}
	record := records[0]
	if record.Domain != "example.test" || record.Path != "/account" || record.HostOnly || !record.Secure || !record.HTTPOnly || record.SameSite != http.SameSiteLaxMode || record.ExpiresUnix <= time.Now().Unix() {
		t.Fatalf("stored cookie attributes = %#v", record)
	}

	restored := newPersistentCookieJar()
	restored.Restore(records)
	secureURL, _ := url.Parse("https://api.example.test/account/profile")
	if got := restored.Cookies(secureURL); len(got) != 1 || got[0].Value != "secret" {
		t.Fatalf("restored secure cookies = %#v", got)
	}
	insecureURL, _ := url.Parse("http://api.example.test/account/profile")
	if got := restored.Cookies(insecureURL); len(got) != 0 {
		t.Fatalf("secure cookie sent over HTTP = %#v", got)
	}
	restored.Delete(record)
	if got := restored.Snapshot(); len(got) != 0 {
		t.Fatalf("deleted cookies = %#v", got)
	}
}

func TestPersistentCookieJarExpiresAndReplacesCookies(t *testing.T) {
	jar := newPersistentCookieJar()
	target, _ := url.Parse("http://example.test/")
	jar.SetCookies(target, []*http.Cookie{{Name: "mode", Value: "one"}})
	jar.SetCookies(target, []*http.Cookie{{Name: "mode", Value: "two"}})
	if records := jar.Snapshot(); len(records) != 1 || records[0].Value != "two" || !records[0].HostOnly {
		t.Fatalf("replaced cookies = %#v", records)
	}
	jar.SetCookies(target, []*http.Cookie{{Name: "mode", MaxAge: -1}})
	if records := jar.Snapshot(); len(records) != 0 {
		t.Fatalf("expired cookies = %#v", records)
	}
}

func TestWorkspacePersistsCapturedCookieJar(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/login" {
			http.SetCookie(response, &http.Cookie{Name: "session", Value: "workspace-token", Path: "/", HttpOnly: true})
			return
		}
		cookie, err := request.Cookie("session")
		if err != nil {
			http.Error(response, "missing cookie", http.StatusUnauthorized)
			return
		}
		_, _ = response.Write([]byte(cookie.Value))
	}))
	defer server.Close()
	workspacePath := filepath.Join(t.TempDir(), "workspace.json")
	m, err := NewModelWithWorkspace(workspacePath)
	if err != nil {
		t.Fatal(err)
	}
	response, err := m.client.Get(server.URL + "/login")
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if err := m.SaveWorkspace(); err != nil {
		t.Fatal(err)
	}

	loaded, err := NewModelWithWorkspace(workspacePath)
	if err != nil {
		t.Fatal(err)
	}
	response, err = loaded.client.Get(server.URL + "/profile")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK || string(body) != "workspace-token" {
		t.Fatalf("restored cookie response = %d %q", response.StatusCode, body)
	}
}

func TestCookieSidebarDeletesStoredCookie(t *testing.T) {
	m := NewModel()
	if err := m.SetCookie("https://example.test/", "theme=dark; Path=/; Secure"); err != nil {
		t.Fatal(err)
	}
	m.sidebarMode = sidebarCookies
	m.handleHistoryKeys("d")
	if len(m.Cookies()) != 1 {
		t.Fatal("first d deleted cookie without confirmation")
	}
	m.handleHistoryKeys("d")
	if len(m.Cookies()) != 0 {
		t.Fatalf("cookie was not deleted: %#v", m.Cookies())
	}
}
