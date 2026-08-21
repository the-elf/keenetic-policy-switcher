package favorites

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestNew_MissingFileIsEmpty(t *testing.T) {
	path := filepath.Join(t.TempDir(), "favorites.json")

	s, err := New(path)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if s.Contains("aa:bb:cc:dd:ee:ff") {
		t.Error("a fresh store should have no favorites")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("New should not create the file before the first write")
	}
}

func TestNew_MalformedFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "favorites.json")
	if err := os.WriteFile(path, []byte("not json"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := New(path); err == nil {
		t.Error("New should reject a malformed favorites file")
	}
}

func TestNew_PathIsDirectory(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "favorites.json")
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}

	_, err := New(path)
	if err == nil {
		t.Fatal("New should reject a path that is a directory")
	}
	if !strings.Contains(err.Error(), "is a directory") {
		t.Errorf("error = %q, expected a clear \"is a directory\" message", err.Error())
	}
}

func TestAddPersistsAcrossReload(t *testing.T) {
	path := filepath.Join(t.TempDir(), "favorites.json")

	s, err := New(path)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := s.Add("aa:bb:cc:dd:ee:ff"); err != nil {
		t.Fatalf("Add: %v", err)
	}

	reloaded, err := New(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if !reloaded.Contains("aa:bb:cc:dd:ee:ff") {
		t.Error("favorite did not survive a reload from disk")
	}
}

func TestAddIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "favorites.json")
	s, err := New(path)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if err := s.Add("aa:bb:cc:dd:ee:ff"); err != nil {
		t.Fatalf("first Add: %v", err)
	}
	if err := s.Add("aa:bb:cc:dd:ee:ff"); err != nil {
		t.Fatalf("second Add: %v", err)
	}

	reloaded, err := New(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if len(reloaded.macs) != 1 {
		t.Errorf("macs = %v, expected exactly one entry", reloaded.macs)
	}
}

func TestRemove(t *testing.T) {
	path := filepath.Join(t.TempDir(), "favorites.json")
	s, err := New(path)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := s.Add("aa:bb:cc:dd:ee:ff"); err != nil {
		t.Fatalf("Add: %v", err)
	}

	if err := s.Remove("aa:bb:cc:dd:ee:ff"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if s.Contains("aa:bb:cc:dd:ee:ff") {
		t.Error("mac still favorited after Remove")
	}

	// Removing something absent is a no-op, not an error.
	if err := s.Remove("aa:bb:cc:dd:ee:ff"); err != nil {
		t.Errorf("Remove of an absent mac returned an error: %v", err)
	}
}

// TestAdd_FallsBackWhenRenameFails simulates a single-file Docker bind mount:
// the destination path can't be replaced via rename ("device or resource
// busy" in production), and the store must fall back to writing in place
// instead of returning an error.
func TestAdd_FallsBackWhenRenameFails(t *testing.T) {
	path := filepath.Join(t.TempDir(), "favorites.json")

	original := renameFile
	renameFile = func(oldpath, newpath string) error {
		return &os.LinkError{Op: "rename", Old: oldpath, New: newpath, Err: syscall.EBUSY}
	}
	defer func() { renameFile = original }()

	s, err := New(path)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := s.Add("aa:bb:cc:dd:ee:ff"); err != nil {
		t.Fatalf("Add should fall back instead of failing: %v", err)
	}

	reloaded, err := New(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if !reloaded.Contains("aa:bb:cc:dd:ee:ff") {
		t.Error("favorite was not persisted through the rename-fallback path")
	}
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Error("the temp file should be cleaned up after falling back")
	}
}

// TestAdd_RollsBackOnSaveFailure ensures a failed disk write doesn't leave
// the in-memory state diverged from what's actually persisted — otherwise a
// 500 response would be a lie: Contains() would report favorite:true on the
// very next GET /api/devices despite the write never landing on disk.
func TestAdd_RollsBackOnSaveFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "favorites.json")
	s, err := New(path)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	original := writeFile
	writeFile = func(name string, data []byte, perm os.FileMode) error {
		return &os.PathError{Op: "write", Path: name, Err: syscall.ENOSPC}
	}
	defer func() { writeFile = original }()

	if err := s.Add("aa:bb:cc:dd:ee:ff"); err == nil {
		t.Fatal("Add should report the disk-write failure")
	}
	if s.Contains("aa:bb:cc:dd:ee:ff") {
		t.Error("Add left the mac favorited in memory despite the failed write")
	}
}

// TestRemove_RollsBackOnSaveFailure mirrors TestAdd_RollsBackOnSaveFailure
// for the removal path.
func TestRemove_RollsBackOnSaveFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "favorites.json")
	s, err := New(path)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := s.Add("aa:bb:cc:dd:ee:ff"); err != nil {
		t.Fatalf("Add: %v", err)
	}

	original := writeFile
	writeFile = func(name string, data []byte, perm os.FileMode) error {
		return &os.PathError{Op: "write", Path: name, Err: syscall.ENOSPC}
	}
	defer func() { writeFile = original }()

	if err := s.Remove("aa:bb:cc:dd:ee:ff"); err == nil {
		t.Fatal("Remove should report the disk-write failure")
	}
	if !s.Contains("aa:bb:cc:dd:ee:ff") {
		t.Error("Remove dropped the mac from memory despite the failed write")
	}
}

func TestContainsMultiple(t *testing.T) {
	path := filepath.Join(t.TempDir(), "favorites.json")
	s, err := New(path)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	for _, mac := range []string{"aa:bb:cc:dd:ee:01", "aa:bb:cc:dd:ee:02"} {
		if err := s.Add(mac); err != nil {
			t.Fatalf("Add(%s): %v", mac, err)
		}
	}

	if !s.Contains("aa:bb:cc:dd:ee:01") || !s.Contains("aa:bb:cc:dd:ee:02") {
		t.Error("expected both MACs to be favorited")
	}
	if s.Contains("aa:bb:cc:dd:ee:03") {
		t.Error("unrelated mac reported as favorited")
	}
}
