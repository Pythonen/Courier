package tui

import (
	"strings"
	"testing"
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
