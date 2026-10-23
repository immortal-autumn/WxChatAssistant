package native

import (
	"encoding/hex"
	"testing"
)

func TestDecodeContactNames(t *testing.T) {
	// Independent protobuf fixture: alice01, Alice, remark Sarah Z.
	raw, _ := hex.DecodeString("0a090a07616c696365303112070a05416c69636552090a075361726168205a")
	contact, err := DecodeContact(raw)
	if err != nil || contact.Username != "alice01" || contact.Nickname != "Alice" || contact.Remark != "Sarah Z" {
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
