package tui

import (
	"fmt"
	"strings"
)

// HTTPieCommand renders a saved request as a copyable POSIX-shell HTTPie command.
func HTTPieCommand(request savedRequest) (string, error) {
	urlValue := requestCommandURL(request)
	if _, matched, err := parseUnixSocketURL(urlValue); err != nil {
		return "", err
	} else if matched {
		return "", fmt.Errorf("HTTPie export does not support Courier Unix-socket URLs; use cURL export")
	}
	parts := []string{"http", shellQuote(request.method), shellQuote(urlValue)}
	for _, header := range request.headers {
		parts = append(parts, shellQuote(header.key+":"+header.value))
	}
	switch request.auth.typeID {
	case authBearer:
		parts = append(parts, shellQuote("Authorization:Bearer "+request.auth.bearerToken))
	case authJWTBearer:
		if request.auth.jwtLocation == apiKeyHeader {
			token := "{{courier_jwt_token}}"
			if prefix := strings.TrimSpace(request.auth.jwtPrefix); prefix != "" {
				token = prefix + " " + token
			}
			parts = append(parts, shellQuote("Authorization:"+token))
		}
	case authBasic:
		parts = append(parts, "--auth", shellQuote(request.auth.username+":"+request.auth.password))
	case authDigest:
		parts = append(parts, "--auth-type=digest", "--auth", shellQuote(request.auth.username+":"+request.auth.password))
	case authAPIKey:
		if request.auth.apiKeyLocation == apiKeyHeader {
			parts = append(parts, shellQuote(request.auth.apiKeyName+":"+request.auth.apiKeyValue))
		}
	case authAWSSignatureV4:
		parts = append(parts, shellQuote("Authorization:{{aws_sigv4_authorization}}"))
		if request.auth.awsSessionToken != "" {
			parts = append(parts, shellQuote("X-Amz-Security-Token:"+request.auth.awsSessionToken))
		}
	case authOAuth2ClientCredentials, authOAuth2Password, authOAuth2RefreshToken, authOAuth2AuthorizationCode:
		parts = append(parts, shellQuote("Authorization:Bearer {{oauth2_access_token}}"))
	case authHawk:
		parts = append(parts, shellQuote("Authorization:{{hawk_authorization}}"))
	case authNTLM:
		parts = append(parts, shellQuote("Authorization:{{ntlm_authorization}}"))
	case authOAuth1:
		if request.auth.oauth1Location == apiKeyHeader {
			parts = append(parts, shellQuote("Authorization:{{oauth1_authorization}}"))
		}
	}
	if len(request.cookies) > 0 {
		cookies := make([]string, 0, len(request.cookies))
		for _, cookie := range request.cookies {
			cookies = append(cookies, cookie.key+"="+cookie.value)
		}
		parts = append(parts, shellQuote("Cookie:"+strings.Join(cookies, "; ")))
	}
	switch request.body.mode {
	case bodyRaw:
		if !hasHeader(request.headers, "Content-Type") {
			parts = append(parts, shellQuote("Content-Type:"+rawContentType(request.body.rawType)))
		}
		parts = append(parts, "--raw", shellQuote(request.body.raw))
	case bodyFormURLEncoded:
		parts = append(parts, "--form")
		for _, field := range request.body.form {
			parts = append(parts, shellQuote(field.key+"="+field.value))
		}
		if oauth1UsesFormBody(request) {
			parts = append(parts, shellQuote("{{oauth1_form_parameters}}"))
		}
	case bodyMultipart:
		parts = append(parts, "--form")
		for _, field := range request.body.multipart {
			if strings.HasPrefix(field.value, "@") && !strings.HasPrefix(field.value, "@@") {
				parts = append(parts, shellQuote(field.key+field.value))
			} else {
				parts = append(parts, shellQuote(field.key+"="+strings.TrimPrefix(field.value, "@")))
			}
		}
	case bodyBinary:
		parts = append(parts, "<", shellQuote(request.body.binaryPath))
	case bodyGraphQL:
		payload, err := buildGraphQLPayload(request.body.graphqlQuery, request.body.graphqlVariables, request.body.graphqlOperationName, newVariableResolver(nil))
		if err != nil {
			return "", err
		}
		if !hasHeader(request.headers, "Content-Type") {
			parts = append(parts, "Content-Type:application/json")
		}
		parts = append(parts, "--raw", shellQuote(string(payload)))
	}
	return strings.Join(parts, " "), nil
}

func (m *model) ExportSavedHTTPie(selector string) (string, error) {
	request, err := ParseSavedRequestSelector(selector, m.savedRequests)
	if err != nil {
		return "", err
	}
	return HTTPieCommand(request)
}
