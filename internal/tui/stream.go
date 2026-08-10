package tui

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	uuid "github.com/google/uuid"
)

type responseStreamMsg struct {
	requestID uuid.UUID
	chunk     string
	final     *responseMsg
	stream    <-chan responseStreamMsg
}

func isEventStream(contentType string) bool {
	contentType = strings.ToLower(strings.TrimSpace(strings.Split(contentType, ";")[0]))
	return contentType == "text/event-stream"
}

func startEventStream(requestID uuid.UUID, resp *http.Response, requestedURL string, unixTarget *unixSocketTarget, started time.Time, assertions []headerEntry) <-chan responseStreamMsg {
	stream := make(chan responseStreamMsg, 1)
	go func() {
		defer close(stream)
		defer resp.Body.Close() //nolint:errcheck
		reader := bufio.NewReader(resp.Body)
		var body, event bytes.Buffer

		sendChunk := func() bool {
			if event.Len() == 0 {
				return true
			}
			chunk := event.String()
			event.Reset()
			select {
			case stream <- responseStreamMsg{requestID: requestID, chunk: chunk}:
				return true
			case <-resp.Request.Context().Done():
				return false
			}
		}

		var readErr error
		tooLarge := false
		for {
			line, err := reader.ReadString('\n')
			if line != "" {
				if body.Len()+len(line) > maxResponseBody {
					tooLarge = true
					readErr = fmt.Errorf("response body exceeds the %s display limit", formatByteCount(maxResponseBody))
					break
				}
				body.WriteString(line)
				event.WriteString(line)
				if strings.TrimRight(line, "\r\n") == "" && !sendChunk() {
					readErr = resp.Request.Context().Err()
					break
				}
			}
			if err != nil {
				readErr = err
				break
			}
		}
		_ = sendChunk()

		elapsed := time.Since(started)
		raw := body.String()
		display := sanitizeTerminalText(raw)
		cancelled := errors.Is(readErr, context.Canceled) || errors.Is(resp.Request.Context().Err(), context.Canceled)
		failed := readErr != nil && !errors.Is(readErr, io.EOF) && !cancelled && !tooLarge
		assertionResponse := assertionResponse{status: resp.StatusCode, headers: resp.Header, body: body.Bytes(), duration: elapsed, size: body.Len()}
		assertionResults := evaluateAssertions(assertions, assertionResponse)
		if cancelled {
			assertionResults = unavailableAssertionResults(assertions, "response stream was cancelled")
		} else if failed {
			assertionResults = unavailableAssertionResults(assertions, "response stream failed before completion")
		}
		final := responseMsg{
			requestID: requestID, responseBody: display, responseRaw: raw, responseRawAvailable: !tooLarge,
			responseHeaders: formatHeaders(resp.Header), responseMeta: responseMeta(resp, elapsed, body.Len(), requestedURL, unixTarget),
			statusCode: resp.StatusCode, duration: elapsed, responseBytes: body.Len(), finalURL: responseFinalURL(resp, unixTarget),
			assertionResults: assertionResults, variableUpdates: successfulVariableUpdates(assertions, assertionResults),
		}
		if tooLarge {
			final.responseBody = readErr.Error()
			final.responseRaw = ""
		} else if readErr != nil && !errors.Is(readErr, io.EOF) {
			if cancelled {
				final.responseMeta = fmt.Sprintf("Cancelled • %s", elapsed.Round(time.Millisecond))
			} else {
				final.responseMeta = fmt.Sprintf("Stream failed • %s", elapsed.Round(time.Millisecond))
				final.responseBody = display + "\n\nStream error: " + sanitizeTerminalText(readErr.Error())
			}
		}
		stream <- responseStreamMsg{requestID: final.requestID, final: &final}
	}()
	return stream
}

func waitResponseStream(stream <-chan responseStreamMsg) tea.Cmd {
	return func() tea.Msg {
		message, ok := <-stream
		if !ok {
			return nil
		}
		message.stream = stream
		return message
	}
}

func consumeResponseStream(response responseMsg) responseMsg {
	if response.stream == nil {
		return response
	}
	for message := range response.stream {
		if message.final != nil {
			return *message.final
		}
	}
	return response
}
