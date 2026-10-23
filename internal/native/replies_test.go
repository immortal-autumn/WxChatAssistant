package native

import (
	"strings"
	"testing"
)

func TestQuotedReplyEscapingAndReference(t *testing.T) {
	target := Message{ID: "18446744073709551615", From: "room@chatroom", Sender: "alice", Peer: "room@chatroom", Type: 1, Text: "<not markup> & a quoted message"}
	content, err := replyContent(target, "A reply with <tags> & symbols", "Alice")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(content, "<not markup>") {
		t.Fatal("reference content was injected as XML")
	}
	reference, ok := ReferenceFor(Message{Type: 49, Text: "<msg>" + content + "</msg>"})
	if !ok || reference.ID != target.ID || reference.Content != target.Text || reference.Sender != "alice" {
		t.Fatalf("quoted reference lost identity: %#v", reference)
	}
	target.Deleted = true
	if _, err = replyContent(target, "reply", "Alice"); err == nil {
		t.Fatal("quoted a deleted message")
	}
}
