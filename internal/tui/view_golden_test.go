package tui

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

var updateViewGoldens = flag.Bool("update", false, "update TUI golden snapshot files")

func TestViewGolden(t *testing.T) {
	initTestZones()

	tests := []struct {
		name   string
		width  int
		height int
	}{
		{name: "80x24", width: 80, height: 24},
		{name: "120x40", width: 120, height: 40},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := representativeGoldenModel()
			updated, _ := m.Update(tea.WindowSizeMsg{Width: tt.width, Height: tt.height})
			m = updated.(model)

			got := normalizeGoldenView(m.View().Content)
			goldenPath := filepath.Join("testdata", "view_"+tt.name+".golden")
			if *updateViewGoldens {
				writeGoldenAtomically(t, goldenPath, got)
			}

			wantBytes, err := os.ReadFile(goldenPath)
			if err != nil {
				t.Fatalf("read %s: %v (regenerate with go test ./internal/tui -run TestViewGolden -update)", goldenPath, err)
			}
			want := normalizeGoldenView(string(wantBytes))
			if got != want {
				t.Fatalf("view differs from %s\n%s\nregenerate only after reviewing the UI change: go test ./internal/tui -run TestViewGolden -update", goldenPath, firstGoldenDifference(want, got))
			}
		})
	}
}

func representativeGoldenModel() model {
	m := NewModel()
	m.setHTTPMethod("POST")
	m.urlInput.SetValue("https://api.example/users")
	m.bodyInput.SetValue("{\n  \"name\": \"Ada Lovelace\",\n  \"role\": \"admin\"\n}")
	m.history = []historyItem{
		{method: "POST", url: "https://api.example.com/v1/users"},
		{method: "GET", url: "https://api.example.com/v1/users/42"},
		{method: "DELETE", url: "https://api.example.com/v1/sessions/current"},
	}
	m.responseMeta = "201 Created • 84ms • 67 B"
	m.response = "{\n  \"id\": 42,\n  \"name\": \"Ada Lovelace\",\n  \"role\": \"admin\"\n}"
	m.responseModel.SetContent(m.response)
	m.responseHeaders = "Content-Type: application/json\nX-Request-Id: req-example-42"
	m.responseHeadersModel.SetContent(m.responseHeaders)
	m.responseTests = "PASS  status equals 201\nPASS  body.id is present"
	m.responseTestsModel.SetContent(m.responseTests)
	m.markRequestDraftClean()
	return m
}

func normalizeGoldenView(view string) string {
	view = ansi.Strip(view)
	view = strings.ReplaceAll(view, "\r\n", "\n")
	return strings.TrimRight(view, "\n") + "\n"
}

func writeGoldenAtomically(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("create golden directory: %v", err)
	}

	temporary, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp-")
	if err != nil {
		t.Fatalf("create temporary golden file: %v", err)
	}
	temporaryPath := temporary.Name()
	t.Cleanup(func() { _ = os.Remove(temporaryPath) })

	if _, err := temporary.WriteString(content); err != nil {
		_ = temporary.Close()
		t.Fatalf("write temporary golden file: %v", err)
	}
	if err := temporary.Chmod(0o644); err != nil {
		_ = temporary.Close()
		t.Fatalf("set golden file permissions: %v", err)
	}
	if err := temporary.Close(); err != nil {
		t.Fatalf("close temporary golden file: %v", err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		t.Fatalf("replace %s: %v", path, err)
	}
}

func firstGoldenDifference(want, got string) string {
	wantLines := strings.Split(want, "\n")
	gotLines := strings.Split(got, "\n")
	lineCount := min(len(wantLines), len(gotLines))
	for index := 0; index < lineCount; index++ {
		if wantLines[index] != gotLines[index] {
			return fmt.Sprintf("first difference at line %d\nwant: %q\n got: %q", index+1, wantLines[index], gotLines[index])
		}
	}
	return fmt.Sprintf("line count differs: want %d, got %d", len(wantLines)-1, len(gotLines)-1)
}
