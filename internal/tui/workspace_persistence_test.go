package tui

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/google/uuid"
)

func TestWorkspaceRejectsStaleLoadedSnapshot(t *testing.T) {
	path := filepath.Join(t.TempDir(), "workspace.json")
	seed := NewModel()
	if err := seed.LoadWorkspace(path); err != nil {
		t.Fatal(err)
	}
	seed.variablesInput.SetEntries([]headerEntry{{key: "source", value: "seed"}})
	if err := seed.SaveWorkspace(); err != nil {
		t.Fatal(err)
	}

	first, err := NewModelWithWorkspace(path)
	if err != nil {
		t.Fatal(err)
	}
	stale, err := NewModelWithWorkspace(path)
	if err != nil {
		t.Fatal(err)
	}

	first.variablesInput.SetEntries([]headerEntry{{key: "source", value: "first"}})
	if err := first.SaveWorkspace(); err != nil {
		t.Fatal(err)
	}
	wantDisk, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	stale.variablesInput.SetEntries([]headerEntry{{key: "source", value: "stale"}})
	err = stale.SaveWorkspace()
	if !errors.Is(err, ErrWorkspaceConflict) || !strings.Contains(err.Error(), "reload before saving") {
		t.Fatalf("stale save error = %v, want workspace conflict", err)
	}
	gotDisk, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(gotDisk, wantDisk) {
		t.Fatal("stale save changed the workspace")
	}

	stale.saveWorkspaceWithStatus()
	if !strings.Contains(stale.workspaceSaveStatus, ErrWorkspaceConflict.Error()) {
		t.Fatalf("TUI conflict status = %q", stale.workspaceSaveStatus)
	}

	if err := stale.LoadWorkspace(path); err != nil {
		t.Fatal(err)
	}
	stale.variablesInput.SetEntries([]headerEntry{{key: "source", value: "reloaded"}})
	if err := stale.SaveWorkspace(); err != nil {
		t.Fatalf("save after reload: %v", err)
	}
}

