package main

import (
	"encoding/hex"
	"testing"

	"github.com/kevmo314/wechat-cli/internal/native"
)

func TestArchiveRecoversSequenceFromOldSyncPayload(t *testing.T) {
	store, err := openNativeStore(Store{t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	// AddMsg fixture: ID 102, alice -> me, timestamp 100, sequence 23.
	raw, err := hex.DecodeString("086612070a05616c6963651a040a026d6520012a070a0548656c6c6f486460666817")
	if err != nil {
		t.Fatal(err)
	}
	later := native.Message{ID: "102", From: "alice", To: "me", Peer: "alice", Type: 1, Text: "Hello", Timestamp: 100}
	for _, record := range []nativeJournal{
		{Owner: "me", Messages: []native.Message{later}},
		{Owner: "me", Messages: []native.Message{{ID: "101", Peer: "alice", Type: 1, Timestamp: 100, Sequence: 22}}},
		{Owner: "me", Messages: []native.Message{later}, Batch: native.SyncBatch{Commands: []native.SyncCommand{{ID: 5, Payload: raw}}}},
	} {
		if err := store.journal(record); err != nil {
			t.Fatal(err)
		}
	}
	snapshot, err := store.chatSnapshot("me")
	if err != nil {
		t.Fatal(err)
	}
	history, err := store.history("me")
	if err != nil {
		t.Fatal(err)
	}
	for _, messages := range [][]native.Message{snapshot.Messages, history} {
		if len(messages) != 2 || messages[0].ID != "101" || messages[1].ID != "102" || messages[1].Sequence != 23 {
			t.Fatal("old archive or receipt retained arrival ordering", messages)
		}
	}
}

func TestLiveSameSecondEchoCorrectsOrderWithoutDuplicateUnread(t *testing.T) {
	m := testChatModel()
	later := native.Message{ID: "102", From: "alice", Peer: "alice", Type: 1, Timestamp: 100}
	m.addMessages([]native.Message{later}, true)
	m.addMessages([]native.Message{{ID: "101", From: "alice", Peer: "alice", Type: 1, Timestamp: 100, Sequence: 22}}, true)
	before := m.unread["alice"]
	later.Sequence = 23
	m.addMessages([]native.Message{later}, true)
	var got []native.Message
	for _, message := range m.messages {
		if message.Peer == "alice" && message.Timestamp == 100 {
			got = append(got, message)
		}
	}
	if len(got) != 2 || got[0].ID != "101" || got[1].ID != "102" || m.unread["alice"] != before {
		t.Fatal("echo did not reconcile ordering without duplicate unread counts")
	}
	if m.latest["alice"].ID != "102" {
		t.Fatal("sync echo did not correct the conversation preview")
	}

	m = testChatModel()
	later.Sequence = 23
	m.addMessages([]native.Message{later, {ID: "101", From: "alice", Peer: "alice", Type: 1, Timestamp: 100, Sequence: 22}}, true)
	m.rebuildRows()
	if m.latest["alice"].ID != "102" {
		t.Fatal("same-second batch left an older message in the conversation preview")
	}
}
