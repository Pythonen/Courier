package tui

import (
	"crypto/md5" //nolint:gosec // MD5 is required for RFC 7616 compatibility with legacy Digest servers.
	"crypto/rand"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"
)

func digestAuthorization(request *http.Request, auth authConfig, challenges []string) (string, error) {
	parameters, err := parseDigestChallenge(challenges)
	if err != nil {
		return "", err
	}
	realm := parameters["realm"]
	nonce := parameters["nonce"]
	if realm == "" || nonce == "" {
		return "", fmt.Errorf("digest challenge must include realm and nonce")
	}
	algorithm := strings.ToUpper(strings.TrimSpace(parameters["algorithm"]))
	if algorithm == "" {
		algorithm = "MD5"
	}
	hash, session, err := digestHash(algorithm)
	if err != nil {
		return "", err
	}

	qop := ""
	if advertised := parameters["qop"]; advertised != "" {
		for option := range strings.SplitSeq(advertised, ",") {
			if strings.EqualFold(strings.TrimSpace(option), "auth") {
				qop = "auth"
				break
			}
		}
		if qop == "" {
			return "", fmt.Errorf("digest challenge does not offer supported qop auth")
		}
	}

	cnonce := ""
	if qop != "" || session {
		cnonce, err = digestClientNonce()
		if err != nil {
			return "", err
		}
	}
	username := auth.username
	if strings.EqualFold(parameters["userhash"], "true") {
		username = hash(auth.username + ":" + realm)
	}
	ha1 := hash(auth.username + ":" + realm + ":" + auth.password)
	if session {
		ha1 = hash(ha1 + ":" + nonce + ":" + cnonce)
	}
	uri := request.URL.RequestURI()
	ha2 := hash(request.Method + ":" + uri)
	nonceCount := "00000001"
	response := ""
	if qop == "" {
		response = hash(ha1 + ":" + nonce + ":" + ha2)
	} else {
		response = hash(ha1 + ":" + nonce + ":" + nonceCount + ":" + cnonce + ":" + qop + ":" + ha2)
	}

	fields := []string{
		`username="` + quoteDigestValue(username) + `"`,
		`realm="` + quoteDigestValue(realm) + `"`,
		`nonce="` + quoteDigestValue(nonce) + `"`,
		`uri="` + quoteDigestValue(uri) + `"`,
		`response="` + response + `"`,
		"algorithm=" + algorithm,
	}
	if opaque := parameters["opaque"]; opaque != "" {
		fields = append(fields, `opaque="`+quoteDigestValue(opaque)+`"`)
	}
	if qop != "" {
		fields = append(fields, "qop="+qop, "nc="+nonceCount, `cnonce="`+cnonce+`"`)
	} else if session {
		fields = append(fields, `cnonce="`+cnonce+`"`)
	}
	if strings.EqualFold(parameters["userhash"], "true") {
		fields = append(fields, "userhash=true")
	}
	return "Digest " + strings.Join(fields, ", "), nil
}

func parseDigestChallenge(challenges []string) (map[string]string, error) {
	for _, challenge := range challenges {
		candidate := strings.TrimSpace(challenge)
		lower := strings.ToLower(candidate)
		if !strings.HasPrefix(lower, "digest ") {
			marker := strings.Index(lower, ", digest ")
			if marker < 0 {
				continue
			}
			candidate = strings.TrimSpace(candidate[marker+2:])
		}
		candidate = strings.TrimSpace(candidate[len("Digest"):])
		parameters, err := parseDigestParameters(candidate)
		if err != nil {
			return nil, fmt.Errorf("parse Digest challenge: %w", err)
		}
		return parameters, nil
	}
	return nil, fmt.Errorf("server did not provide a Digest authentication challenge")
}

func parseDigestParameters(input string) (map[string]string, error) {
	parameters := make(map[string]string)
	for position := 0; position < len(input); {
		for position < len(input) && (input[position] == ' ' || input[position] == '\t' || input[position] == ',') {
			position++
		}
		if position >= len(input) {
			break
		}
		keyStart := position
		for position < len(input) && input[position] != '=' && input[position] != ',' {
			position++
		}
		if position >= len(input) || input[position] != '=' {
			return nil, fmt.Errorf("invalid parameter near %q", input[keyStart:])
		}
		key := strings.ToLower(strings.TrimSpace(input[keyStart:position]))
		if key == "" {
			return nil, fmt.Errorf("empty parameter name")
		}
		position++
		for position < len(input) && (input[position] == ' ' || input[position] == '\t') {
			position++
		}
		value := ""
		if position < len(input) && input[position] == '"' {
			position++
			var builder strings.Builder
			closed := false
			for position < len(input) {
				character := input[position]
				position++
				if character == '\\' && position < len(input) {
					builder.WriteByte(input[position])
					position++
					continue
				}
				if character == '"' {
					closed = true
					break
				}
				builder.WriteByte(character)
			}
			if !closed {
				return nil, fmt.Errorf("unterminated quoted value for %s", key)
			}
			value = builder.String()
		} else {
			valueStart := position
			for position < len(input) && input[position] != ',' {
				position++
			}
			value = strings.TrimSpace(input[valueStart:position])
		}
		parameters[key] = value
	}
	return parameters, nil
}

func digestHash(algorithm string) (func(string) string, bool, error) {
	session := strings.HasSuffix(algorithm, "-SESS")
	base := strings.TrimSuffix(algorithm, "-SESS")
	switch base {
	case "MD5":
		return func(value string) string {
			sum := md5.Sum([]byte(value)) //nolint:gosec // Required by the negotiated Digest algorithm.
			return hex.EncodeToString(sum[:])
		}, session, nil
	case "SHA-256":
		return func(value string) string {
			sum := sha256.Sum256([]byte(value))
			return hex.EncodeToString(sum[:])
		}, session, nil
	case "SHA-512-256":
		return func(value string) string {
			sum := sha512.Sum512_256([]byte(value))
			return hex.EncodeToString(sum[:])
		}, session, nil
	default:
		return nil, false, fmt.Errorf("unsupported Digest algorithm %q", algorithm)
	}
}

func digestClientNonce() (string, error) {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		return "", fmt.Errorf("generate Digest client nonce: %w", err)
	}
	return hex.EncodeToString(value), nil
}

func quoteDigestValue(value string) string {
	return strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(value)
}