func TestWorkspaceConflictRollsBackRejectedModelMutations(t *testing.T) {
	path := filepath.Join(t.TempDir(), "workspace.json")
	seed := NewModel()
	if err := seed.LoadWorkspace(path); err != nil {
		t.Fatal(err)
	}
	seed.savedRequests = []savedRequest{{
		name: "Kept", method: "GET", url: "https://example.com/kept",
		examples: []savedExample{{name: "Kept example", responseBody: "seed response"}},
	}}
	seed.history = []historyItem{{
		method: "GET", url: "https://example.com/history", requestID: uuid.New(),
		responseBody: "seed history",
	}}
	seed.variablesInput.SetEntries([]headerEntry{{key: "source", value: "seed"}})
	seed.settings.SetConfig(requestSettings{timeout: 5 * time.Second, proxyURL: "http://seed-proxy.example"})
	if err := seed.SetCookie("https://example.com/path", "session=seed; Path=/"); err != nil {
		t.Fatal(err)
	}
	if err := seed.SaveWorkspace(); err != nil {
		t.Fatal(err)
	}

	writer, err := NewModelWithWorkspace(path)
	if err != nil {
		t.Fatal(err)
	}
	stale, err := NewModelWithWorkspace(path)
	if err != nil {
		t.Fatal(err)
	}
	writer.variablesInput.SetEntries([]headerEntry{{key: "source", value: "writer"}})
	if err := writer.SaveWorkspace(); err != nil {
		t.Fatal(err)
	}

	stale.applySavedRequest(stale.savedRequests[0])
	stale.activeSavedIndex = 0
	stale.sidebarMode = sidebarCollections
	stale.collectionPendingD = true
	stale.savedRequests[0].examples = nil
	stale.history = nil
	stale.ClearCookies()
	stale.variablesInput.SetEntries([]headerEntry{{key: "source", value: "rejected"}})
	stale.settings.SetConfig(requestSettings{timeout: 9 * time.Second, proxyURL: "http://rejected-proxy.example"})
	stale.handleHistoryKeys("d")

	if len(stale.savedRequests) != 1 || stale.savedRequests[0].name != "Kept" {
		t.Fatalf("saved requests after rejected delete = %#v, want original request", stale.savedRequests)
	}
	if stale.activeSavedIndex != 0 && !stale.requestDraftDirty() {
		t.Fatalf("rejected delete left a clean detached draft at active index %d", stale.activeSavedIndex)
	}
	if len(stale.savedRequests[0].examples) != 1 || stale.savedRequests[0].examples[0].name != "Kept example" {
		t.Fatalf("examples after rejected save = %#v, want loaded snapshot", stale.savedRequests[0].examples)
	}
	if len(stale.history) != 1 || stale.history[0].responseBody != "seed history" {
		t.Fatalf("history after rejected save = %#v, want loaded snapshot", stale.history)
	}
	cookies := stale.Cookies()
	if len(cookies) != 1 || cookies[0].Name != "session" || cookies[0].Value != "seed" {
		t.Fatalf("cookies after rejected save = %#v, want loaded snapshot", cookies)
	}
	entries := stale.variablesInput.Entries()
	if len(entries) != 1 || entries[0].value != "seed" {
		t.Fatalf("environment after rejected save = %#v, want loaded snapshot", entries)
	}
	if stale.settings.config.proxyURL != "http://seed-proxy.example" || stale.settings.config.timeout != 5*time.Second {
		t.Fatalf("settings after rejected save = %#v, want loaded snapshot", stale.settings.config)
	}
	if !strings.Contains(stale.workspaceSaveStatus, ErrWorkspaceConflict.Error()) {
		t.Fatalf("conflict status = %q", stale.workspaceSaveStatus)
	}

	loaded, err := NewModelWithWorkspace(path)
	if err != nil {
		t.Fatal(err)
	}
	diskEntries := loaded.variablesInput.Entries()
	if len(diskEntries) != 1 || diskEntries[0].value != "writer" {
		t.Fatalf("winning workspace environment = %#v, want writer value", diskEntries)
	}
	if len(loaded.savedRequests) != 1 || loaded.savedRequests[0].name != "Kept" {
		t.Fatalf("winning workspace requests = %#v, want original request", loaded.savedRequests)
	}
	if len(loaded.savedRequests[0].examples) != 1 || len(loaded.history) != 1 || len(loaded.Cookies()) != 1 {
		t.Fatalf("winning workspace lost persisted state: request=%#v history=%#v cookies=%#v", loaded.savedRequests[0], loaded.history, loaded.Cookies())
	}
	if loaded.settings.config.proxyURL != "http://seed-proxy.example" || loaded.settings.config.timeout != 5*time.Second {
		t.Fatalf("winning workspace settings = %#v, want seed settings", loaded.settings.config)
	}
}

func TestNonConflictWorkspaceFailureKeepsModelMutations(t *testing.T) {
	parent := filepath.Join(t.TempDir(), "regular-file")
	if err := os.WriteFile(parent, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	m := NewModel()
	m.workspacePath = filepath.Join(parent, "workspace.json")
	m.savedRequests = append(m.savedRequests, savedRequest{name: "Unsaved", method: "GET", url: "https://example.com"})

	if m.saveWorkspaceWithStatus().succeeded() {
		t.Fatal("non-conflict workspace failure reported success")
	}
	if len(m.savedRequests) != 1 || m.savedRequests[0].name != "Unsaved" {
		t.Fatalf("model mutation after non-conflict failure = %#v, want it retained", m.savedRequests)
	}
	if strings.Contains(m.response, ErrWorkspaceConflict.Error()) {
		t.Fatalf("non-conflict save error reported as conflict: %q", m.response)
	}
}

func TestWorkspaceConflictRollsBackConcurrentCreationMutation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "workspace.json")
	winner := NewModel()
	stale := NewModel()
	if err := winner.LoadWorkspace(path); err != nil {
		t.Fatal(err)
	}
	if err := stale.LoadWorkspace(path); err != nil {
		t.Fatal(err)
	}
	winner.savedRequests = append(winner.savedRequests, savedRequest{name: "Winner", method: "GET", url: "https://example.com/winner"})
	if err := winner.SaveWorkspace(); err != nil {
		t.Fatal(err)
	}

	stale.savedRequests = append(stale.savedRequests, savedRequest{name: "Rejected", method: "GET", url: "https://example.com/rejected"})
	if result := stale.saveWorkspaceWithStatus(); result != workspaceSaveConflictHandled {
		t.Fatalf("concurrent workspace creation result = %d, want handled conflict", result)
	}
	if len(stale.savedRequests) != 0 {
		t.Fatalf("rejected concurrent creation left requests in memory: %#v", stale.savedRequests)
	}
	if !strings.Contains(stale.workspaceSaveStatus, ErrWorkspaceConflict.Error()) {
		t.Fatalf("concurrent creation error = %q, want workspace conflict", stale.workspaceSaveStatus)
	}
}

