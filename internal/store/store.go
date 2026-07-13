package store

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// State is the embedded datastore. For v0 it is a single JSON file holding the
// last-applied rendered artifacts (path -> content). That is enough to compute
// "pending" as a diff between what apply WOULD write and what it last DID write.
// The interface is deliberately small so a bbolt or Postgres backend can slot
// in behind it later without touching callers.
type State struct {
	path    string
	Applied map[string]string `json:"applied"` // artifact path -> content
}

// Open loads state from path, creating an empty state if it does not exist.
func Open(path string) (*State, error) {
	s := &State{path: path, Applied: map[string]string{}}
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return s, nil
		}
		return nil, err
	}
	if err := json.Unmarshal(b, s); err != nil {
		return nil, err
	}
	if s.Applied == nil {
		s.Applied = map[string]string{}
	}
	s.path = path
	return s, nil
}

// Save persists state atomically.
func (s *State) Save() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

// LastApplied returns the previously applied content for an artifact, and
// whether it was present.
func (s *State) LastApplied(path string) (string, bool) {
	v, ok := s.Applied[path]
	return v, ok
}

// Record stores the applied content for an artifact.
func (s *State) Record(path, content string) {
	s.Applied[path] = content
}
