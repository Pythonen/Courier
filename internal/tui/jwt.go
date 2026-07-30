package tui

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/sha512"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"hash"
	"math/big"
	"os"
	"strings"
)

var jwtAlgorithms = []string{"HS256", "HS384", "HS512", "RS256", "RS384", "RS512", "PS256", "PS384", "PS512", "ES256", "ES384", "ES512"}

func generateJWT(config authConfig) (string, error) {
	algorithm := strings.ToUpper(strings.TrimSpace(config.jwtAlgorithm))
	if !containsString(jwtAlgorithms, algorithm) {
		return "", fmt.Errorf("unsupported JWT algorithm %q", config.jwtAlgorithm)
	}
	header := map[string]any{"typ": "JWT"}
	if value := strings.TrimSpace(config.jwtHeaders); value != "" {
		if err := json.Unmarshal([]byte(value), &header); err != nil {
			return "", fmt.Errorf("decode JWT headers: %w", err)
		}
	}
	header["alg"] = algorithm
	payload := json.RawMessage(strings.TrimSpace(config.jwtPayload))
	if len(payload) == 0 {
		payload = json.RawMessage(`{}`)
	}
	var claims map[string]any
	if err := json.Unmarshal(payload, &claims); err != nil {
		return "", fmt.Errorf("decode JWT payload: %w", err)
	}
	headerJSON, err := json.Marshal(header)
	if err != nil {
		return "", fmt.Errorf("encode JWT headers: %w", err)
	}
	payloadJSON, err := json.Marshal(claims)
	if err != nil {
		return "", fmt.Errorf("encode JWT payload: %w", err)
	}
	encodedHeader := base64.RawURLEncoding.EncodeToString(headerJSON)
	encodedPayload := base64.RawURLEncoding.EncodeToString(payloadJSON)
	signingInput := encodedHeader + "." + encodedPayload
	signature, err := signJWT([]byte(signingInput), algorithm, config)
	if err != nil {
		return "", err
	}
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(signature), nil
}

func signJWT(signingInput []byte, algorithm string, config authConfig) ([]byte, error) {
	hashID, hashFactory, err := jwtHash(algorithm)
	if err != nil {
		return nil, err
	}
	digest := hashFactory()
	_, _ = digest.Write(signingInput)
	hashed := digest.Sum(nil)
	if strings.HasPrefix(algorithm, "HS") {
		secret := []byte(config.jwtKey)
		if config.jwtSecretBase64 {
			secret, err = decodeJWTSecret(config.jwtKey)
			if err != nil {
				return nil, err
			}
		}
		if len(secret) == 0 {
			return nil, fmt.Errorf("JWT secret is required")
		}
		mac := hmac.New(hashFactory, secret)
		_, _ = mac.Write(signingInput)
		return mac.Sum(nil), nil
	}

	keyData, err := jwtPrivateKeyData(config.jwtKey)
	if err != nil {
		return nil, err
	}
	privateKey, err := parseJWTPrivateKey(keyData)
	if err != nil {
		return nil, err
	}
	switch {
	case strings.HasPrefix(algorithm, "RS"):
		key, ok := privateKey.(*rsa.PrivateKey)
		if !ok {
			return nil, fmt.Errorf("JWT %s requires an RSA private key", algorithm)
		}
		return rsa.SignPKCS1v15(rand.Reader, key, hashID, hashed)
	case strings.HasPrefix(algorithm, "PS"):
		key, ok := privateKey.(*rsa.PrivateKey)
		if !ok {
			return nil, fmt.Errorf("JWT %s requires an RSA private key", algorithm)
		}
		return rsa.SignPSS(rand.Reader, key, hashID, hashed, &rsa.PSSOptions{SaltLength: rsa.PSSSaltLengthEqualsHash, Hash: hashID})
	case strings.HasPrefix(algorithm, "ES"):
		key, ok := privateKey.(*ecdsa.PrivateKey)
		if !ok {
			return nil, fmt.Errorf("JWT %s requires an EC private key", algorithm)
		}
		expectedBits := map[string]int{"ES256": 256, "ES384": 384, "ES512": 521}[algorithm]
		if key.Curve.Params().BitSize != expectedBits {
			return nil, fmt.Errorf("JWT %s requires a %d-bit EC private key", algorithm, expectedBits)
		}
		r, s, err := ecdsa.Sign(rand.Reader, key, hashed)
		if err != nil {
			return nil, fmt.Errorf("sign JWT: %w", err)
		}
		partLength := (key.Curve.Params().BitSize + 7) / 8
		return append(paddedBigInt(r, partLength), paddedBigInt(s, partLength)...), nil
	default:
		return nil, fmt.Errorf("unsupported JWT algorithm %q", algorithm)
	}
}

func jwtHash(algorithm string) (crypto.Hash, func() hash.Hash, error) {
	switch {
	case strings.HasSuffix(algorithm, "256"):
		return crypto.SHA256, sha256.New, nil
	case strings.HasSuffix(algorithm, "384"):
		return crypto.SHA384, sha512.New384, nil
	case strings.HasSuffix(algorithm, "512"):
		return crypto.SHA512, sha512.New, nil
	default:
		return 0, nil, fmt.Errorf("unsupported JWT algorithm %q", algorithm)
	}
}

func decodeJWTSecret(value string) ([]byte, error) {
	value = strings.TrimSpace(value)
	for _, encoding := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		if decoded, err := encoding.DecodeString(value); err == nil {
			return decoded, nil
		}
	}
	return nil, fmt.Errorf("decode base64 JWT secret")
}

func jwtPrivateKeyData(value string) ([]byte, error) {
	value = strings.TrimSpace(value)
	if strings.Contains(value, "-----BEGIN") {
		return []byte(value), nil
	}
	if value == "" {
		return nil, fmt.Errorf("JWT private key path is required")
	}
	data, err := os.ReadFile(value)
	if err != nil {
		return nil, fmt.Errorf("read JWT private key %q: %w", value, err)
	}
	return data, nil
}

func parseJWTPrivateKey(data []byte) (crypto.PrivateKey, error) {
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, fmt.Errorf("JWT private key is not PEM encoded")
	}
	if key, err := x509.ParsePKCS8PrivateKey(block.Bytes); err == nil {
		return key, nil
	}
	if key, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return key, nil
	}
	if key, err := x509.ParseECPrivateKey(block.Bytes); err == nil {
		return key, nil
	}
	return nil, fmt.Errorf("parse JWT private key: expected PKCS #8, PKCS #1, or SEC 1 PEM")
}

func paddedBigInt(value *big.Int, length int) []byte {
	result := make([]byte, length)
	bytes := value.Bytes()
	copy(result[length-len(bytes):], bytes)
	return result
}

func containsString(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}
