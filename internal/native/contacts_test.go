package native

import (
	"encoding/binary"
	"encoding/hex"
	"testing"
)

func TestDecodeContactNames(t *testing.T) {
	// Independent protobuf fixture: alice01, Alice, remark Friend.
	raw, _ := hex.DecodeString("0a090a07616c696365303112070a05416c69636552080a06467269656e64")
	contact, err := DecodeContact(raw)
	if err != nil || contact.Username != "alice01" || contact.Nickname != "Alice" || contact.Remark != "Friend" {
		t.Fatalf("contact: %#v %v", contact, err)
	}
	if _, err = DecodeContact(nil); err == nil {
		t.Fatal("accepted unnamed contact")
	}
	duplicate := append(raw, raw...)
	if _, err = DecodeContact(duplicate); err == nil {
		t.Fatal("accepted ambiguous recipient identity")
	}
}

func contactScalar(number, value uint64) []byte {
	return binary.AppendUvarint(binary.AppendUvarint(nil, number<<3), value)
}

func TestContactClassificationPreservesAbsentVersusZero(t *testing.T) {
	base, _ := hex.DecodeString("0a0c0a0a614063686174726f6f6d") // a@chatroom
	for _, test := range []struct {
		name           string
		property       []byte
		present, muted bool
	}{
		{"absent", nil, false, false},
		{"muted", contactScalar(17, 0), true, true},
		{"enabled", contactScalar(17, 1), true, false},
		{"unknown", contactScalar(17, 2), true, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			c, err := DecodeContact(append(append([]byte(nil), base...), test.property...))
			if err != nil || !c.Group() || c.Muted() != test.muted || (c.ChatRoomNotify != nil) != test.present {
				t.Fatalf("classification: %#v %v", c, err)
			}
		})
	}
	for _, bad := range [][]byte{{0x8a, 0x01, 0x00}, append(contactScalar(17, 0), contactScalar(17, 1)...), contactScalar(24, 1<<32)} {
		if _, err := DecodeContact(append(append([]byte(nil), base...), bad...)); err == nil {
			t.Fatal("accepted ambiguous or invalid classification property")
		}
	}
	for _, flags := range []uint32{8, 24, 136, 520, 776, 1048} {
		c := Contact{Username: "official", VerifyFlag: &flags}
		if !c.Official() {
			t.Fatalf("official verification flag %d was ignored", flags)
		}
		c.Username = "a@chatroom"
		if c.Official() {
			t.Fatal("a group was classified as an official account")
		}
	}
	if !(Contact{Username: "gh_example"}).Official() || (Contact{Username: "alice"}).Official() {
		t.Fatal("ID fallback classification failed")
	}
}

func TestContactMergeRetainsClassificationAndAppliesExplicitChanges(t *testing.T) {
	zero, one, verified := uint32(0), uint32(1), uint32(8)
	old := Contact{Username: "a@chatroom", Nickname: "Group", Remark: "Old remark", Flags: 0x800, FlagMask: 0x800, VerifyFlag: &verified, ChatRoomNotify: &zero, Members: map[string]string{"alice": "Alice"}}
	updated := MergeContact(old, Contact{Username: old.Username, Nickname: "Renamed"})
	if !updated.Muted() || !updated.Pinned() || updated.VerifyFlag == nil || len(updated.Members) != 1 || updated.Remark != "" {
		t.Fatal("omitted classification, pin or roster was lost, or an explicit remark removal was ignored")
	}
	updated = MergeContact(updated, Contact{Username: old.Username, ChatRoomNotify: &one, VerifyFlag: &zero, FlagMask: 0x800})
	if updated.Muted() || updated.Pinned() || *updated.VerifyFlag != 0 {
		t.Fatal("explicit zero/notification update was ignored")
	}
	// Two updates collected in a single sync batch must carry both flag masks
	// when that batch is merged again into an already running TUI.
	batch := MergeContact(Contact{}, Contact{Username: "alice", Flags: 0x800, FlagMask: 0x800})
	batch = MergeContact(batch, Contact{Username: "alice", Flags: 3, FlagMask: 3})
	if merged := MergeContact(Contact{Username: "alice"}, batch); !merged.Pinned() || merged.Flags != 0x803 {
		t.Fatal("batch aggregation lost an earlier pin update")
	}
}

func TestChatRoomNotificationDeltaPreservesContactAcrossMergeLayers(t *testing.T) {
	raw, _ := hex.DecodeString("0a0c0a0a614063686174726f6f6d1000")
	delta, err := DecodeChatRoomNotify(raw)
	if err != nil || !delta.Muted() {
		t.Fatal("could not decode muted notification delta", err)
	}
	batch := MergeContact(Contact{}, delta)
	old := Contact{Username: delta.Username, Nickname: "Group", Remark: "Keep remark", Flags: 0x800, FlagMask: 0x800}
	updated := MergeContact(old, batch)
	if !updated.Muted() || !updated.Pinned() || updated.Nickname != old.Nickname || updated.Remark != old.Remark {
		t.Fatal("notification-only batch replaced full contact metadata")
	}
	for _, bad := range [][]byte{nil, raw[:len(raw)-2], append(append([]byte(nil), raw...), 0x10, 1)} {
		if _, err := DecodeChatRoomNotify(bad); err == nil {
			t.Fatal("accepted missing or ambiguous notification status")
		}
	}
}