func TestWorkspaceConflictDoesNotRebindShiftedDuplicateDraft(t *testing.T) {
	path := filepath.Join(t.TempDir(), "workspace.json")
	seed := NewModel()
	if err := seed.LoadWorkspace(path); err != nil {
		t.Fatal(err)
	}
	seed.savedRequests = []savedRequest{
		{name: "A", method: "GET", url: "https://example.com/same", examples: []savedExample{{name: "A example"}}},
		{name: "B", method: "GET", url: "https://example.com/same", examples: []savedExample{{name: "B example"}}},
	}
	if err := seed.SaveWorkspace(); err != nil {
		t.Fatal(err)
	}

	writer, err := NewModelWithWorkspace(path)
	if err != nil {
		t.Fatal(err)
	}
	stale, err := NewModelWithWorkspace(path)
	if err != nil {
		t.Fatal(err)
	}
	writer.variablesInput.SetEntries([]headerEntry{{key: "source", value: "writer"}})
	if err := writer.SaveWorkspace(); err != nil {
		t.Fatal(err)
	}

	stale.applySavedRequest(stale.savedRequests[1])
	stale.activeSavedIndex = 1
	stale.sidebarMode = sidebarCollections
	stale.collectionPos = 0
	stale.collectionPendingD = true
	stale.handleHistoryKeys("d")

	if len(stale.savedRequests) != 2 || stale.savedRequests[0].name != "A" || stale.savedRequests[1].name != "B" {
		t.Fatalf("requests after rejected delete = %#v, want A and B restored", stale.savedRequests)
	}
	if stale.activeSavedIndex != -1 {
		t.Fatalf("active saved index = %d, want detached draft after count-changing rollback", stale.activeSavedIndex)
	}
	if !stale.requestDraftDirty() {
		t.Fatal("shifted duplicate draft was rebound cleanly after rollback")
	}
}

func TestWorkspaceConflictRestoresSidebarPositions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "workspace.json")
	seed := NewModel()
	if err := seed.LoadWorkspace(path); err != nil {
		t.Fatal(err)
	}
	seed.savedRequests = []savedRequest{
		{name: "A", method: "GET", url: "https://example.com/a", examples: []savedExample{{name: "A1"}, {name: "A2"}}},
		{name: "B", method: "GET", url: "https://example.com/b"},
	}
	seed.history = []historyItem{
		{method: "GET", url: "https://example.com/a", requestID: uuid.New()},
		{method: "GET", url: "https://example.com/b", requestID: uuid.New()},
	}
	if err := seed.SetCookie("https://example.com/path", "a=1; Path=/"); err != nil {
		t.Fatal(err)
	}
	if err := seed.SetCookie("https://example.com/path", "b=2; Path=/"); err != nil {
		t.Fatal(err)
	}
	if err := seed.SaveWorkspace(); err != nil {
		t.Fatal(err)
	}

	writer, err := NewModelWithWorkspace(path)
	if err != nil {
		t.Fatal(err)
	}
	stale, err := NewModelWithWorkspace(path)
	if err != nil {
		t.Fatal(err)
	}
	writer.variablesInput.SetEntries([]headerEntry{{key: "source", value: "writer"}})
	if err := writer.SaveWorkspace(); err != nil {
		t.Fatal(err)
	}

	stale.activeSavedIndex = 0
	stale.examplePos = 0
	stale.response = "rejected example"
	stale.responseMeta = "200 OK"
	if err := stale.saveCurrentResponseExample(); err == nil || !strings.Contains(err.Error(), ErrWorkspaceConflict.Error()) {
		t.Fatalf("save example error = %v, want workspace conflict", err)
	}
	if stale.examplePos != 0 || len(stale.savedRequests[0].examples) != 2 || strings.HasPrefix(stale.responseSearchStatus, "Saved response example") {
		t.Fatalf("example save rollback = pos %d examples %#v status %q", stale.examplePos, stale.savedRequests[0].examples, stale.responseSearchStatus)
	}

	stale.sidebarMode = sidebarCollections
	stale.collectionPos = 0
	stale.handleHistoryKeys("c")
	if stale.collectionPos != 0 {
		t.Fatalf("collection position after rejected duplicate = %d, want 0", stale.collectionPos)
	}
	stale.collectionPos = 1
	stale.collectionPendingD = true
	stale.handleHistoryKeys("d")
	if stale.collectionPos != 1 {
		t.Fatalf("collection position after rejected delete = %d, want 1", stale.collectionPos)
	}

	stale.sidebarMode = sidebarExamples
	stale.examplePos = 1
	stale.examplePendingD = true
	stale.handleHistoryKeys("d")
	if stale.examplePos != 1 {
		t.Fatalf("example position after rejected delete = %d, want 1", stale.examplePos)
	}

	stale.sidebarMode = sidebarHistory
	stale.historyPos = 1
	stale.historyPendingD = true
	stale.handleHistoryKeys("d")
	if stale.historyPos != 1 {
		t.Fatalf("history position after rejected delete = %d, want 1", stale.historyPos)
	}

	stale.sidebarMode = sidebarCookies
	stale.cookiePos = 1
	stale.cookiePendingD = true
	stale.handleHistoryKeys("d")
	if stale.cookiePos != 1 {
		t.Fatalf("cookie position after rejected delete = %d, want 1", stale.cookiePos)
	}
}

