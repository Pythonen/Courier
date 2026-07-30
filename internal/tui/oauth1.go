package tui

import (
	"bytes"
	"crypto"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha1" //nolint:gosec // OAuth 1.0 interoperability requires HMAC/RSA-SHA1.
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

type oauth1Pair struct{ key, value string }

func authorizeOAuth1(request *http.Request, payload []byte, config authConfig, now time.Time, nonce string) error {
	if strings.TrimSpace(config.oauth1ConsumerKey) == "" {
		return fmt.Errorf("OAuth 1.0 consumer key is required")
	}
	algorithm := strings.ToUpper(strings.TrimSpace(config.oauth1SignatureMethod))
	if algorithm == "" {
		algorithm = "HMAC-SHA1"
	}
	switch algorithm {
	case "HMAC-SHA1", "HMAC-SHA256", "RSA-SHA1", "PLAINTEXT":
	default:
		return fmt.Errorf("OAuth 1.0 signature method must be HMAC-SHA1, HMAC-SHA256, RSA-SHA1, or PLAINTEXT")
	}
	if nonce == "" {
		value := make([]byte, 12)
		if _, err := rand.Read(value); err != nil {
			return fmt.Errorf("generate OAuth 1.0 nonce: %w", err)
		}
		nonce = base64.RawURLEncoding.EncodeToString(value)
	}
	oauthParameters := []oauth1Pair{
		{key: "oauth_consumer_key", value: config.oauth1ConsumerKey},
		{key: "oauth_nonce", value: nonce},
		{key: "oauth_signature_method", value: algorithm},
		{key: "oauth_timestamp", value: strconv.FormatInt(now.Unix(), 10)},
	}
	if config.oauth1Token != "" {
		oauthParameters = append(oauthParameters, oauth1Pair{key: "oauth_token", value: config.oauth1Token})
	}
	if config.oauth1Callback != "" {
		oauthParameters = append(oauthParameters, oauth1Pair{key: "oauth_callback", value: config.oauth1Callback})
	}
	if config.oauth1Verifier != "" {
		oauthParameters = append(oauthParameters, oauth1Pair{key: "oauth_verifier", value: config.oauth1Verifier})
	}
	contentType := request.Header.Get("Content-Type")
	if base, _, ok := strings.Cut(contentType, ";"); ok {
		contentType = base
	}
	isFormBody := strings.EqualFold(strings.TrimSpace(contentType), "application/x-www-form-urlencoded")
	if config.oauth1IncludeBodyHash && len(payload) > 0 && !isFormBody {
		digest := sha1.Sum(payload) //nolint:gosec // The OAuth body-hash extension specifies SHA-1.
		oauthParameters = append(oauthParameters, oauth1Pair{key: "oauth_body_hash", value: base64.StdEncoding.EncodeToString(digest[:])})
	}
	parameters := append([]oauth1Pair(nil), oauthParameters...)
	managed := map[string]bool{
		"oauth_body_hash": true, "oauth_callback": true, "oauth_consumer_key": true, "oauth_nonce": true,
		"oauth_signature": true, "oauth_signature_method": true, "oauth_timestamp": true,
		"oauth_token": true, "oauth_verifier": true, "oauth_version": true,
	}
	for key, values := range request.URL.Query() {
		if managed[key] {
			continue
		}
		for _, value := range values {
			parameters = append(parameters, oauth1Pair{key: key, value: value})
		}
	}
	var bodyValues url.Values
	if isFormBody && payload != nil {
		var err error
		bodyValues, err = url.ParseQuery(string(payload))
		if err != nil {
			return fmt.Errorf("parse OAuth 1.0 form body: %w", err)
		}
		for key, values := range bodyValues {
			for _, value := range values {
				parameters = append(parameters, oauth1Pair{key: key, value: value})
			}
		}
	}
	signature, err := oauth1Signature(request, parameters, config, algorithm)
	if err != nil {
		return err
	}
	oauthParameters = append(oauthParameters, oauth1Pair{key: "oauth_signature", value: signature})
	if config.oauth1Location == apiKeyQuery {
		if isFormBody && (request.Method == http.MethodPost || request.Method == http.MethodPut) {
			if bodyValues == nil {
				bodyValues = make(url.Values)
			}
			for key := range managed {
				bodyValues.Del(key)
			}
			for _, parameter := range oauthParameters {
				bodyValues.Set(parameter.key, parameter.value)
			}
			setOAuth1RequestBody(request, []byte(bodyValues.Encode()))
			return nil
		}
		query := request.URL.Query()
		for _, parameter := range oauthParameters {
			query.Set(parameter.key, parameter.value)
		}
		request.URL.RawQuery = query.Encode()
		return nil
	}
	sort.Slice(oauthParameters, func(i, j int) bool { return oauthParameters[i].key < oauthParameters[j].key })
	attributes := make([]string, 0, len(oauthParameters)+1)
	if config.oauth1Realm != "" {
		attributes = append(attributes, `realm="`+oauth1Encode(config.oauth1Realm)+`"`)
	}
	for _, parameter := range oauthParameters {
		attributes = append(attributes, oauth1Encode(parameter.key)+`="`+oauth1Encode(parameter.value)+`"`)
	}
	request.Header.Set("Authorization", "OAuth "+strings.Join(attributes, ", "))
	return nil
}

func setOAuth1RequestBody(request *http.Request, payload []byte) {
	request.Body = io.NopCloser(bytes.NewReader(payload))
	request.ContentLength = int64(len(payload))
	request.GetBody = func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(payload)), nil
	}
}

