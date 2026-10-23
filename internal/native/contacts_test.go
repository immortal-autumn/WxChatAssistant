package native

import (
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
