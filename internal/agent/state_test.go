package agent

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSnapshotRoundTrip(t *testing.T) {
	dir := t.TempDir()
	if _, ok := LoadSnapshot(dir, "dns"); ok {
		t.Fatal("пустой каталог что-то вернул")
	}
	body := []byte(`{"version":7}`)
	if err := SaveSnapshot(dir, "dns", body); err != nil {
		t.Fatal(err)
	}
	got, ok := LoadSnapshot(dir, "dns")
	if !ok || string(got) != string(body) {
		t.Fatalf("got %q ok=%v", got, ok)
	}
	// The other role is a separate file.
	if _, ok := LoadSnapshot(dir, "proxy"); ok {
		t.Fatal("роли перепутаны")
	}
	// A second save replaces the first and leaves no temporary files.
	if err := SaveSnapshot(dir, "dns", []byte(`{"version":8}`)); err != nil {
		t.Fatal(err)
	}
	got, _ = LoadSnapshot(dir, "dns")
	if string(got) != `{"version":8}` {
		t.Fatalf("не перезаписался: %q", got)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "dns.json" {
		t.Fatalf("лишние файлы: %v", entries)
	}
}

func TestSnapshotFileMode(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "sub", "state")
	if err := SaveSnapshot(dir, "proxy", []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(filepath.Join(dir, "proxy.json"))
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Fatalf("права: %v", st.Mode().Perm())
	}
	di, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if di.Mode().Perm() != 0o700 {
		t.Fatalf("права каталога: %v", di.Mode().Perm())
	}
}

func TestSnapshotCorruptIgnored(t *testing.T) {
	dir := t.TempDir()
	// An empty file means a write was cut short; the node must start without a snapshot.
	if err := os.WriteFile(filepath.Join(dir, "dns.json"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ok := LoadSnapshot(dir, "dns"); ok {
		t.Fatal("пустой файл принят за снимок")
	}
	// No directory configured means no persistence, not an error.
	if err := SaveSnapshot("", "dns", []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	if _, ok := LoadSnapshot("", "dns"); ok {
		t.Fatal("без каталога что-то вернулось")
	}
}
