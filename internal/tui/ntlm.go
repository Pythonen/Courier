package tui

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/Azure/go-ntlmssp"
)

func configureNTLMClient(client *http.Client, config authConfig) (*http.Client, error) {
	if config.typeID != authNTLM {
		return client, nil
	}
	if strings.TrimSpace(config.username) == "" {
		return nil, fmt.Errorf("NTLM username is required")
	}
	copyClient := *client
	transport := client.Transport
	if transport == nil {
		transport = http.DefaultTransport
	}
	copyClient.Transport = ntlmssp.Negotiator{RoundTripper: transport, WorkstationName: config.ntlmWorkstation}
	return &copyClient, nil
}

func applyNTLMCredentials(request *http.Request, config authConfig) {
	username := config.username
	if domain := strings.TrimSpace(config.ntlmDomain); domain != "" && !strings.Contains(username, `\`) && !strings.Contains(username, "@") {
		username = domain + `\` + username
	}
	request.SetBasicAuth(username, config.password)
}
