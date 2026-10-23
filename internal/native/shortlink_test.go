package native

import (
	"bytes"
	"testing"
)

func shortReplyFixture(t *testing.T, wrongFinished bool) ([]byte, []byte, []byte, []byte) {
	t.Helper()
	psk := bytes.Repeat([]byte{9}, 32)
	hello := section([]byte("client hello"))
	extensions := section([]byte("client extensions"))
	server := section(message([]byte{2, 4, 0xf1, 0, 0xa8}, bytes.Repeat([]byte{5}, 32), section([]byte{0})))
	transcript := message(hello, extensions, server)
	keys := expand(psk, append([]byte("handshake key expansion"), digest(transcript)...), 28)
	finished := mac(expand(psk, []byte("server finished"), 32), digest(transcript))
	if wrongFinished {
		finished[0] ^= 1
	}
	wire, err := frame(0x16, server)
	if err != nil {
		t.Fatal(err)
	}
	parts := []struct {
		kind byte
		body []byte
	}{{0x16, section(append([]byte{0x14}, shortSection(finished)...))}, {0x17, []byte("verified ")}, {0x17, []byte("reply")}, {0x15, section([]byte{0, 1, 1})}}
	for i, p := range parts {
		record, err := sealRecord(p.kind, p.body, keys[:16], keys[16:], uint64(i+1))
		if err != nil {
			t.Fatal(err)
		}
		wire = append(wire, record...)
	}
	return wire, psk, hello, extensions
}
func TestShortlinkAuthenticatedReply(t *testing.T) {
	wire, psk, hello, ext := shortReplyFixture(t, false)
	plain, err := decodeShortReply(wire, psk, hello, ext)
	if err != nil || string(plain) != "verified reply" {
		t.Fatalf("shortlink: %q %v", plain, err)
	}
	wire[len(wire)-1] ^= 1
	if _, err = decodeShortReply(wire, psk, hello, ext); err == nil {
		t.Fatal("accepted tampered shortlink")
	}
	wire, psk, hello, ext = shortReplyFixture(t, true)
	if _, err = decodeShortReply(wire, psk, hello, ext); err == nil {
		t.Fatal("accepted invalid server Finished despite valid record tags")
	}
}
func TestAttachmentChunkBounds(t *testing.T) {
	response := func(start, size, declared uint64) []byte {
		return message(pb(1, pv(1, 0)), pv(5, size), pv(6, start), pv(7, declared), pb(8, buffer([]byte("data"))))
	}
	data, size, err := validateAttachmentChunk(response(4, 8, 4), 4, 8)
	if err != nil || string(data) != "data" || size != 8 {
		t.Fatal(size, err)
	}
	for _, raw := range [][]byte{response(0, 8, 4), response(4, 8, 5), response(4, 7, 4), response(4, MaxAttachmentBytes+1, 4)} {
		if _, _, err = validateAttachmentChunk(raw, 4, 8); err == nil {
			t.Fatal("accepted invalid attachment range")
		}
	}
}
func TestReadStateFrozenBoundary(t *testing.T) {
	a := &Account{Host: "long.weixin.qq.com", Profile: AndroidTabletProfile, Device: bytes.Repeat([]byte{1}, 16), UIN: 42, Username: "me", RequestKey: bytes.Repeat([]byte{2}, 24), ResponseKey: bytes.Repeat([]byte{3}, 24)}
	m := Message{Peer: "alice", ID: "18446744073709551615", Timestamp: 1700000000}
	request, err := readStateRequest(a, m, "stable-request")
	if err != nil {
		t.Fatal(err)
	}
	peer, err := getBytes(request, 4)
	if err != nil || string(peer) != "alice" {
		t.Fatal("wrong read target")
	}
	argument, _ := getBytes(request, 8)
	value, _ := getBytes(argument, 2)
	if !bytes.Contains(value, []byte(`"messageSvrId":"18446744073709551615"`)) || !bytes.Contains(value, []byte(`"MsgCreateTime":"1700000000"`)) {
		t.Fatal("read boundary lost precision", string(value))
	}
	m.ID = "local-id"
	if _, err = readStateRequest(a, m, "stable-request"); err == nil {
		t.Fatal("accepted a nonserver read boundary")
	}
}