func oauth1Signature(request *http.Request, parameters []oauth1Pair, config authConfig, algorithm string) (string, error) {
	key := oauth1Encode(config.oauth1ConsumerSecret) + "&" + oauth1Encode(config.oauth1TokenSecret)
	if algorithm == "PLAINTEXT" {
		return key, nil
	}
	encoded := make([]oauth1Pair, len(parameters))
	for index, parameter := range parameters {
		encoded[index] = oauth1Pair{key: oauth1Encode(parameter.key), value: oauth1Encode(parameter.value)}
	}
	sort.Slice(encoded, func(i, j int) bool {
		return encoded[i].key < encoded[j].key || (encoded[i].key == encoded[j].key && encoded[i].value < encoded[j].value)
	})
	normalized := make([]string, len(encoded))
	for index, parameter := range encoded {
		normalized[index] = parameter.key + "=" + parameter.value
	}
	baseURL := *request.URL
	baseURL.RawQuery, baseURL.Fragment, baseURL.RawFragment = "", "", ""
	baseURL.User = nil
	baseURL.Scheme = strings.ToLower(baseURL.Scheme)
	switch baseURL.Scheme {
	case "ws":
		baseURL.Scheme = "http"
	case "wss":
		baseURL.Scheme = "https"
	}
	if request.Host != "" {
		baseURL.Host = request.Host
	}
	hostURL := &url.URL{Host: baseURL.Host}
	host := strings.ToLower(hostURL.Hostname())
	port := hostURL.Port()
	isDefaultPort := baseURL.Scheme == "http" && port == "80" || baseURL.Scheme == "https" && port == "443"
	if port != "" && !isDefaultPort {
		host = net.JoinHostPort(host, port)
	} else if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	baseURL.Host = host
	if baseURL.Path == "" {
		baseURL.Path = "/"
	}
	baseString := strings.ToUpper(request.Method) + "&" + oauth1Encode(baseURL.String()) + "&" + oauth1Encode(strings.Join(normalized, "&"))
	var signature []byte
	switch algorithm {
	case "HMAC-SHA1":
		mac := hmac.New(sha1.New, []byte(key)) //nolint:gosec // OAuth 1.0 compatibility.
		_, _ = mac.Write([]byte(baseString))
		signature = mac.Sum(nil)
	case "HMAC-SHA256":
		mac := hmac.New(sha256.New, []byte(key))
		_, _ = mac.Write([]byte(baseString))
		signature = mac.Sum(nil)
	case "RSA-SHA1":
		keyData, err := jwtPrivateKeyData(config.oauth1PrivateKey)
		if err != nil {
			return "", fmt.Errorf("read OAuth 1.0 private key: %w", err)
		}
		privateKey, err := parseJWTPrivateKey(keyData)
		if err != nil {
			return "", fmt.Errorf("parse OAuth 1.0 private key: %w", err)
		}
		rsaKey, ok := privateKey.(*rsa.PrivateKey)
		if !ok {
			return "", fmt.Errorf("OAuth 1.0 RSA-SHA1 requires an RSA private key")
		}
		digest := sha1.Sum([]byte(baseString))                                         //nolint:gosec // OAuth 1.0 compatibility.
		signature, err = rsa.SignPKCS1v15(rand.Reader, rsaKey, crypto.SHA1, digest[:]) //nolint:gosec // OAuth 1.0 compatibility.
		if err != nil {
			return "", fmt.Errorf("sign OAuth 1.0 request: %w", err)
		}
	}
	return base64.StdEncoding.EncodeToString(signature), nil
}

func oauth1Encode(value string) string {
	return strings.ReplaceAll(url.QueryEscape(value), "+", "%20")
}
