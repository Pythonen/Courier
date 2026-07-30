package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const maxOAuthTokenResponse = 1 << 20

func (c authConfig) authorize(ctx context.Context, client *http.Client, request *http.Request, payload []byte) error {
	if c.typeID == authAWSSignatureV4 {
		_, err := signAWSv4(request, payload, c, time.Now().UTC())
		return err
	}
	if c.typeID == authHawk {
		header, err := hawkAuthorization(request, payload, c, time.Now().UTC(), "")
		if err != nil {
			return err
		}
		request.Header.Set("Authorization", header)
		return nil
	}
	if c.typeID == authNTLM {
		applyNTLMCredentials(request, c)
		return nil
	}
	if c.typeID == authOAuth1 {
		return authorizeOAuth1(request, payload, c, time.Now().UTC(), "")
	}
	if c.typeID == authJWTBearer {
		if c.jwtLocation == apiKeyQuery {
			return nil
		}
		token, err := generateJWT(c)
		if err != nil {
			return err
		}
		prefix := strings.TrimSpace(c.jwtPrefix)
		if prefix != "" {
			token = prefix + " " + token
		}
		request.Header.Set("Authorization", token)
		return nil
	}
	if c.typeID == authOAuth2AuthorizationCode {
		if strings.TrimSpace(c.oauthAccessToken) == "" {
			return fmt.Errorf("OAuth 2 access token is missing; press g in the Authorization tab to authorize")
		}
		if c.oauthAccessTokenExpiry > 0 && time.Now().Unix() >= c.oauthAccessTokenExpiry {
			return fmt.Errorf("OAuth 2 access token has expired; press g in the Authorization tab to authorize again")
		}
		c.applyHeaders(request)
		return nil
	}
	if c.typeID != authOAuth2ClientCredentials && c.typeID != authOAuth2Password && c.typeID != authOAuth2RefreshToken {
		c.applyHeaders(request)
		return nil
	}
	token, tokenType, err := c.fetchOAuth2Token(ctx, client)
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", tokenType+" "+token)
	return nil
}

func (c authConfig) fetchOAuth2ClientCredentialsToken(ctx context.Context, client *http.Client) (string, string, error) {
	c.typeID = authOAuth2ClientCredentials
	return c.fetchOAuth2Token(ctx, client)
}

func (c authConfig) fetchOAuth2Token(ctx context.Context, client *http.Client) (string, string, error) {
	if strings.TrimSpace(c.oauthTokenURL) == "" {
		return "", "", fmt.Errorf("OAuth 2 token URL is required")
	}
	if c.typeID == authOAuth2ClientCredentials && strings.TrimSpace(c.oauthClientID) == "" {
		return "", "", fmt.Errorf("OAuth 2 client ID is required")
	}
	values := url.Values{}
	switch c.typeID {
	case authOAuth2Password:
		if strings.TrimSpace(c.username) == "" {
			return "", "", fmt.Errorf("OAuth 2 username is required")
		}
		values.Set("grant_type", "password")
		values.Set("username", c.username)
		values.Set("password", c.password)
	case authOAuth2RefreshToken:
		if strings.TrimSpace(c.oauthRefreshToken) == "" {
			return "", "", fmt.Errorf("OAuth 2 refresh token is required")
		}
		values.Set("grant_type", "refresh_token")
		values.Set("refresh_token", c.oauthRefreshToken)
	default:
		values.Set("grant_type", "client_credentials")
	}
	if scope := strings.TrimSpace(c.oauthScope); scope != "" {
		values.Set("scope", scope)
	}
	tokenRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, c.oauthTokenURL, strings.NewReader(values.Encode()))
	if err != nil {
		return "", "", fmt.Errorf("create OAuth 2 token request: %w", err)
	}
	tokenRequest.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	tokenRequest.Header.Set("Accept", "application/json")
	if c.oauthClientID != "" || c.oauthClientSecret != "" {
		tokenRequest.SetBasicAuth(c.oauthClientID, c.oauthClientSecret)
	}
	response, err := client.Do(tokenRequest)
	if err != nil {
		return "", "", fmt.Errorf("request OAuth 2 token: %w", err)
	}
	defer response.Body.Close() //nolint:errcheck
	body, err := io.ReadAll(io.LimitReader(response.Body, maxOAuthTokenResponse+1))
	if err != nil {
		return "", "", fmt.Errorf("read OAuth 2 token response: %w", err)
	}
	if len(body) > maxOAuthTokenResponse {
		return "", "", fmt.Errorf("OAuth 2 token response exceeds 1 MiB")
	}
	var payload struct {
		AccessToken      string `json:"access_token"`
		TokenType        string `json:"token_type"`
		Error            string `json:"error"`
		ErrorDescription string `json:"error_description"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return "", "", fmt.Errorf("decode OAuth 2 token response: %w", err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		detail := strings.TrimSpace(strings.Join([]string{payload.Error, payload.ErrorDescription}, ": "))
		if detail == ":" || detail == "" {
			detail = response.Status
		}
		return "", "", fmt.Errorf("OAuth 2 token request failed: %s", detail)
	}
	if strings.TrimSpace(payload.AccessToken) == "" {
		return "", "", fmt.Errorf("OAuth 2 token response did not include access_token")
	}
	if strings.ContainsAny(payload.AccessToken, "\r\n") {
		return "", "", fmt.Errorf("OAuth 2 token response contained an invalid access_token")
	}
	tokenType := strings.TrimSpace(payload.TokenType)
	if tokenType == "" {
		tokenType = "Bearer"
	}
	if strings.ContainsAny(tokenType, "\r\n") {
		return "", "", fmt.Errorf("OAuth 2 token response contained an invalid token_type")
	}
	return payload.AccessToken, tokenType, nil
}