func TestWorkspaceConflictWithoutSnapshotIsNotReportedHandled(t *testing.T) {
	path := filepath.Join(t.TempDir(), "workspace.json")
	seed := NewModel()
	if err := seed.LoadWorkspace(path); err != nil {
		t.Fatal(err)
	}
	if err := seed.SaveWorkspace(); err != nil {
		t.Fatal(err)
	}

	uninitialized := NewModel()
	uninitialized.workspacePath = path
	uninitialized.savedRequests = append(uninitialized.savedRequests, savedRequest{name: "Retained"})
	if result := uninitialized.saveWorkspaceWithStatus(); result != workspaceSaveFailed {
		t.Fatalf("save result = %d, want ordinary failure when conflict rollback is unavailable", result)
	}
	if len(uninitialized.savedRequests) != 1 || !strings.Contains(uninitialized.workspaceSaveStatus, "failed to roll back rejected changes") {
		t.Fatalf("unhandled conflict state = requests %#v status %q", uninitialized.savedRequests, uninitialized.workspaceSaveStatus)
	}
}

func TestConflictRollbackSupersedesRequestSaveLocalRollback(t *testing.T) {
	path := filepath.Join(t.TempDir(), "workspace.json")
	seed := NewModel()
	if err := seed.LoadWorkspace(path); err != nil {
		t.Fatal(err)
	}
	seed.savedRequests = []savedRequest{{name: "Persisted", method: "GET", url: "https://example.com/persisted"}}
	if err := seed.SaveWorkspace(); err != nil {
		t.Fatal(err)
	}

	writer, err := NewModelWithWorkspace(path)
	if err != nil {
		t.Fatal(err)
	}
	stale, err := NewModelWithWorkspace(path)
	if err != nil {
		t.Fatal(err)
	}

	retained := savedRequest{name: "Retained after I/O failure", method: "POST", url: "https://example.com/retained"}
	stale.savedRequests = append(stale.savedRequests, retained)
	stale.applySavedRequest(retained)
	stale.activeSavedIndex = 1
	regularFile := filepath.Join(t.TempDir(), "regular-file")
	if err := os.WriteFile(regularFile, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	stale.workspacePath = filepath.Join(regularFile, "workspace.json")
	if result := stale.saveWorkspaceWithStatus(); result != workspaceSaveFailed {
		t.Fatalf("transient non-conflict save result = %d, want failed", result)
	}
	stale.workspacePath = path
	if len(stale.savedRequests) != 2 {
		t.Fatalf("non-conflict failure did not retain model mutation: %#v", stale.savedRequests)
	}

	writer.variablesInput.SetEntries([]headerEntry{{key: "source", value: "writer"}})
	if err := writer.SaveWorkspace(); err != nil {
		t.Fatal(err)
	}

	updated, _ := stale.Update(tea.KeyPressMsg{Code: 'w', Mod: tea.ModCtrl})
	stale = updated.(model)
	if len(stale.savedRequests) != 1 || stale.savedRequests[0].name != "Persisted" {
		t.Fatalf("conflict rollback was overwritten by caller-local rollback: %#v", stale.savedRequests)
	}
	if stale.activeSavedIndex != -1 || !stale.requestDraftDirty() {
		t.Fatalf("restored request binding = index %d dirty %v, want detached dirty draft", stale.activeSavedIndex, stale.requestDraftDirty())
	}
	if !strings.Contains(stale.workspaceSaveStatus, ErrWorkspaceConflict.Error()) {
		t.Fatalf("request save error = %q, want workspace conflict", stale.workspaceSaveStatus)
	}
}

func TestConflictRollbackDetachesEqualCountReplacementDraft(t *testing.T) {
	path := filepath.Join(t.TempDir(), "workspace.json")
	seed := NewModel()
	if err := seed.LoadWorkspace(path); err != nil {
		t.Fatal(err)
	}
	seed.savedRequests = []savedRequest{
		{name: "A", method: "GET", url: "https://example.com/a"},
		{name: "B", method: "GET", url: "https://example.com/b"},
	}
	if err := seed.SaveWorkspace(); err != nil {
		t.Fatal(err)
	}

	writer, err := NewModelWithWorkspace(path)
	if err != nil {
		t.Fatal(err)
	}
	stale, err := NewModelWithWorkspace(path)
	if err != nil {
		t.Fatal(err)
	}

	// Retain a deletion after a non-conflict I/O failure, leaving the stale
	// model with [B] while its canonical snapshot remains [A, B].
	stale.savedRequests = stale.savedRequests[1:]
	regularFile := filepath.Join(t.TempDir(), "regular-file")
	if err := os.WriteFile(regularFile, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	stale.workspacePath = filepath.Join(regularFile, "workspace.json")
	if result := stale.saveWorkspaceWithStatus(); result != workspaceSaveFailed {
		t.Fatalf("transient non-conflict save result = %d, want failed", result)
	}
	stale.workspacePath = path

	stale.applyHistoryItem(historyItem{method: "POST", url: "https://example.com/c", requestID: uuid.New()})
	writer.variablesInput.SetEntries([]headerEntry{{key: "source", value: "writer"}})
	if err := writer.SaveWorkspace(); err != nil {
		t.Fatal(err)
	}

	// Ctrl-W appends C to [B], so the post-mutation and restored lists both
	// contain two entries even though index 1 refers to different requests.
	updated, _ := stale.Update(tea.KeyPressMsg{Code: 'w', Mod: tea.ModCtrl})
	stale = updated.(model)
	if len(stale.savedRequests) != 2 || stale.savedRequests[0].name != "A" || stale.savedRequests[1].name != "B" {
		t.Fatalf("restored requests = %#v, want canonical A and B", stale.savedRequests)
	}
	if stale.urlInput.Value() != "https://example.com/c" || stale.activeSavedIndex != -1 || !stale.requestDraftDirty() {
		t.Fatalf("replacement draft = url %q index %d dirty %v, want detached dirty C", stale.urlInput.Value(), stale.activeSavedIndex, stale.requestDraftDirty())
	}
}

func TestWorkspaceConflictRollsBackOverlayEditsWhenClosed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "workspace.json")
	seed := NewModel()
	if err := seed.LoadWorkspace(path); err != nil {
		t.Fatal(err)
	}
	seed.variablesInput.SetEntries([]headerEntry{{key: "source", value: "seed"}, {key: "second", value: "kept"}})
	seed.settings.SetConfig(requestSettings{timeout: 5 * time.Second, proxyURL: "http://seed-proxy.example"})
	if err := seed.SaveWorkspace(); err != nil {
		t.Fatal(err)
	}

	writer, err := NewModelWithWorkspace(path)
	if err != nil {
		t.Fatal(err)
	}
	stale, err := NewModelWithWorkspace(path)
	if err != nil {
		t.Fatal(err)
	}
	writer.variablesInput.SetEntries([]headerEntry{{key: "source", value: "writer"}, {key: "second", value: "kept"}})
	if err := writer.SaveWorkspace(); err != nil {
		t.Fatal(err)
	}

	stale.settingsOpen = true
	stale.setFocus(paneRequest)
	stale.inputMode = modeInsert
	stale.settings.page = settingsTLS
	stale.settings.cursor = 5
	stale.settings.proxyInput.SetValue("http://rejected-proxy.example")
	updated, _ := stale.Update(tea.KeyPressMsg{Code: 't', Mod: tea.ModCtrl})
	stale = updated.(model)
	if stale.settingsOpen || stale.settings.config.proxyURL != "http://seed-proxy.example" || stale.settings.proxyInput.Value() != "http://seed-proxy.example" || stale.settings.page != settingsTLS || stale.settings.cursor != 5 {
		t.Fatalf("settings close rollback = open %v config %q input %q page %d cursor %d", stale.settingsOpen, stale.settings.config.proxyURL, stale.settings.proxyInput.Value(), stale.settings.page, stale.settings.cursor)
	}
	if !strings.Contains(stale.workspaceSaveStatus, ErrWorkspaceConflict.Error()) {
		t.Fatalf("settings close status = %q, want workspace conflict", stale.workspaceSaveStatus)
	}

	stale.environmentOpen = true
	stale.setFocus(paneRequest)
	stale.inputMode = modeInsert
	stale.variablesInput.SetEntries([]headerEntry{{key: "source", value: "rejected"}, {key: "second", value: "kept"}})
	stale.variablesInput.cursorRow = 1
	stale.variablesInput.cursorCol = 1
	updated, _ = stale.Update(tea.KeyPressMsg{Code: 't', Mod: tea.ModCtrl})
	stale = updated.(model)
	entries := stale.variablesInput.Entries()
	if !stale.settingsOpen || stale.environmentOpen || len(entries) != 2 || entries[0].value != "seed" || stale.variablesInput.cursorRow != 1 || stale.variablesInput.cursorCol != 1 {
		t.Fatalf("environment switch rollback = settings %v environment %v entries %#v cursor %d/%d", stale.settingsOpen, stale.environmentOpen, entries, stale.variablesInput.cursorRow, stale.variablesInput.cursorCol)
	}

	stale.settingsOpen = false
	stale.environmentOpen = true
	stale.setFocus(paneRequest)
	stale.inputMode = modeInsert
	stale.variablesInput.SetEntries([]headerEntry{{key: "source", value: "rejected on focus loss"}, {key: "second", value: "kept"}})
	stale.variablesInput.cursorRow = 1
	stale.variablesInput.cursorCol = 1
	stale.setFocus(paneHistory)
	entries = stale.variablesInput.Entries()
	if stale.focus != paneHistory || len(entries) != 2 || entries[0].value != "seed" || stale.variablesInput.cursorRow != 1 || stale.variablesInput.cursorCol != 1 {
		t.Fatalf("environment focus-loss rollback = focus %d entries %#v cursor %d/%d", stale.focus, entries, stale.variablesInput.cursorRow, stale.variablesInput.cursorCol)
	}

	stale.setFocus(paneRequest)
	stale.inputMode = modeNormal
	updated, _ = stale.Update(tea.KeyPressMsg{Code: 'd'})
	stale = updated.(model)
	updated, _ = stale.Update(tea.KeyPressMsg{Code: 'd'})
	stale = updated.(model)
	entries = stale.variablesInput.Entries()
	if len(entries) != 2 || entries[0].value != "seed" || stale.variablesInput.cursorRow != 1 || stale.variablesInput.cursorCol != 1 {
		t.Fatalf("environment dd rollback = entries %#v cursor %d/%d", entries, stale.variablesInput.cursorRow, stale.variablesInput.cursorCol)
	}
}

