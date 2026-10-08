package agent

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
)

// SaveSnapshot writes the last applied snapshot to disk. After a restart the node keeps
// answering from it instead of returning SERVFAIL until the panel comes back.
func SaveSnapshot(dir, role string, body []byte) error {
	if dir == "" {
		return nil
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp := filepath.Join(dir, role+".json.tmp")
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(body); err != nil {
		f.Close()
		_ = os.Remove(tmp)
		return err
	}
	// Flush before rename: a crash must not leave an empty file under the real name.
	if err := f.Sync(); err != nil {
		f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, filepath.Join(dir, role+".json"))
}

// LoadSnapshot returns the stored snapshot. A missing or unreadable file is not an error:
// the node simply waits for the panel.
func LoadSnapshot(dir, role string) ([]byte, bool) {
	if dir == "" {
		return nil, false
	}
	body, err := os.ReadFile(filepath.Join(dir, role+".json"))
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			return nil, false
		}
		return nil, false
	}
	if len(body) == 0 {
		return nil, false
	}
	return body, true
}

// Restore applies the snapshot left on disk, so a restarted node keeps answering
// while the panel is down. It reports whether a usable snapshot was found.
func Restore(dir, role string, apply ApplyFunc) bool {
	body, ok := LoadSnapshot(dir, role)
	if !ok {
		return false
	}
	if err := apply(body); err != nil {
		return false
	}
	return true
}

// Persist applies a snapshot and then stores it. The store happens after the snapshot is live,
// so a failed write never blocks a valid configuration.
func Persist(dir, role string, apply ApplyFunc, onErr func(error)) ApplyFunc {
	return func(body []byte) error {
		if err := apply(body); err != nil {
			return err
		}
		if err := SaveSnapshot(dir, role, body); err != nil && onErr != nil {
			onErr(err)
		}
		return nil
	}
}
