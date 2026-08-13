package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	zone "github.com/lrstanley/bubblezone/v2"
)

func TestSensitiveTablesMaskValuesUntilExplicitlyRevealed(t *testing.T) {
	tests := []struct {
		name  string
		table headersTable
		entry headerEntry
	}{
		{name: "cookie", table: newCookiesTable(), entry: headerEntry{key: "session", value: "cookie-secret"}},
		{name: "environment", table: newVariablesTable(), entry: headerEntry{key: "ordinary-name", value: "environment-secret"}},
		{name: "authorization header", table: newHeadersTable(), entry: headerEntry{key: "Authorization", value: "Bearer header-secret"}},
		{name: "custom token header", table: newHeadersTable(), entry: headerEntry{key: "X-Auth-Token", value: "custom-token-secret"}},
		{name: "API key query", table: newParamsTable(), entry: headerEntry{key: "api_key", value: "query-secret"}},
		{name: "password form field", table: newFormTable(), entry: headerEntry{key: "password", value: "form-secret"}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			table := test.table
			table.SetWidth(70)
			table.SetHeight(8)
			table.SetEntries([]headerEntry{test.entry})

			masked := stripANSI(table.View())
			if strings.Contains(masked, test.entry.value) || !strings.Contains(masked, "v:reveal secrets") {
				t.Fatalf("masked table exposed value or omitted hint:\n%s", masked)
			}

			table.UpdateNormal("v")
			revealed := stripANSI(table.View())
			if !strings.Contains(revealed, test.entry.value) || !strings.Contains(revealed, "v:hide secrets") {
				t.Fatalf("reveal toggle did not show value and hide hint:\n%s", revealed)
			}

			table.Blur()
			if remasked := stripANSI(table.View()); strings.Contains(remasked, test.entry.value) {
				t.Fatalf("blur did not remask table value:\n%s", remasked)
			}
		})
	}
}

func TestNonSensitiveHeaderRemainsVisible(t *testing.T) {
	table := newHeadersTable()
	table.SetWidth(70)
	table.SetHeight(8)
	table.SetEntries([]headerEntry{{key: "Accept", value: "application/json"}})
	if view := stripANSI(table.View()); !strings.Contains(view, "application/json") {
		t.Fatalf("ordinary header value was masked:\n%s", view)
	}
}

func TestCookieSidebarMasksStoredValues(t *testing.T) {
	initTestZones()
	m := NewModel()
	m.sidebarMode = sidebarCookies
	if err := m.SetCookie("https://x/path", "s=Z; Path=/"); err != nil {
		t.Fatal(err)
	}

	masked := stripANSI(m.viewHistory(10))
	if strings.Contains(masked, "s=Z") || !strings.Contains(masked, "v:reveal") {
		t.Fatalf("cookie sidebar exposed stored value:\n%s", masked)
	}
	m.handleHistoryKeys("v")
	if revealed := stripANSI(m.viewHistory(10)); !strings.Contains(revealed, "s=Z") || !strings.Contains(revealed, "v:hide") {
		t.Fatalf("cookie sidebar reveal toggle failed:\n%s", revealed)
	}
}

func TestCookieSidebarRevealRemasksWhenFocusLeaves(t *testing.T) {
	initTestZones()
	tests := []struct {
		name string
		move func(*testing.T, model) model
		want pane
	}{
		{
			name: "Tab",
			move: func(_ *testing.T, m model) model {
				updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
				return updated.(model)
			},
			want: paneURL,
		},
		{
			name: "Ctrl-L",
			move: func(_ *testing.T, m model) model {
				updated, _ := m.Update(tea.KeyPressMsg{Code: 'l', Mod: tea.ModCtrl})
				return updated.(model)
			},
			want: paneRequest,
		},
		{
			name: "mouse",
			move: func(t *testing.T, m model) model {
				t.Helper()
				_ = m.View()
				urlZone := zone.Get("url")
				if urlZone.IsZero() {
					t.Fatal("URL mouse zone was not rendered")
				}
				updated, _ := m.Update(tea.MouseReleaseMsg{X: urlZone.StartX, Y: urlZone.StartY, Button: tea.MouseLeft})
				return updated.(model)
			},
			want: paneURL,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			m := NewModel()
			updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
			m = updated.(model)
			m.sidebarMode = sidebarCookies
			if err := m.SetCookie("https://x/path", "s=Z; Path=/"); err != nil {
				t.Fatal(err)
			}
			m.setFocus(paneHistory)
			m.handleHistoryKeys("v")
			if !m.cookieSecretsVisible {
				t.Fatal("cookie values were not revealed before focus changed")
			}
			if revealed := stripANSI(m.viewHistory(10)); !strings.Contains(revealed, "s=Z") {
				t.Fatalf("cookie value was not visible after reveal:\n%s", revealed)
			}

			m = test.move(t, m)
			if m.focus != test.want {
				t.Fatalf("focus = %d, want %d", m.focus, test.want)
			}
			if m.cookieSecretsVisible {
				t.Fatal("cookie values remained revealed after sidebar lost focus")
			}
			if remasked := stripANSI(m.viewHistory(10)); strings.Contains(remasked, "s=Z") {
				t.Fatalf("cookie value remained visible after sidebar lost focus:\n%s", remasked)
			}
		})
	}
}

