package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/google/uuid"
)

func TestCollectionNavigationDoesNotReplaceRequestDraft(t *testing.T) {
	m := NewModel()
	m.savedRequests = []savedRequest{
		{method: "GET", url: "https://api.example.test/first"},
		{method: "POST", url: "https://api.example.test/second"},
	}
	m.sidebarMode = sidebarCollections
	m.urlInput.SetValue("https://draft.example.test")
	m.setFocus(paneHistory)

	m.handleHistoryKeys("down")

	if m.collectionPos != 1 {
		t.Fatalf("collection position = %d, want 1", m.collectionPos)
	}
	if got := m.urlInput.Value(); got != "https://draft.example.test" {
		t.Fatalf("navigation replaced draft URL with %q", got)
	}
	if !m.requestDraftDirty() {
		t.Fatal("edited request was not marked dirty")
	}
	if view := stripANSI(m.viewURL(80)); !strings.Contains(view, "GET*") {
		t.Fatalf("URL bar does not show dirty marker:\n%s", view)
	}
}

func TestLoadingSelectionConfirmsBeforeDiscardingDirtyDraft(t *testing.T) {
	m := NewModel()
	m.width, m.height = 100, 30
	m.savedRequests = []savedRequest{
		{method: "GET", url: "https://api.example.test/first"},
		{method: "POST", url: "https://api.example.test/second"},
	}
	m.sidebarMode = sidebarCollections
	m.applyRequestLoad(requestLoadTarget{kind: requestLoadCollection, index: 0})
	m.urlInput.SetValue("https://draft.example.test")
	m.collectionPos = 1
	m.setFocus(paneHistory)

	updated, command := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(model)
	if command != nil || !m.requestLoadConfirmOpen {
		t.Fatal("dirty draft did not open a discard confirmation")
	}
	if got := m.urlInput.Value(); got != "https://draft.example.test" {
		t.Fatalf("opening confirmation replaced draft URL with %q", got)
	}
	if view := stripANSI(m.View().Content); !strings.Contains(view, "Discard unsaved request changes?") {
		t.Fatalf("discard confirmation is missing from view:\n%s", view)
	}

	updated, command = m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	m = updated.(model)
	if command != nil || m.requestLoadConfirmOpen || m.urlInput.Value() != "https://draft.example.test" {
		t.Fatal("Escape did not preserve the dirty draft")
	}

	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(model)
	updated, command = m.Update(tea.KeyPressMsg{Code: 'y', Text: "y"})
	m = updated.(model)
	if command != nil || m.requestLoadConfirmOpen {
		t.Fatal("confirming discard left the modal open")
	}
	if got := m.urlInput.Value(); got != "https://api.example.test/second" {
		t.Fatalf("confirmed load URL = %q", got)
	}
	if m.activeSavedIndex != 1 || m.focus != paneURL || m.requestDraftDirty() {
		t.Fatalf("confirmed load state = active %d focus %d dirty %v", m.activeSavedIndex, m.focus, m.requestDraftDirty())
	}
}

func TestExampleNavigationDoesNotReplaceRequestDraft(t *testing.T) {
	m := NewModel()
	m.savedRequests = []savedRequest{{
		method: "GET",
		url:    "https://api.example.test",
		examples: []savedExample{
			{name: "first", responseBody: "one"},
			{name: "second", responseBody: "two"},
		},
	}}
	m.sidebarMode = sidebarExamples
	m.urlInput.SetValue("https://draft.example.test")

	m.handleHistoryKeys("down")

	if m.examplePos != 1 || m.urlInput.Value() != "https://draft.example.test" || m.response != "" {
		t.Fatalf("example navigation loaded content: pos=%d url=%q response=%q", m.examplePos, m.urlInput.Value(), m.response)
	}
}

func TestOAuthTokenSavePreservesUnrelatedDirtyDraft(t *testing.T) {
	auth := authConfig{
		typeID:                authOAuth2AuthorizationCode,
		oauthAuthorizationURL: "https://identity.example.test/authorize",
		oauthTokenURL:         "https://identity.example.test/token",
		oauthClientID:         "courier",
	}
	m := NewModel()
	m.savedRequests = []savedRequest{{
		name:   "Protected request",
		method: "GET",
		url:    "https://api.example.test/original",
		auth:   auth,
	}}
	m.applyRequestLoad(requestLoadTarget{kind: requestLoadCollection, index: 0})
	m.urlInput.SetValue("https://api.example.test/edited")
	m.headersInput.SetEntries([]headerEntry{{key: "X-Draft", value: "unsaved"}})
	if !m.requestDraftDirty() {
		t.Fatal("request edits were not dirty before OAuth completion")
	}

	loginID := uuid.New()
	m.oauthLoginID = loginID
	updated, _ := m.Update(oauthLoginMsg{
		id: loginID,
		token: oauth2IssuedToken{
			accessToken:  "issued-access-token",
			tokenType:    "Bearer",
			refreshToken: "issued-refresh-token",
			expiresAt:    1_900_000_000,
		},
	})
	m = updated.(model)

	if !m.requestDraftDirty() {
		t.Fatal("OAuth token save cleared unrelated request edits")
	}
	if got := m.urlInput.Value(); got != "https://api.example.test/edited" {
		t.Fatalf("draft URL = %q, want edited URL", got)
	}
	if got := m.savedRequests[0].url; got != "https://api.example.test/original" {
		t.Fatalf("saved URL = %q, want original URL", got)
	}
	savedAuth := m.savedRequests[0].auth
	if savedAuth.oauthAccessToken != "issued-access-token" || savedAuth.oauthRefreshToken != "issued-refresh-token" {
		t.Fatalf("saved OAuth tokens = %#v", savedAuth)
	}
	if m.requestDraftBaseline.auth != savedAuth {
		t.Fatal("saved OAuth auth was not incorporated into the clean baseline")
	}
	if m.requestDraftBaseline.url != "https://api.example.test/original" {
		t.Fatalf("baseline URL = %q, want original URL", m.requestDraftBaseline.url)
	}
}
