package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

const defaultStateDir = "/var/lib/dynamo-connect"

// state is what dynamo-connect remembers between runs of what it sent, since
// the tools answer whether they hold a client secret but never which one.
type state struct {
	path string
	// SignIn holds, per tool, the fingerprint of the sign-in it last sent.
	SignIn map[string]string `json:"signIn"`
}

func loadState(dir string) (*state, error) {
	s := &state{path: filepath.Join(dir, "state.json"), SignIn: map[string]string{}}
	raw, err := os.ReadFile(s.path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return s, nil
	case err != nil:
		return nil, fmt.Errorf("read what dynamo-connect sent before from %s: %w", s.path, err)
	}
	if err := json.Unmarshal(raw, s); err != nil {
		return nil, fmt.Errorf("%s is not what dynamo-connect writes; remove it, and the next run sends every client secret again: %w", s.path, err)
	}
	if s.SignIn == nil {
		s.SignIn = map[string]string{}
	}
	return s, nil
}

// save replaces the file whole, so a run stopped halfway leaves the old one.
func (s *state) save() error {
	raw, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return fmt.Errorf("dynamo-connect cannot keep what it sent in %s; give it a writable volume there (DYNAMO_STATE_DIR): %w", filepath.Dir(s.path), err)
	}
	return os.Rename(tmp, s.path)
}

// signInFingerprint stands for a sign-in without revealing its secret.
func signInFingerprint(issuer, clientID, secret string) string {
	sum := sha256.Sum256([]byte("dynamo-connect sign-in\x00" + issuer + "\x00" + clientID + "\x00" + secret))
	return hex.EncodeToString(sum[:])
}
