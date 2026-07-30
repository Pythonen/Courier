package tui

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestHawkPublishedHeaderVector(t *testing.T) {
	request, err := http.NewRequest(http.MethodGet, "http://example.com:8000/resource/1?b=1&a=2", nil)
	if err != nil {
		t.Fatal(err)
	}
	config := authConfig{typeID: authHawk, hawkID: "dh37fgj492je", hawkKey: "werxhqb98rpaxn39848xrunpaw3489ruxnpa98w4rxn", hawkAlgorithm: "sha256", hawkExt: "some-app-ext-data"}
	header, err := hawkAuthorization(request, nil, config, time.Unix(1353832234, 0), "j4h3g2")
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{`Hawk id="dh37fgj492je"`, `ts="1353832234"`, `nonce="j4h3g2"`, `ext="some-app-ext-data"`, `mac="6R4rV5iE+NPoym+WwjeHzjAGXUtLNIxmo1vpMofpLAE="`} {
		if !strings.Contains(header, expected) {
			t.Fatalf("Hawk header missing %q: %s", expected, header)
		}
	}
}

func TestHawkPublishedPayloadHashVector(t *testing.T) {
	request, err := http.NewRequest(http.MethodPost, "https://example.com/resource", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "text/plain; charset=utf-8")
	config := authConfig{typeID: authHawk, hawkID: "id", hawkKey: "key", hawkAlgorithm: "sha256"}
	header, err := hawkAuthorization(request, []byte("Thank you for flying Hawk"), config, time.Unix(1, 0), "nonce")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(header, `hash="Yi9LfIIFRtBEPt74PVmbTF/xVAwPn7ub15ePICfgnuY="`) {
		t.Fatalf("Hawk payload hash = %s", header)
	}
}

func TestHawkWorkspaceAndPostmanRoundTrip(t *testing.T) {
	config := authConfig{typeID: authHawk, hawkID: "client", hawkKey: "secret", hawkAlgorithm: "sha1", hawkExt: "app-data"}
	if got := config.toWorkspace().fromWorkspace(); got != config {
		t.Fatalf("workspace Hawk = %#v", got)
	}
	exported := exportPostmanAuth(config)
	encoded, _ := json.Marshal(exported)
	var imported postmanAuth
	if err := json.Unmarshal(encoded, &imported); err != nil {
		t.Fatal(err)
	}
	if got := postmanAuthConfig(&imported); got != config {
		t.Fatalf("Postman Hawk = %#v", got)
	}
}

func TestHawkRejectsInvalidConfiguration(t *testing.T) {
	request, _ := http.NewRequest(http.MethodGet, "https://example.com", nil)
	for _, config := range []authConfig{{hawkAlgorithm: "sha512", hawkID: "id", hawkKey: "key"}, {hawkAlgorithm: "sha256", hawkID: "", hawkKey: "key"}, {hawkAlgorithm: "sha256", hawkID: "id\ninvalid", hawkKey: "key"}} {
		if _, err := hawkAuthorization(request, nil, config, time.Now(), "nonce"); err == nil {
			t.Fatalf("accepted invalid Hawk config %#v", config)
		}
	}
}
