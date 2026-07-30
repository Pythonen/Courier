package tui

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"
)

const awsV4Algorithm = "AWS4-HMAC-SHA256"

type awsV4SigningResult struct {
	CanonicalRequest string
	StringToSign     string
	SignedHeaders    string
	Signature        string
}

func signAWSv4(request *http.Request, payload []byte, auth authConfig, now time.Time) (awsV4SigningResult, error) {
	accessKey := strings.TrimSpace(auth.awsAccessKey)
	secretKey := strings.TrimSpace(auth.awsSecretKey)
	region := strings.ToLower(strings.TrimSpace(auth.awsRegion))
	service := strings.ToLower(strings.TrimSpace(auth.awsService))
	if accessKey == "" {
		return awsV4SigningResult{}, fmt.Errorf("aws access key ID is required")
	}
	if secretKey == "" {
		return awsV4SigningResult{}, fmt.Errorf("aws secret access key is required")
	}
	if region == "" {
		return awsV4SigningResult{}, fmt.Errorf("aws region is required")
	}
	if service == "" {
		return awsV4SigningResult{}, fmt.Errorf("aws service is required")
	}
	if request.URL == nil || request.URL.Host == "" {
		return awsV4SigningResult{}, fmt.Errorf("aws signing requires an absolute request URL")
	}

	now = now.UTC()
	amzDate := now.Format("20060102T150405Z")
	shortDate := now.Format("20060102")
	payloadHash := awsSHA256Hex(payload)
	request.Header.Del("Authorization")
	request.Header.Set("X-Amz-Date", amzDate)
	request.Header.Set("X-Amz-Content-Sha256", payloadHash)
	if token := strings.TrimSpace(auth.awsSessionToken); token != "" {
		request.Header.Set("X-Amz-Security-Token", token)
	} else {
		request.Header.Del("X-Amz-Security-Token")
	}

	canonicalHeaders, signedHeaders := awsCanonicalHeaders(request)
	canonicalRequest := strings.Join([]string{
		request.Method,
		awsCanonicalURI(request.URL.Path),
		awsCanonicalQuery(request.URL.Query()),
		canonicalHeaders,
		signedHeaders,
		payloadHash,
	}, "\n")
	credentialScope := strings.Join([]string{shortDate, region, service, "aws4_request"}, "/")
	stringToSign := strings.Join([]string{awsV4Algorithm, amzDate, credentialScope, awsSHA256Hex([]byte(canonicalRequest))}, "\n")
	dateKey := awsHMAC([]byte("AWS4"+secretKey), shortDate)
	regionKey := awsHMAC(dateKey, region)
	serviceKey := awsHMAC(regionKey, service)
	signingKey := awsHMAC(serviceKey, "aws4_request")
	signature := hex.EncodeToString(awsHMAC(signingKey, stringToSign))
	request.Header.Set("Authorization", awsV4Algorithm+" Credential="+accessKey+"/"+credentialScope+", SignedHeaders="+signedHeaders+", Signature="+signature)
	return awsV4SigningResult{CanonicalRequest: canonicalRequest, StringToSign: stringToSign, SignedHeaders: signedHeaders, Signature: signature}, nil
}

func awsCanonicalHeaders(request *http.Request) (string, string) {
	headers := map[string][]string{}
	host := request.Host
	if host == "" {
		host = request.URL.Host
	}
	headers["host"] = []string{host}
	for name, values := range request.Header {
		lowerName := strings.ToLower(strings.TrimSpace(name))
		if lowerName == "authorization" || (lowerName != "content-type" && lowerName != "content-md5" && !strings.HasPrefix(lowerName, "x-amz-")) {
			continue
		}
		headers[lowerName] = values
	}
	names := make([]string, 0, len(headers))
	for name := range headers {
		names = append(names, name)
	}
	sort.Strings(names)
	var canonical strings.Builder
	for _, name := range names {
		values := make([]string, len(headers[name]))
		for index, value := range headers[name] {
			values[index] = strings.Join(strings.Fields(value), " ")
		}
		canonical.WriteString(name)
		canonical.WriteByte(':')
		canonical.WriteString(strings.Join(values, ","))
		canonical.WriteByte('\n')
	}
	return canonical.String(), strings.Join(names, ";")
}

func awsCanonicalQuery(values map[string][]string) string {
	var pairs []string
	for key, entries := range values {
		if len(entries) == 0 {
			entries = []string{""}
		}
		for _, value := range entries {
			pairs = append(pairs, awsURIEncode(key, true)+"="+awsURIEncode(value, true))
		}
	}
	sort.Strings(pairs)
	return strings.Join(pairs, "&")
}

func awsCanonicalURI(path string) string {
	if path == "" {
		return "/"
	}
	return awsURIEncode(path, false)
}

func awsURIEncode(value string, encodeSlash bool) string {
	const hexadecimal = "0123456789ABCDEF"
	var encoded strings.Builder
	for index := 0; index < len(value); index++ {
		character := value[index]
		unreserved := character >= 'A' && character <= 'Z' || character >= 'a' && character <= 'z' || character >= '0' && character <= '9' || character == '-' || character == '.' || character == '_' || character == '~'
		if unreserved || character == '/' && !encodeSlash {
			encoded.WriteByte(character)
			continue
		}
		encoded.WriteByte('%')
		encoded.WriteByte(hexadecimal[character>>4])
		encoded.WriteByte(hexadecimal[character&0x0f])
	}
	return encoded.String()
}

func awsSHA256Hex(value []byte) string {
	sum := sha256.Sum256(value)
	return hex.EncodeToString(sum[:])
}

func awsHMAC(key []byte, value string) []byte {
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(value))
	return mac.Sum(nil)
}
