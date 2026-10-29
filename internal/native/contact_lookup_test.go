package native

import (
	"context"
	"strings"
	"testing"
)

func TestContactLookupValidatesGroupIdentityAndResult(t *testing.T) {
	peer := "room@chatroom"
	raw := message(pb(1, pb(1, []byte(peer))), pb(2, pb(1, []byte("学习交流群"))))
	response := message(pb(1, pv(1, 0)), pv(2, 1), pb(3, raw))
	got, err := decodeContactLookup(response, peer)
	if err != nil {
		t.Fatal(err)
	}
	contact, err := DecodeContact(got)
	if err != nil || contact.Username != peer || contact.Nickname != "学习交流群" {
		t.Fatal("group name was not decoded", err)
	}
	for _, bad := range [][]byte{
		nil,
		message(pb(1, pv(1, 5)), pv(2, 1), pb(3, raw)),
		message(pb(1, pv(1, 0)), pv(2, 0)),
		message(pb(1, pv(1, 0)), pv(2, 2), pb(3, raw)),
		message(pb(1, pv(1, 0)), pv(2, 1), pb(3, raw), pb(3, raw)),
		append(response, pb(4, []byte{5})...),
		append(response, pv(4, 5)...),
		append(response, pb(4, []byte{0, 0})...),
		append(response, pb(4, []byte{0x80})...),
	} {
		if _, err := decodeContactLookup(bad, peer); err == nil {
			t.Fatal("accepted missing, failed, or ambiguous contact result")
		}
	}
	if _, err := decodeContactLookup(response, "other@chatroom"); err == nil {
		t.Fatal("accepted a different group's name")
	}
	for _, status := range [][]byte{pb(4, []byte{0}), pv(4, 0)} {
		if _, err := decodeContactLookup(append(response, status...), peer); err != nil {
			t.Fatal("successful per-contact status rejected", err)
		}
	}
}

func TestFetchContactRejectsInvalidPeerBeforeConnecting(t *testing.T) {
	s := &Session{}
	for _, peer := range []string{"", "bad\n@chatroom", "\xff", strings.Repeat("x", 513)} {
		if _, err := s.FetchContact(context.Background(), nil, peer); err == nil || !strings.Contains(err.Error(), "invalid contact ID") {
			t.Fatalf("invalid peer was not rejected before authentication: %v", err)
		}
	}
}
