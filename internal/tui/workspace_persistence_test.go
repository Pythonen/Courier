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
	if stale.responseMeta != "Workspace save failed" || !strings.Contains(stale.response, ErrWorkspaceConflict.Error()) {
		t.Fatalf("TUI conflict status = meta %q response %q", stale.responseMeta, stale.response)
	}

	if err := stale.LoadWorkspace(path); err != nil {
		t.Fatal(err)
	}
	stale.variablesInput.SetEntries([]headerEntry{{key: "source", value: "reloaded"}})
	if err := stale.SaveWorkspace(); err != nil {
		t.Fatalf("save after reload: %v", err)
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

	ctrlC := tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}
	updated, _ := stale.Update(ctrlC)
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

	ctrlC := tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}
	updated, _ := m.Update(ctrlC)
	m = updated.(model)
	updated, command := m.Update(ctrlC)
	m = updated.(model)
	if command != nil {
		t.Fatal("non-conflict workspace save failure allowed quit")
	}
	if m.responseMeta != "Workspace save failed" || m.responseModel.GetContent() == "" {
		t.Fatalf("save failure state = meta %q response %q", m.responseMeta, m.responseModel.GetContent())
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