func TestStaleWorkspaceCanQuitWithoutOverwritingNewerSnapshot(t *testing.T) {
	path := filepath.Join(t.TempDir(), "workspace.json")
	writer := NewModel()
	if err := writer.LoadWorkspace(path); err != nil {
		t.Fatal(err)
	}
	stale := NewModel()
	if err := stale.LoadWorkspace(path); err != nil {
		t.Fatal(err)
	}

	writer.variablesInput.SetEntries([]headerEntry{{key: "writer", value: "newer"}})
	if err := writer.SaveWorkspace(); err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	stale.variablesInput.SetEntries([]headerEntry{{key: "writer", value: "stale"}})
	stale.settings.proxyInput.SetValue("http://stale-proxy.example")
	stale.settingsOpen = true
	stale.inputMode = modeInsert
	stale.response = "original response"
	stale.responseMeta = "200 OK"
	stale.responseModel.SetContent(stale.response)

	ctrlC := tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}
	updated, firstCommand := stale.Update(ctrlC)
	stale = updated.(model)
	if firstCommand == nil || !stale.quitConfirmOpen {
		t.Fatal("initial quit did not open confirmation")
	}
	if stale.response != "original response" || stale.responseModel.GetContent() != "original response" || stale.responseMeta != "200 OK" {
		t.Fatalf("quit preflight clobbered response: body=%q viewport=%q metadata=%q", stale.response, stale.responseModel.GetContent(), stale.responseMeta)
	}
	if !strings.Contains(stale.workspaceSaveStatus, ErrWorkspaceConflict.Error()) {
		t.Fatalf("initial quit status = %q, want workspace conflict", stale.workspaceSaveStatus)
	}
	if entries := stale.variablesInput.Entries(); len(entries) != 0 {
		t.Fatalf("stale environment edits survived quit preflight: %#v", entries)
	}
	if stale.settings.config.proxyURL != "" || stale.settings.proxyInput.Value() != "" {
		t.Fatalf("stale settings edits survived quit preflight: config=%q input=%q", stale.settings.config.proxyURL, stale.settings.proxyInput.Value())
	}
	if stale.inputMode != modeNormal {
		t.Fatalf("quit preflight left restored settings in input mode %d", stale.inputMode)
	}
	updated, _ = stale.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	stale = updated.(model)
	if stale.quitConfirmOpen || stale.inputMode != modeNormal {
		t.Fatalf("cancelled quit state = confirm %v mode %d", stale.quitConfirmOpen, stale.inputMode)
	}
	if stale.response != "original response" || stale.responseModel.GetContent() != "original response" || stale.responseMeta != "200 OK" {
		t.Fatalf("cancelled quit lost response: body=%q viewport=%q metadata=%q", stale.response, stale.responseModel.GetContent(), stale.responseMeta)
	}
	updated, _ = stale.Update(ctrlC)
	stale = updated.(model)
	updated, command := stale.Update(ctrlC)
	stale = updated.(model)
	if command == nil {
		t.Fatal("confirmed stale-workspace quit returned no command")
	}
	message := command()
	if _, ok := message.(tea.QuitMsg); !ok {
		t.Fatalf("confirmed stale-workspace quit returned %T, want tea.QuitMsg", message)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatal("stale quit overwrote the newer workspace snapshot")
	}
}

