package tui

import (
	"strings"
	"testing"
)

func TestAuthPaneMasksSecretsAndSupportsExplicitReveal(t *testing.T) {
	tests := []struct {
		name    string
		config  authConfig
		secrets []string
	}{
		{
			name:    "bearer token",
			config:  authConfig{typeID: authBearer, bearerToken: "bearer-secret"},
			secrets: []string{"bearer-secret"},
		},
		{
			name:    "API key value",
			config:  authConfig{typeID: authAPIKey, apiKeyName: "X-Key", apiKeyValue: "api-secret"},
			secrets: []string{"api-secret"},
		},
		{
			name: "OAuth 1 credentials",
			config: authConfig{
				typeID: authOAuth1, oauth1ConsumerKey: "public-key", oauth1ConsumerSecret: "consumer-secret",
				oauth1Token: "oauth-token", oauth1TokenSecret: "token-secret", oauth1PrivateKey: "private-key",
			},
			secrets: []string{"consumer-secret", "oauth-token", "token-secret", "private-key"},
		},
		{
			name: "AWS credentials",
			config: authConfig{
				typeID: authAWSSignatureV4, awsAccessKey: "public-id", awsSecretKey: "aws-secret",
				awsRegion: "eu-north-1", awsService: "execute-api", awsSessionToken: "session-token",
			},
			secrets: []string{"aws-secret", "session-token"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			pane := newAuthPane()
			pane.SetWidth(100)
			pane.SetConfig(test.config)

			masked := stripANSI(pane.View())
			if !strings.Contains(masked, "v:reveal secrets") {
				t.Fatalf("masked view does not advertise reveal toggle:\n%s", masked)
			}
			for _, secret := range test.secrets {
				if strings.Contains(masked, secret) {
					t.Fatalf("masked view exposed %q:\n%s", secret, masked)
				}
			}

			pane.UpdateNormal("v")
			revealed := stripANSI(pane.View())
			if !strings.Contains(revealed, "v:hide secrets") {
				t.Fatalf("revealed view does not advertise hide toggle:\n%s", revealed)
			}
			for _, secret := range test.secrets {
				if !strings.Contains(revealed, secret) {
					t.Fatalf("reveal toggle did not show %q:\n%s", secret, revealed)
				}
			}

			pane.Blur()
			remasked := stripANSI(pane.View())
			for _, secret := range test.secrets {
				if strings.Contains(remasked, secret) {
					t.Fatalf("blur did not remask %q:\n%s", secret, remasked)
				}
			}
		})
	}
}

func TestAuthPaneViewportFollowsCursor(t *testing.T) {
	pane := newAuthPane()
	pane.SetWidth(100)
	pane.SetConfig(authConfig{
		typeID: authOAuth1, oauth1ConsumerKey: "consumer", oauth1ConsumerSecret: "secret",
		oauth1Token: "token", oauth1TokenSecret: "token-secret", oauth1PrivateKey: "private-key",
		oauth1Realm: "realm", oauth1Callback: "callback", oauth1Verifier: "verifier",
	})
	pane.Focus()

	top := stripANSI(pane.View(4))
	if !strings.Contains(top, "Consumer") || strings.Contains(top, "Verifier") {
		t.Fatalf("top of OAuth form is not visible at first cursor:\n%s", top)
	}
	if got := len(strings.Split(top, "\n")); got != 4 {
		t.Fatalf("top viewport height = %d, want 4", got)
	}

	pane.cursor = 7
	bottom := stripANSI(pane.View(4))
	if !strings.Contains(bottom, "Verifier") || !strings.Contains(bottom, "↑") || strings.Contains(bottom, "Consumer") {
		t.Fatalf("viewport did not follow the last OAuth field:\n%s", bottom)
	}
	if got := len(strings.Split(bottom, "\n")); got != 4 {
		t.Fatalf("bottom viewport height = %d, want 4", got)
	}
}

func TestSettingsPaneViewportFollowsCursor(t *testing.T) {
	pane := newSettingsPane()
	pane.SetWidth(100)
	pane.page = settingsTLS

	top := stripANSI(pane.View(4))
	if !strings.Contains(top, "Skip TLS verify") || strings.Contains(top, "PFX passphrase") {
		t.Fatalf("top of TLS form is not visible at first cursor:\n%s", top)
	}

	pane.cursor = 5
	bottom := stripANSI(pane.View(4))
	if !strings.Contains(bottom, "PFX passphrase") || !strings.Contains(bottom, "↑") || strings.Contains(bottom, "Skip TLS verify") {
		t.Fatalf("viewport did not follow the PFX passphrase field:\n%s", bottom)
	}
}

func TestRequestPaneUsesFormViewportHeight(t *testing.T) {
	initTestZones()
	m := NewModel()
	m.requestTab = requestTabAuth
	m.authInput.SetConfig(authConfig{
		typeID: authOAuth1, oauth1ConsumerKey: "consumer", oauth1ConsumerSecret: "secret",
		oauth1Token: "token", oauth1TokenSecret: "token-secret", oauth1PrivateKey: "private-key",
		oauth1Realm: "realm", oauth1Callback: "callback", oauth1Verifier: "verifier",
	})
	m.authInput.cursor = 7
	m.setFocus(paneRequest)

	view := stripANSI(m.viewRequest(70, 6))
	if !strings.Contains(view, "Verifier") || strings.Contains(view, "Consumer") {
		t.Fatalf("request pane clipped the active auth field instead of scrolling:\n%s", view)
	}
}
