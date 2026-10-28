package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/immortal-autumn/WxChatAssistant/internal/native"
)

func TestNativeArchiveReplayAndAccountIsolation(t *testing.T) {
	root := t.TempDir()
	botState := []byte("existing bot state")
	if err := os.WriteFile(filepath.Join(root, "state.json"), botState, 0600); err != nil {
		t.Fatal(err)
	}
	s, err := openNativeStore(Store{root})
	if err != nil {
		t.Fatal(err)
	}
	first := nativeJournal{Owner: "alice", Batch: native.SyncBatch{Commands: []native.SyncCommand{{ID: 9999, Payload: []byte{1, 2, 3}}}}, Messages: []native.Message{{ID: "123", Text: "retained"}}}
	for _, record := range []nativeJournal{first, first, {Owner: "bob", Messages: []native.Message{{ID: "123", Text: "other account"}}}} {
		if err = s.journal(record); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.history("alice")
	if err != nil || len(got) != 1 || got[0].Text != "retained" {
		t.Fatalf("replay/account filtering: %v %v", got, err)
	}
	files, err := filepath.Glob(filepath.Join(s.dir, "batches", "*.json"))
	if err != nil || len(files) != 3 {
		t.Fatal(files, err)
	}
	for _, path := range files {
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != 0600 {
			t.Fatalf("archive permissions: %v %v", info, err)
		}
	}
	var saved nativeJournal
	if err = s.load(filepath.Join("batches", filepath.Base(files[0])), &saved); err != nil {
		t.Fatal(err)
	}
	if len(saved.Batch.Commands) != 1 || !bytes.Equal(saved.Batch.Commands[0].Payload, []byte{1, 2, 3}) {
		t.Fatal("unknown commands lost")
	}
	if err = s.save("state.json", nativeState{}); err != nil {
		t.Fatal(err)
	}
	unchanged, err := os.ReadFile(filepath.Join(root, "state.json"))
	if err != nil || !bytes.Equal(unchanged, botState) {
		t.Fatal("native overwrote bot credentials")
	}
}