func TestQuitStillBlocksOnNonConflictWorkspaceSaveFailure(t *testing.T) {
	parent := filepath.Join(t.TempDir(), "regular-file")
	if err := os.WriteFile(parent, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	m := NewModel()
	m.workspacePath = filepath.Join(parent, "workspace.json")
	m.response = "original response"
	m.responseMeta = "200 OK"
	m.responseModel.SetContent(m.response)

	ctrlC := tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}
	updated, _ := m.Update(ctrlC)
	m = updated.(model)
	updated, command := m.Update(ctrlC)
	m = updated.(model)
	if command != nil {
		t.Fatal("non-conflict workspace save failure allowed quit")
	}
	if m.response != "original response" || m.responseMeta != "200 OK" || m.responseModel.GetContent() != "original response" {
		t.Fatalf("save failure clobbered response: body=%q meta=%q viewport=%q", m.response, m.responseMeta, m.responseModel.GetContent())
	}
	if !strings.Contains(m.workspaceSaveStatus, "Workspace save failed") {
		t.Fatalf("save failure status = %q", m.workspaceSaveStatus)
	}
}

func TestWorkspaceRejectsConcurrentCreationFromStaleSnapshot(t *testing.T) {
	path := filepath.Join(t.TempDir(), "workspace.json")
	first := NewModel()
	stale := NewModel()
	if err := first.LoadWorkspace(path); err != nil {
		t.Fatal(err)
	}
	if err := stale.LoadWorkspace(path); err != nil {
		t.Fatal(err)
	}

	first.variablesInput.SetEntries([]headerEntry{{key: "writer", value: "first"}})
	if err := first.SaveWorkspace(); err != nil {
		t.Fatal(err)
	}
	stale.variablesInput.SetEntries([]headerEntry{{key: "writer", value: "stale"}})
	if err := stale.SaveWorkspace(); !errors.Is(err, ErrWorkspaceConflict) {
		t.Fatalf("concurrent creation error = %v, want workspace conflict", err)
	}

	loaded, err := NewModelWithWorkspace(path)
	if err != nil {
		t.Fatal(err)
	}
	entries := loaded.variablesInput.Entries()
	if len(entries) != 1 || entries[0].value != "first" {
		t.Fatalf("winning workspace = %#v", entries)
	}

	workspaceInfo, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := workspaceInfo.Mode().Perm(); got != 0o600 {
		t.Fatalf("workspace permissions = %o, want 600", got)
	}
	lockInfo, err := os.Stat(workspaceIdentity(path) + ".lock")
	if err != nil {
		t.Fatal(err)
	}
	if got := lockInfo.Mode().Perm(); got != 0o600 {
		t.Fatalf("workspace lock permissions = %o, want 600", got)
	}
}

