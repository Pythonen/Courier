package tui

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	uuid "github.com/google/uuid"
)

const maxResponseBody = 10 << 20 // 10 MiB

type responseMsg struct {
	requestID            uuid.UUID
	responseBody         string
	responseRaw          string
	responseRawAvailable bool
	responseHeaders      string
	responseMeta         string
	statusCode           int
	duration             time.Duration
	responseBytes        int
	finalURL             string
	assertionResults     []AssertionResult
	variableUpdates      []headerEntry
	stream               <-chan responseStreamMsg
}

func (m model) DoRequest() tea.Cmd {
	if isGRPCURL(m.urlInput.Value()) {
		return m.DoGRPCRequest()
	}
	return func() tea.Msg {
		requestID := m.requestId
		assertions := m.testsInput.Entries()
		method := m.displayedMethod()
		resolver := newVariableResolver(m.variablesInput.Entries())
		urlValue := resolver.Resolve(m.urlInput.Value())
		unixTarget, isUnixSocket, unixErr := parseUnixSocketURL(urlValue)
		if unixErr != nil {
			return responseMsg{requestID: requestID, responseBody: fmt.Sprintf("Error parsing URL: %v", unixErr), responseMeta: "Request failed", assertionResults: unavailableAssertionResults(assertions, "request did not produce a response")}
		}
		requestURL := urlValue
		if isUnixSocket {
			requestURL = unixTarget.requestURL
		}

		payload, contentType, hasBody, bodyErr := m.buildRequestBody(resolver)
		if bodyErr != nil {
			return responseMsg{requestID: requestID, responseBody: fmt.Sprintf("Error creating request body: %v", bodyErr), responseMeta: "Request failed", assertionResults: unavailableAssertionResults(assertions, "request did not produce a response")}
		}
		var bodyReader io.Reader
		if hasBody {
			bodyReader = bytes.NewReader(payload)
		}

		parsedURL, err := url.Parse(requestURL)
		if err != nil {
			return responseMsg{requestID: requestID, responseBody: fmt.Sprintf("Error parsing URL: %v", err), responseMeta: "Request failed", assertionResults: unavailableAssertionResults(assertions, "request did not produce a response")}
		}
		query := parsedURL.Query()
		for _, param := range m.paramsInput.Entries() {
			query.Add(resolver.Resolve(param.key), resolver.Resolve(param.value))
		}
		auth := m.authInput.Config().resolved(resolver)
		if authErr := auth.applyQuery(query); authErr != nil {
			return responseMsg{requestID: requestID, responseBody: fmt.Sprintf("Error authorizing request: %v", authErr), responseMeta: "Request failed", assertionResults: unavailableAssertionResults(assertions, "request did not produce a response")}
		}
		parsedURL.RawQuery = query.Encode()
		displayURL := parsedURL.String()
		if unixTarget != nil {
			displayURL = unixTarget.displayURL(parsedURL)
		}

		requestContext := m.requestContext
		if requestContext == nil {
			requestContext = context.Background()
		}
		req, err := http.NewRequestWithContext(requestContext, method, parsedURL.String(), bodyReader)
		if err != nil {
			return responseMsg{requestID: requestID, responseBody: fmt.Sprintf("Error creating request: %v", err), responseMeta: "Request failed", assertionResults: unavailableAssertionResults(assertions, "request did not produce a response")}
		}

		// Add preserves repeated header names entered on separate rows.
		for _, header := range m.headersInput.Entries() {
			name := resolver.Resolve(header.key)
			value := resolver.Resolve(header.value)
			if strings.EqualFold(strings.TrimSpace(name), "Host") {
				req.Host = value
				continue
			}
			req.Header.Add(name, value)
		}
		if contentType != "" && req.Header.Get("Content-Type") == "" {
			req.Header.Set("Content-Type", contentType)
		}
		for _, cookie := range m.cookiesInput.Entries() {
			req.AddCookie(&http.Cookie{Name: resolver.Resolve(cookie.key), Value: resolver.Resolve(cookie.value)})
		}

		m.settings.syncConfig()
		settings := m.settings.config
		settings.proxyURL = resolver.Resolve(settings.proxyURL)
		settings.proxyBypass = resolver.Resolve(settings.proxyBypass)
		settings.caCertPath = resolver.Resolve(settings.caCertPath)
		settings.clientCertPath = resolver.Resolve(settings.clientCertPath)
		settings.clientKeyPath = resolver.Resolve(settings.clientKeyPath)
		settings.clientPFXPath = resolver.Resolve(settings.clientPFXPath)
		settings.clientPFXPassword = resolver.Resolve(settings.clientPFXPassword)
		client, configErr := configuredClient(m.client, settings)
		if configErr != nil {
			return responseMsg{requestID: requestID, responseBody: fmt.Sprintf("Error configuring request: %v", configErr), responseMeta: "Request failed", assertionResults: unavailableAssertionResults(assertions, "request did not produce a response")}
		}
		client, configErr = configureUnixSocketClient(client, unixTarget)
		if configErr != nil {
			return responseMsg{requestID: requestID, responseBody: fmt.Sprintf("Error configuring request: %v", configErr), responseMeta: "Request failed", assertionResults: unavailableAssertionResults(assertions, "request did not produce a response")}
		}
		client, configErr = configureNTLMClient(client, auth)
		if configErr != nil {
			return responseMsg{requestID: requestID, responseBody: fmt.Sprintf("Error configuring request: %v", configErr), responseMeta: "Request failed", assertionResults: unavailableAssertionResults(assertions, "request did not produce a response")}
		}
		if authErr := auth.authorize(requestContext, client, req, payload); authErr != nil {
			return responseMsg{requestID: requestID, responseBody: fmt.Sprintf("Error authorizing request: %v", authErr), responseMeta: "Request failed", finalURL: displayURL, assertionResults: unavailableAssertionResults(assertions, "request did not produce a response")}
		}
		displayURL = req.URL.String()
		if unixTarget != nil {
			displayURL = unixTarget.displayURL(req.URL)
		}
		started := time.Now()
		resp, err := client.Do(req)
		if err == nil && auth.typeID == authDigest && resp.StatusCode == http.StatusUnauthorized {
			authorization, digestErr := digestAuthorization(resp.Request, auth, resp.Header.Values("WWW-Authenticate"))
			if digestErr == nil {
				var retryRequest *http.Request
				retryRequest, digestErr = cloneRequestForDigestRetry(resp.Request, req.Context(), authorization)
				if digestErr == nil {
					_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
					_ = resp.Body.Close()
					resp, err = client.Do(retryRequest)
				}
			}
			if digestErr != nil {
				_ = resp.Body.Close()
				elapsed := time.Since(started)
				return responseMsg{requestID: requestID, responseBody: fmt.Sprintf("Error authorizing request: %v", digestErr), responseMeta: fmt.Sprintf("Request failed • %s", elapsed.Round(time.Millisecond)), duration: elapsed, finalURL: displayURL, assertionResults: unavailableAssertionResults(assertions, "request did not produce a response")}
			}
		}
		elapsed := time.Since(started)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return responseMsg{requestID: requestID, responseBody: "Request cancelled.", responseMeta: fmt.Sprintf("Cancelled • %s", elapsed.Round(time.Millisecond)), duration: elapsed, finalURL: displayURL, assertionResults: unavailableAssertionResults(assertions, "request was cancelled")}
			}
			return responseMsg{
				requestID:        requestID,
				responseBody:     fmt.Sprintf("Error: %v", err),
				responseMeta:     fmt.Sprintf("Request failed • %s", elapsed.Round(time.Millisecond)),
				duration:         elapsed,
				finalURL:         displayURL,
				assertionResults: unavailableAssertionResults(assertions, "request did not produce a response"),
			}
		}
		if isEventStream(resp.Header.Get("Content-Type")) {
			stream := startEventStream(requestID, resp, parsedURL.String(), unixTarget, started, assertions)
			return responseMsg{
				requestID: requestID, responseHeaders: formatHeaders(resp.Header),
				responseMeta: fmt.Sprintf("%s • %s • streaming • %s", resp.Status, resp.Proto, elapsed.Round(time.Millisecond)),
				statusCode:   resp.StatusCode, duration: elapsed, finalURL: responseFinalURL(resp, unixTarget),
				responseRawAvailable: true, stream: stream,
			}
		}
		defer resp.Body.Close() //nolint:errcheck

		body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBody+1))
		if err != nil {
			assertionResults := evaluateAssertions(assertions, assertionResponse{status: resp.StatusCode, headers: resp.Header, duration: elapsed})
			return responseMsg{
				requestID:        requestID,
				responseBody:     fmt.Sprintf("Error reading response: %v", err),
				responseMeta:     responseMeta(resp, elapsed, 0, parsedURL.String(), unixTarget),
				statusCode:       resp.StatusCode,
				duration:         elapsed,
				finalURL:         responseFinalURL(resp, unixTarget),
				assertionResults: assertionResults,
				variableUpdates:  successfulVariableUpdates(assertions, assertionResults),
			}
		}
		if len(body) > maxResponseBody {
			assertionResults := evaluateAssertions(assertions, assertionResponse{status: resp.StatusCode, headers: resp.Header, body: body, duration: elapsed, size: len(body)})
			return responseMsg{
				requestID:        requestID,
				responseBody:     fmt.Sprintf("Response body exceeds the %s display limit.", formatByteCount(maxResponseBody)),
				responseHeaders:  formatHeaders(resp.Header),
				responseMeta:     responseMeta(resp, elapsed, len(body), parsedURL.String(), unixTarget),
				statusCode:       resp.StatusCode,
				duration:         elapsed,
				responseBytes:    len(body),
				finalURL:         responseFinalURL(resp, unixTarget),
				assertionResults: assertionResults,
				variableUpdates:  successfulVariableUpdates(assertions, assertionResults),
			}
		}

		assertionResults := evaluateAssertions(assertions, assertionResponse{status: resp.StatusCode, headers: resp.Header, body: body, duration: elapsed, size: len(body)})
		return responseMsg{
			requestID:            requestID,
			responseBody:         formatResponseBody(body, resp.Header.Get("Content-Type")),
			responseRaw:          string(body),
			responseRawAvailable: true,
			responseHeaders:      formatHeaders(resp.Header),
			responseMeta:         responseMeta(resp, elapsed, len(body), parsedURL.String(), unixTarget),
			statusCode:           resp.StatusCode,
			duration:             elapsed,
			responseBytes:        len(body),
			finalURL:             responseFinalURL(resp, unixTarget),
			assertionResults:     assertionResults,
			variableUpdates:      successfulVariableUpdates(assertions, assertionResults),
		}
	}
}

