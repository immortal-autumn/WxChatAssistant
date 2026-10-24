package native

import (
	"reflect"
	"testing"
)

func TestSameSecondMessagesUseSequenceWithinConversation(t *testing.T) {
	messages := []Message{
		{ID: "later", Peer: "alice", Timestamp: 100, Sequence: 23},
		{ID: "other", Peer: "bob", Timestamp: 100, Sequence: 1},
		{ID: "unknown", Peer: "alice", Timestamp: 100},
		{ID: "earlier", Peer: "alice", Timestamp: 100, Sequence: 22},
		{ID: "old", Peer: "alice", Timestamp: 99, Sequence: 999},
	}
	SortMessages(messages)
	var ids []string
	for _, m := range messages {
		ids = append(ids, m.ID)
	}
	want := []string{"old", "earlier", "other", "unknown", "later"}
	if !reflect.DeepEqual(ids, want) {
		t.Fatal(ids)
	}
	SortMessages(messages)
	for i, m := range messages {
		if m.ID != want[i] {
			t.Fatal("ordering was not stable")
		}
	}
}

func TestMessageSequenceDecodingAndReceiptEnrichment(t *testing.T) {
	raw := message(pv(1, 1), pb(2, pb(1, []byte("alice"))), pb(3, pb(1, []byte("me"))), pv(4, 1), pb(5, pb(1, []byte("Hello"))), pv(9, 100), pv(12, 102), pv(13, 23))
	echo, err := DecodeMessage(raw, "me")
	if err != nil || echo.Sequence != 23 {
		t.Fatal("server sequence was not decoded", err)
	}
	receipt := Message{ID: echo.ID, Peer: "alice", Timestamp: 101}
	MergeMessageOrder(&receipt, echo)
	if receipt.Sequence != 23 || receipt.Timestamp != 100 {
		t.Fatal("sync echo did not replace receipt ordering metadata")
	}
	wrongPeer := echo
	wrongPeer.Peer = "bob"
	wrongPeer.Sequence = 100
	MergeMessageOrder(&receipt, wrongPeer)
	if receipt.Sequence != 23 {
		t.Fatal("metadata crossed conversation boundary")
	}
	if _, err := DecodeMessage(append(raw, pv(13, 1<<32)...), "me"); err == nil {
		t.Fatal("accepted duplicate or overflowing sequence")
	}
}
