package native

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"encoding/hex"
	"strings"
	"testing"
)

func pollFixture(t *testing.T, plain, key []byte) []byte {
	t.Helper()
	padding := aes.BlockSize - len(plain)%aes.BlockSize
	plain = append(bytes.Clone(plain), bytes.Repeat([]byte{byte(padding)}, padding)...)
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	encrypted := make([]byte, len(plain))
	cipher.NewCBCEncrypter(block, key).CryptBlocks(encrypted, plain)
	return message(pb(1, pv(1, 0)), pb(3, pb(1, buffer(encrypted))))
}

func TestApprovalRequiresCredentialsAndNoLANChallenge(t *testing.T) {
	key := bytes.Repeat([]byte{7}, 16)
	approved := message(pv(2, 2), pb(3, []byte("temporary grant")), pb(4, []byte("temporary proof")))
	status, c3, c4, err := decodeLoginPoll(pollFixture(t, approved, key), key)
	if err != nil || status != 2 || len(c3) == 0 || len(c4) == 0 {
		t.Fatalf("approval: %d %v", status, err)
	}
	if _, _, _, err = decodeLoginPoll(pollFixture(t, pv(2, 2), key), key); err == nil {
		t.Fatal("approved without credentials")
	}
	challenge := append(bytes.Clone(approved), pb(17, buffer(pv(1, 11)))...)
	if _, _, _, err = decodeLoginPoll(pollFixture(t, challenge, key), key); err == nil || !strings.Contains(err.Error(), "LAN") {
		t.Fatal("accepted unimplemented device challenge", err)
	}
	// The live service omits an empty SKBuffer's bytes field.
	empty := append(bytes.Clone(approved), pb(17, pv(1, 0))...)
	if _, _, _, err = decodeLoginPoll(pollFixture(t, empty, key), key); err != nil {
		t.Fatal(err)
	}
	if _, err = unbuffer(pv(1, 1)); err == nil {
		t.Fatal("accepted truncated nonempty buffer")
	}
}

func TestCursorMergeAndReset(t *testing.T) {
	prior := message(pv(1, 1), pb(2, message(pv(1, 3), pv(2, 0xffffffff))))
	next := message(pv(1, 2), pb(2, message(pv(1, 3), pv(2, 4))), pb(2, message(pv(1, 7), pv(2, 0))))
	merged, err := mergeCursor(prior, next, false)
	if err != nil {
		t.Fatal(err)
	}
	values, err := decodeCursor(merged)
	if err != nil || len(values) != 2 || values[3] != 0xffffffff {
		t.Fatalf("cursor regressed: %v %v", values, err)
	}
	if _, ok := values[7]; !ok {
		t.Fatal("dropped new zero-valued key")
	}
	reset, err := mergeCursor(prior, next, true)
	if err != nil {
		t.Fatal(err)
	}
	values, err = decodeCursor(reset)
	if err != nil || values[3] != 4 {
		t.Fatalf("cursor reset failed: %v %v", values, err)
	}
}

func TestSendReceiptIdentity(t *testing.T) {
	// Hand-encoded protobuf receipt: base=0, count=1, item status=0,
	// recipient="alice", client ID field 4=123, server ID field 8=456.
	raw, _ := hex.DecodeString("0a02080010011a10080012070a05616c696365207b40c803")
	id, err := decodeSendReceipt(raw, "alice", 123)
	if err != nil || id != "456" {
		t.Fatalf("receipt: %q %v", id, err)
	}
	if _, err = decodeSendReceipt(raw, "bob", 123); err == nil {
		t.Fatal("accepted wrong recipient")
	}
	if _, err = decodeSendReceipt(raw, "alice", 124); err == nil {
		t.Fatal("accepted wrong client ID")
	}
}

func TestSyncRetainsUnknownCommands(t *testing.T) {
	command := message(pv(1, 9999), pb(2, buffer([]byte{1, 2, 3})))
	response := message(pv(1, 0), pb(2, message(pv(1, 1), pb(2, command))), pv(3, 0), pb(4, buffer([]byte{0})))
	batch, err := decodeSync(response, nil)
	if err != nil || len(batch.Commands) != 1 || !bytes.Equal(batch.Commands[0].Payload, []byte{1, 2, 3}) {
		t.Fatalf("lost unknown sync payload: %v %v", batch, err)
	}
}
