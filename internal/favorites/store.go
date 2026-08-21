// Package favorites persists the set of starred device MACs in a small JSON
// file on disk. It is intentionally not backed by a database: the data is a
// handful of MAC strings, and a plain file kept in memory with a mutex is the
// lightest thing that can serve every browser on the LAN the same list (the
// reason it isn't per-browser localStorage in the first place).
package favorites

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"sync"
)

// Store holds the favorite MAC set in memory and mirrors every change to a
// JSON file at path.
type Store struct {
	path string
	mu   sync.Mutex
	macs map[string]struct{}
}

type fileFormat struct {
	MACs []string `json:"macs"`
}

// New loads path if it exists and returns a Store seeded with its contents.
// A missing file is not an error: it just means no favorites have been set
// yet, and the file is created on the first Add.
func New(path string) (*Store, error) {
	s := &Store{path: path, macs: map[string]struct{}{}}

	if info, err := os.Stat(path); err == nil && info.IsDir() {
		return nil, fmt.Errorf("%s is a directory, expected a JSON file (or nothing)", path)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return s, nil
		}
		return nil, err
	}

	var ff fileFormat
	if err := json.Unmarshal(data, &ff); err != nil {
		return nil, err
	}
	for _, mac := range ff.MACs {
		s.macs[mac] = struct{}{}
	}
	return s, nil
}

// Contains reports whether mac is currently favorited.
func (s *Store) Contains(mac string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.macs[mac]
	return ok
}

// Add marks mac as a favorite. It is idempotent and a no-op (no disk write)
// if mac is already favorited. If the disk write fails, the in-memory state
// is rolled back so a reported error always matches what's actually
// persisted and served.
func (s *Store) Add(mac string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.macs[mac]; ok {
		return nil
	}
	s.macs[mac] = struct{}{}
	if err := s.saveLocked(); err != nil {
		delete(s.macs, mac)
		return err
	}
	return nil
}

// Remove unmarks mac as a favorite. It is idempotent and a no-op if mac is
// not currently favorited. If the disk write fails, the in-memory state is
// rolled back — see Add.
func (s *Store) Remove(mac string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.macs[mac]; !ok {
		return nil
	}
	delete(s.macs, mac)
	if err := s.saveLocked(); err != nil {
		s.macs[mac] = struct{}{}
		return err
	}
	return nil
}

// writeFile and renameFile are os.WriteFile and os.Rename, indirected so
// tests can force a disk-write failure (writeFile) or the cross-mount
// rename fallback (renameFile) deterministically, without needing an
// actual full disk or bind mount.
var (
	writeFile  = os.WriteFile
	renameFile = os.Rename
)

// saveLocked writes the current set to disk. It normally writes to a temp
// file and renames it into place, so a crash mid-write can't leave a
// truncated, unparseable favorites file behind. But when path is a
// single-file Docker bind mount, the file is its own mount point and the
// kernel refuses to rename over it ("device or resource busy") even though
// it's in the same directory — in that case, fall back to writing the file
// in place, trading atomicity for actually working under Docker Compose's
// `- ./favorites.json:/favorites.json` volume.
func (s *Store) saveLocked() error {
	macs := make([]string, 0, len(s.macs))
	for mac := range s.macs {
		macs = append(macs, mac)
	}
	sort.Strings(macs)

	data, err := json.MarshalIndent(fileFormat{MACs: macs}, "", "  ")
	if err != nil {
		return err
	}

	tmp := s.path + ".tmp"
	if err := writeFile(tmp, data, 0o600); err != nil {
		return err
	}
	if err := renameFile(tmp, s.path); err != nil {
		_ = os.Remove(tmp)
		return writeFile(s.path, data, 0o600)
	}
	return nil
}
