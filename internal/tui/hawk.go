package tui

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1" //nolint:gosec // Hawk interoperability explicitly supports SHA-1 credentials.
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"hash"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

func hawkAuthorization(request *http.Request, payload []byte, config authConfig, now time.Time, nonce string) (string, error) {
	if strings.TrimSpace(config.hawkID) == "" || strings.TrimSpace(config.hawkKey) == "" {
		return "", fmt.Errorf("hawk auth ID and key are required")
	}
	hashFactory, err := hawkHash(config.hawkAlgorithm)
	if err != nil {
		return "", err
	}
	if nonce == "" {
		nonce, err = newHawkNonce()
		if err != nil {
			return "", err
		}
	}
	if strings.ContainsAny(config.hawkID+config.hawkExt+nonce, "\r\n") {
		return "", fmt.Errorf("hawk fields cannot contain line breaks")
	}
	host := request.URL.Hostname()
	port := request.URL.Port()
	if request.Host != "" {
		host = request.Host
		if parsedHost, parsedPort, splitErr := net.SplitHostPort(request.Host); splitErr == nil {
			host, port = parsedHost, parsedPort
		}
	}
	if port == "" {
		if strings.EqualFold(request.URL.Scheme, "https") {
			port = "443"
		} else {
			port = "80"
		}
	}
	timestamp := strconv.FormatInt(now.Unix(), 10)
	payloadHash := ""
	if payload != nil {
		contentType := request.Header.Get("Content-Type")
		if base, _, ok := strings.Cut(contentType, ";"); ok {
			contentType = base
		}
		hasher := hashFactory()
		_, _ = fmt.Fprintf(hasher, "hawk.1.payload\n%s\n", strings.ToLower(strings.TrimSpace(contentType)))
		_, _ = hasher.Write(payload)
		_, _ = hasher.Write([]byte("\n"))
		payloadHash = base64.StdEncoding.EncodeToString(hasher.Sum(nil))
	}
	ext := strings.ReplaceAll(strings.ReplaceAll(config.hawkExt, "\\", "\\\\"), "\n", "\\n")
	normalized := strings.Join([]string{"hawk.1.header", timestamp, nonce, strings.ToUpper(request.Method), request.URL.RequestURI(), strings.ToLower(host), port, payloadHash, ext, ""}, "\n")
	mac := hmac.New(hashFactory, []byte(config.hawkKey))
	_, _ = mac.Write([]byte(normalized))
	attributes := []string{`id="` + hawkEscape(config.hawkID) + `"`, `ts="` + timestamp + `"`, `nonce="` + hawkEscape(nonce) + `"`}
	if payloadHash != "" {
		attributes = append(attributes, `hash="`+payloadHash+`"`)
	}
	if config.hawkExt != "" {
		attributes = append(attributes, `ext="`+hawkEscape(config.hawkExt)+`"`)
	}
	attributes = append(attributes, `mac="`+base64.StdEncoding.EncodeToString(mac.Sum(nil))+`"`)
	return "Hawk " + strings.Join(attributes, ", "), nil
}

func hawkHash(algorithm string) (func() hash.Hash, error) {
	switch strings.ToLower(strings.TrimSpace(algorithm)) {
	case "sha1":
		return sha1.New, nil //nolint:gosec // Required for existing Hawk credentials.
	case "", "sha256":
		return sha256.New, nil
	default:
		return nil, fmt.Errorf("hawk algorithm must be sha1 or sha256")
	}
}

func newHawkNonce() (string, error) {
	value := make([]byte, 6)
	if _, err := rand.Read(value); err != nil {
		return "", fmt.Errorf("generate Hawk nonce: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(value), nil
}

func hawkEscape(value string) string {
	return strings.NewReplacer("\\", "\\\\", `"`, `\"`).Replace(value)
}