func TestCookieSidebarRevealPersistsWhileSidebarKeepsFocus(t *testing.T) {
	m := NewModel()
	m.sidebarMode = sidebarCookies
	if err := m.SetCookie("https://x/path", "s=Z; Path=/"); err != nil {
		t.Fatal(err)
	}
	m.setFocus(paneHistory)
	m.handleHistoryKeys("v")
	m.setFocus(paneHistory)
	if !m.cookieSecretsVisible {
		t.Fatal("cookie values were remasked without leaving the sidebar")
	}
	if revealed := stripANSI(m.viewHistory(10)); !strings.Contains(revealed, "s=Z") {
		t.Fatalf("cookie value was hidden while sidebar retained focus:\n%s", revealed)
	}
}

func TestSettingsMasksProxyCredentialsAndPFXPassword(t *testing.T) {
	pane := newSettingsPane()
	pane.SetWidth(100)
	pane.SetConfig(requestSettings{
		timeout:  30,
		proxyURL: "http://proxy-user:proxy-password@example.test:8080",
	})

	masked := stripANSI(pane.View())
	if strings.Contains(masked, "proxy-user") || strings.Contains(masked, "proxy-password") || !strings.Contains(masked, "example.test:8080") {
		t.Fatalf("masked proxy view leaked credentials or hid its endpoint:\n%s", masked)
	}
	pane.UpdateNormal("v")
	if revealed := stripANSI(pane.View()); !strings.Contains(revealed, "proxy-user:proxy-password") {
		t.Fatalf("proxy reveal toggle failed:\n%s", revealed)
	}

	pane.page = settingsTLS
	pane.clientPFXPasswordInput.SetValue("pfx-secret")
	pane.setSecretsVisible(false)
	if maskedPFX := stripANSI(pane.View()); strings.Contains(maskedPFX, "pfx-secret") {
		t.Fatalf("PFX password was exposed:\n%s", maskedPFX)
	}
}

func TestSettingsRevealRemasksWhenFocusLeavesRequestPane(t *testing.T) {
	initTestZones()
	tests := []struct {
		name string
		move func(*testing.T, model) model
		want pane
	}{
		{
			name: "focus transition",
			move: func(_ *testing.T, m model) model {
				m.setFocus(paneResponse)
				return m
			},
			want: paneResponse,
		},
		{
			name: "mouse",
			move: func(t *testing.T, m model) model {
				t.Helper()
				_ = m.View()
				urlZone := zone.Get("url")
				if urlZone.IsZero() {
					t.Fatal("URL mouse zone was not rendered")
				}
				updated, _ := m.Update(tea.MouseReleaseMsg{X: urlZone.StartX, Y: urlZone.StartY, Button: tea.MouseLeft})
				return updated.(model)
			},
			want: paneURL,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			m := NewModel()
			updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
			m = updated.(model)
			m.settings.SetConfig(requestSettings{
				timeout:  30,
				proxyURL: "http://proxy-user:proxy-password@example.test:8080",
			})
			m.settings.clientPFXPasswordInput.SetValue("pfx-secret")
			m.settingsOpen = true
			m.setFocus(paneRequest)
			m.settings.UpdateNormal("v")
			if !m.settings.secretsVisible {
				t.Fatal("settings credentials were not revealed before focus changed")
			}
			if revealed := stripANSI(m.settings.View()); !strings.Contains(revealed, "proxy-user:proxy-password") {
				t.Fatalf("proxy credentials were not visible after reveal:\n%s", revealed)
			}

			m = test.move(t, m)
			if m.focus != test.want {
				t.Fatalf("focus = %d, want %d", m.focus, test.want)
			}
			if !m.settingsOpen {
				t.Fatal("settings unexpectedly closed when focus changed")
			}
			if m.settings.secretsVisible {
				t.Fatal("settings credentials remained revealed after focus left the request pane")
			}
			if remasked := stripANSI(m.settings.View()); strings.Contains(remasked, "proxy-user") || strings.Contains(remasked, "proxy-password") {
				t.Fatalf("proxy credentials remained visible after focus changed:\n%s", remasked)
			}
			m.settings.page = settingsTLS
			if remasked := stripANSI(m.settings.View()); strings.Contains(remasked, "pfx-secret") {
				t.Fatalf("PFX password remained visible after focus changed:\n%s", remasked)
			}
		})
	}
}
