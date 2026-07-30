package tui

import (
	"strings"
	"testing"
)

func TestHTTPieCommandCoversRequestControls(t *testing.T) {
	request := savedRequest{
		method: "POST", url: "https://api.example/users?fixed=1",
		params: []headerEntry{{key: "include", value: "profile details"}}, headers: []headerEntry{{key: "X-Trace", value: "abc"}}, cookies: []headerEntry{{key: "session", value: "a b"}},
		auth: authConfig{typeID: authBasic, username: "alice", password: "secret"},
		body: bodyConfig{mode: bodyRaw, rawType: rawJSON, raw: `{"name":"Ada"}`},
	}
	command, err := HTTPieCommand(request)
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"http POST", "fixed=1&include=profile+details", "X-Trace:abc", "--auth alice:secret", "Cookie:", "Content-Type:application/json", "--raw", `{"name":"Ada"}`} {
		if !strings.Contains(command, expected) {
			t.Fatalf("HTTPie command missing %q: %s", expected, command)
		}
	}
}

func TestHTTPieCommandMultipartGraphQLAndOAuth(t *testing.T) {
	multipart, err := HTTPieCommand(savedRequest{method: "POST", url: "https://api.example/upload", body: bodyConfig{mode: bodyMultipart, multipart: []headerEntry{{key: "file", value: "@/tmp/a b.txt"}, {key: "note", value: "hello"}}}})
	if err != nil || !strings.Contains(multipart, "--form") || !strings.Contains(multipart, "file@") || !strings.Contains(multipart, "note=hello") {
		t.Fatalf("multipart HTTPie = %q %v", multipart, err)
	}
	graphql, err := HTTPieCommand(savedRequest{method: "POST", url: "https://api.example/graphql", auth: authConfig{typeID: authOAuth2RefreshToken}, body: bodyConfig{mode: bodyGraphQL, graphqlQuery: "query { viewer { id } }", graphqlVariables: `{}`}})
	if err != nil || !strings.Contains(graphql, "oauth2_access_token") || !strings.Contains(graphql, `"query"`) {
		t.Fatalf("GraphQL HTTPie = %q %v", graphql, err)
	}
}

func TestHTTPieCommandRejectsUnixSocket(t *testing.T) {
	_, err := HTTPieCommand(savedRequest{method: "GET", url: "http://unix:/var/run/app.sock:/health"})
	if err == nil || !strings.Contains(err.Error(), "use cURL") {
		t.Fatalf("Unix socket error = %v", err)
	}
}
