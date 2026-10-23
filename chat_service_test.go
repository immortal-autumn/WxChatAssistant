package main

import (
	"encoding/hex"
	"testing"

	"github.com/kevmo314/wechat-cli/internal/native"
)

func TestChatSnapshotNamesAndAccountIsolation(t *testing.T) {
	store, err := openNativeStore(Store{t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := hex.DecodeString("0a090a07616c696365303112070a05416c69636552080a06467269656e64")
	first := nativeJournal{Owner: "me", Batch: native.SyncBatch{Commands: []native.SyncCommand{{ID: 2, Payload: raw}}}, Messages: []native.Message{{ID: "1", Peer: "alice01", Text: "hello"}}}
	for _, record := range []nativeJournal{first, first, {Owner: "other", Messages: []native.Message{{ID: "2", Text: "private other account"}}}} {
		if err = store.journal(record); err != nil {
			t.Fatal(err)
		}
	}
	snapshot, err := store.chatSnapshot("me")
	if err != nil || len(snapshot.Messages) != 1 || snapshot.Contacts["alice01"].Remark != "Friend" {
		t.Fatalf("snapshot: %#v %v", snapshot, err)
	}
}
