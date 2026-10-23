package native

import (
	"strings"
	"testing"
)

func contactEditFixture() []byte {
	return message(pb(1, pb(1, []byte("alice"))), pb(2, pb(1, []byte("Alice"))), pb(10, pb(1, []byte("Old remark"))), pv(7, 0xffff), pv(8, 0xa01), pv(17, 2))
}

func contactEditCommand(t *testing.T, request []byte) []byte {
	t.Helper()
	list, err := getBytes(request, 1)
	if err != nil {
		t.Fatal(err)
	}
	item, err := getBytes(list, 2)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := getInt(item, 1)
	if id != 2 {
		t.Fatal("unexpected contact operation")
	}
	buf, err := getBytes(item, 2)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := getBytes(buf, 2)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestContactEditOnlyChangesSelectedProperty(t *testing.T) {
	for _, pinned := range []bool{true, false} {
		req, projected, err := contactUpdatePayload(contactEditFixture(), "alice", ContactChange{Pinned: &pinned})
		if err != nil {
			t.Fatal(err)
		}
		update := contactEditCommand(t, req)
		mask, _ := getInt(update, 7)
		value, _ := getInt(update, 8)
		if mask != 0x800 || (value == 0x800) != pinned {
			t.Fatal("pin mutation changed unrelated flag bits")
		}
		for _, n := range []uint64{10, 17} {
			if v, err := optionalBytes(update, n); err != nil || v != nil {
				t.Fatal("pin mutation included unrelated optional properties")
			}
		}
		contact, err := DecodeContact(projected)
		if err != nil || contact.Pinned() != pinned || contact.Remark != "Old remark" || contact.Flags&0x201 != 0x201 {
			t.Fatal("local projection lost other contact properties")
		}
	}
	for _, remark := range []string{"New remark", "", "朋友"} {
		req, projected, err := contactUpdatePayload(contactEditFixture(), "alice", ContactChange{Remark: &remark})
		if err != nil {
			t.Fatal(err)
		}
		update := contactEditCommand(t, req)
		mask, _ := getInt(update, 7)
		if mask != 0 {
			t.Fatal("remark mutation would change contact flags")
		}
		contact, err := DecodeContact(projected)
		if err != nil || contact.Remark != remark || contact.Flags != 0xa01 {
			t.Fatal("remark or flags not preserved")
		}
	}
	value := "x"
	if _, _, err := contactUpdatePayload(contactEditFixture(), "bob", ContactChange{Remark: &value}); err == nil {
		t.Fatal("accepted wrong contact identity")
	}
}

func TestContactEditValidatesServerResult(t *testing.T) {
	for _, statuses := range [][]byte{pb(2, []byte{0}), pv(2, 0)} {
		if err := requireContactUpdateSuccess(message(pv(1, 0), pb(2, message(pv(1, 1), statuses)))); err != nil {
			t.Fatal(err)
		}
	}
	for _, response := range [][]byte{
		nil,
		message(pv(1, 1)),
		message(pv(1, 0), pb(2, message(pv(1, 1), pb(2, []byte{5})))),
		message(pv(1, 0), pb(2, message(pv(1, 1), pb(2, []byte{0, 0})))),
		message(pv(1, 0), pb(2, message(pv(1, 1), pb(2, []byte{0x80})))),
		message(pv(1, 0), pb(2, pv(1, 1))),
	} {
		if err := requireContactUpdateSuccess(response); err == nil {
			t.Fatal("accepted missing or unsuccessful per-operation status")
		}
	}
	for _, remark := range []string{strings.Repeat("a", 51), "hello\nthere", "\xff"} {
		if err := (ContactChange{Remark: &remark}).Validate("alice"); err == nil {
			t.Fatal("invalid remark accepted")
		}
	}
}
