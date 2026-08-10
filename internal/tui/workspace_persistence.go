package tui

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// ErrWorkspaceConflict indicates that the workspace changed after this model
// loaded (or last saved) it. Callers must reload instead of overwriting the
// newer snapshot.
var ErrWorkspaceConflict = errors.New("workspace changed since it was loaded")

type workspaceDiskSnapshot struct {
	exists   bool
	checksum [sha256.Size]byte
}

type modelWorkspaceSnapshot struct {
	path string
	disk workspaceDiskSnapshot
	// data is the canonical loaded/saved state used to roll back a TUI
	// mutation when the disk snapshot rejects it as stale.
	data []byte
}

// Bubble Tea copies model values between updates. The HTTP client pointer is
// stable across those copies and unique to a logical model, so it provides a
// persistence identity without coupling the UI model definition to storage.
var modelWorkspaceSnapshots sync.Map // map[*http.Client]modelWorkspaceSnapshot

func workspaceSnapshotForData(data []byte) workspaceDiskSnapshot {
	return workspaceDiskSnapshot{exists: true, checksum: sha256.Sum256(data)}
}

func workspaceSnapshotOnDisk(path string) (workspaceDiskSnapshot, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return workspaceDiskSnapshot{}, nil
	}
	if err != nil {
		return workspaceDiskSnapshot{}, err
	}
	return workspaceSnapshotForData(data), nil
}

func rememberModelWorkspaceSnapshot(m *model, path string, disk workspaceDiskSnapshot, data []byte) {
	if m.client == nil {
		return
	}
	modelWorkspaceSnapshots.Store(m.client, modelWorkspaceSnapshot{
		path: workspaceIdentity(path),
		disk: disk,
		data: append([]byte(nil), data...),
	})
}

func modelWorkspaceSnapshotData(m *model, path string) ([]byte, bool) {
	if m.client == nil {
		return nil, false
	}
	loaded, ok := modelWorkspaceSnapshots.Load(m.client)
	if !ok {
		return nil, false
	}
	snapshot := loaded.(modelWorkspaceSnapshot)
	if snapshot.path != workspaceIdentity(path) {
		return nil, false
	}
	data := snapshot.data
	return append([]byte(nil), data...), true
}

func validateModelWorkspaceSnapshot(m *model, path string, current workspaceDiskSnapshot) error {
	identity := workspaceIdentity(path)
	loaded, ok := modelWorkspaceSnapshots.Load(m.client)
	if !ok {
		if !current.exists {
			return nil
		}
		return workspaceConflictError(path)
	}
	expected := loaded.(modelWorkspaceSnapshot)
	if expected.path != identity || expected.disk != current {
		return workspaceConflictError(path)
	}
	return nil
}

func workspaceConflictError(path string) error {
	return fmt.Errorf("%w: %s; reload before saving to avoid overwriting newer changes", ErrWorkspaceConflict, path)
}

func workspaceIdentity(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		return filepath.Clean(path)
	}
	return filepath.Clean(abs)
}

type workspaceLock struct {
	file *os.File
}

func acquireWorkspaceLock(path string) (*workspaceLock, error) {
	identity := workspaceIdentity(path)
	dir := filepath.Dir(identity)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create workspace directory: %w", err)
	}
	file, err := os.OpenFile(identity+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open workspace lock: %w", err)
	}
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("secure workspace lock: %w", err)
	}
	if err := lockWorkspaceFile(file); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("lock workspace: %w", err)
	}
	return &workspaceLock{file: file}, nil
}

func (lock *workspaceLock) release() error {
	unlockErr := unlockWorkspaceFile(lock.file)
	closeErr := lock.file.Close()
	if unlockErr != nil {
		return fmt.Errorf("unlock workspace: %w", unlockErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close workspace lock: %w", closeErr)
	}
	return nil
}

func withWorkspaceLock(path string, action func() error) (err error) {
	lock, err := acquireWorkspaceLock(path)
	if err != nil {
		return err
	}
	defer func() {
		if releaseErr := lock.release(); err == nil && releaseErr != nil {
			err = releaseErr
		}
	}()
	return action()
}

func writeWorkspaceAtomically(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create workspace directory: %w", err)
	}
	temp, err := os.CreateTemp(dir, ".workspace-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary workspace: %w", err)
	}
	tempPath := temp.Name()
	defer func() { _ = os.Remove(tempPath) }()
	if err := temp.Chmod(0o600); err != nil {
		_ = temp.Close()
		return fmt.Errorf("secure temporary workspace: %w", err)
	}
	if _, err := temp.Write(data); err != nil {
		_ = temp.Close()
		return fmt.Errorf("write workspace: %w", err)
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return fmt.Errorf("sync workspace: %w", err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("close workspace: %w", err)
	}
	if err := os.Rename(tempPath, path); err != nil {
		return fmt.Errorf("replace workspace: %w", err)
	}
	return nil
}
