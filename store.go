package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"github.com/gofrs/flock"
)

type State struct {
	Token    string            `json:"token"`
	BotID    string            `json:"bot_id"`
	UserID   string            `json:"user_id"`
	BaseURL  string            `json:"base_url"`
	Cursor   string            `json:"cursor"`
	Contexts map[string]string `json:"contexts"`
}
type Store struct{ Dir string }

func (s Store) init() error { return os.MkdirAll(s.Dir, 0700) }
func (s Store) load() (State, error) {
	state := State{Contexts: map[string]string{}}
	data, err := os.ReadFile(filepath.Join(s.Dir, "state.json"))
	if errors.Is(err, os.ErrNotExist) {
		return state, nil
	}
	if err != nil {
		return state, err
	}
	if err := json.Unmarshal(data, &state); err != nil {
		return state, errors.New("cannot decode state.json; restore it or use another --config-dir")
	}
	if state.Contexts == nil {
		state.Contexts = map[string]string{}
	}
	return state, nil
}

// Hold a short lock and reload before writing, so a sender and reader can coexist.
func (s Store) update(fn func(*State) error) error {
	if err := s.init(); err != nil {
		return err
	}
	lock := flock.New(filepath.Join(s.Dir, "state.lock"))
	if err := lock.Lock(); err != nil {
		return err
	}
	defer lock.Close()
	state, err := s.load()
	if err != nil {
		return err
	}
	if err := fn(&state); err != nil {
		return err
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(s.Dir, ".state-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), filepath.Join(s.Dir, "state.json"))
}
func (s Store) readerLock() (*flock.Flock, error) {
	if err := s.init(); err != nil {
		return nil, err
	}
	lock := flock.New(filepath.Join(s.Dir, "reader.lock"))
	ok, err := lock.TryLock()
	if err != nil {
		lock.Close()
		return nil, err
	}
	if !ok {
		lock.Close()
		return nil, errors.New("another read/login/logout command is running for this config directory")
	}
	return lock, nil
}
