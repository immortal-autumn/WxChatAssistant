package native

import "testing"

func TestRecallMarksOriginalInTheCorrectConversation(t *testing.T) {
	messages := []Message{
		{ID: "18446744073709551615", From: "room@chatroom", To: "me", Peer: "room@chatroom", Type: 49, Text: "alice:\n<msg><appmsg><title>Interview notes</title><type>5</type></appmsg></msg>"},
		{ID: "event", From: "me", To: "me", Peer: "me", Type: 10002, Text: `<sysmsg type="revokemsg"><revokemsg><session>room@chatroom</session><newmsgid>18446744073709551615</newmsgid><replacemsg>Alice recalled a message</replacemsg></revokemsg></sysmsg>`},
		{ID: "18446744073709551615", From: "bob", Peer: "bob", Type: 1, Text: "different conversation"},
	}
	ProjectMessages(messages)
	if !messages[0].Deleted || messages[0].Sender != "alice" || messages[0].Text[0] != '<' {
		t.Fatalf("original not projected: %#v", messages[0])
	}
	if !messages[1].Hidden || messages[1].Peer != "room@chatroom" {
		t.Fatal("recall event did not bind to its conversation")
	}
	if messages[2].Deleted {
		t.Fatal("recall crossed conversation boundary")
	}
	ProjectMessages(messages)
	if messages[0].Sender != "alice" {
		t.Fatal("group sender normalization was not idempotent")
	}
}
func TestRoomMemberDisplayNames(t *testing.T) {
	data := message(pv(1, 1), pb(2, message(pb(1, []byte("alice")), pb(2, []byte("Alice")), pb(3, []byte("Group Alias")))))
	members, err := decodeRoomMembers(data)
	if err != nil || members["alice"] != "Group Alias" {
		t.Fatal(members, err)
	}
	raw := message(pb(1, pb(1, []byte("room@chatroom"))), pb(15, message(pb(1, pb(1, []byte("alice"))), pb(2, pb(1, []byte("Alice"))))))
	contact, err := DecodeContact(raw)
	if err != nil || contact.Members["alice"] != "Alice" {
		t.Fatal(contact, err)
	}
}