func cloneRequestForDigestRetry(request *http.Request, retryContext context.Context, authorization string) (*http.Request, error) {
	retry := request.Clone(retryContext)
	retry.Header = request.Header.Clone()
	retry.Header.Set("Authorization", authorization)
	if request.Body == nil || request.Body == http.NoBody {
		return retry, nil
	}
	if request.GetBody == nil {
		return nil, fmt.Errorf("digest authentication cannot replay the request body")
	}
	body, err := request.GetBody()
	if err != nil {
		return nil, fmt.Errorf("replay request body for Digest authentication: %w", err)
	}
	retry.Body = body
	return retry, nil
}

func responseFinalURL(resp *http.Response, unixTarget ...*unixSocketTarget) string {
	if resp.Request != nil && resp.Request.URL != nil {
		if len(unixTarget) > 0 && unixTarget[0] != nil {
			return unixTarget[0].displayURL(resp.Request.URL)
		}
		return resp.Request.URL.String()
	}
	return ""
}

func responseMeta(resp *http.Response, elapsed time.Duration, size int, requestedURL string, unixTarget ...*unixSocketTarget) string {
	meta := fmt.Sprintf("%s • %s • %s • %s", resp.Status, resp.Proto, elapsed.Round(time.Millisecond), formatByteCount(size))
	finalURL := responseFinalURL(resp, unixTarget...)
	displayRequestedURL := requestedURL
	if len(unixTarget) > 0 && unixTarget[0] != nil {
		if parsed, err := url.Parse(requestedURL); err == nil {
			displayRequestedURL = unixTarget[0].displayURL(parsed)
		}
	}
	if finalURL != "" && finalURL != displayRequestedURL {
		meta += " • → " + finalURL
	}
	return meta
}

func formatByteCount(size int) string {
	if size < 1024 {
		return fmt.Sprintf("%d B", size)
	}
	if size < 1024*1024 {
		return fmt.Sprintf("%.1f KiB", float64(size)/1024)
	}
	return fmt.Sprintf("%.1f MiB", float64(size)/(1024*1024))
}