const (
	workspaceLockHelperEnv     = "COURIER_WORKSPACE_LOCK_HELPER"
	workspaceLockHelperPathEnv = "COURIER_WORKSPACE_LOCK_PATH"
	workspaceLockHelperReady   = "workspace-lock-helper-ready"
	workspaceLockHelperHeld    = "workspace-lock-helper-acquired"
)

func TestWorkspaceLockHelper(t *testing.T) {
	if os.Getenv(workspaceLockHelperEnv) != "1" {
		return
	}
	if _, err := fmt.Fprintln(os.Stdout, workspaceLockHelperReady); err != nil {
		t.Fatal(err)
	}
	lock, err := acquireWorkspaceLock(os.Getenv(workspaceLockHelperPathEnv))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fmt.Fprintln(os.Stdout, workspaceLockHelperHeld); err != nil {
		t.Fatal(err)
	}
	if err := lock.release(); err != nil {
		t.Fatal(err)
	}
}

func TestWorkspaceLockSerializesProcesses(t *testing.T) {
	path := filepath.Join(t.TempDir(), "workspace.json")
	lock, err := acquireWorkspaceLock(path)
	if err != nil {
		t.Fatal(err)
	}
	lockHeld := true
	defer func() {
		if lockHeld {
			_ = lock.release()
		}
	}()

	command := exec.Command(os.Args[0], "-test.run=^TestWorkspaceLockHelper$", "-test.v")
	command.Env = append(os.Environ(), workspaceLockHelperEnv+"=1", workspaceLockHelperPathEnv+"="+path)
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr synchronizedBuffer
	command.Stderr = &stderr
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	finished := false
	defer func() {
		if !finished {
			_ = command.Process.Kill()
			_ = command.Wait()
		}
	}()

	lines := make(chan string)
	go func() {
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			lines <- scanner.Text()
		}
		close(lines)
	}()
	if !waitForWorkspaceLockHelperLine(lines, workspaceLockHelperReady, 5*time.Second) {
		t.Fatalf("lock helper did not start; stderr: %s", stderr.String())
	}
	if waitForWorkspaceLockHelperLine(lines, workspaceLockHelperHeld, 250*time.Millisecond) {
		t.Fatal("second process acquired the workspace lock before it was released")
	}

	if err := lock.release(); err != nil {
		t.Fatal(err)
	}
	lockHeld = false
	if !waitForWorkspaceLockHelperLine(lines, workspaceLockHelperHeld, 5*time.Second) {
		t.Fatalf("lock helper did not acquire the released lock; stderr: %s", stderr.String())
	}
	if err := command.Wait(); err != nil {
		finished = true
		t.Fatalf("lock helper failed: %v; stderr: %s", err, stderr.String())
	}
	finished = true
}

type synchronizedBuffer struct {
	mu sync.Mutex
	bytes.Buffer
}

func (buffer *synchronizedBuffer) Write(data []byte) (int, error) {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.Buffer.Write(data)
}

func (buffer *synchronizedBuffer) String() string {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.Buffer.String()
}

func waitForWorkspaceLockHelperLine(lines <-chan string, want string, timeout time.Duration) bool {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for {
		select {
		case line, ok := <-lines:
			if !ok {
				return false
			}
			if line == want {
				return true
			}
		case <-timer.C:
			return false
		}
	}
}
