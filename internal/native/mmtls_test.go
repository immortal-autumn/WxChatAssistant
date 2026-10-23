package native

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"testing"
)

func TestHKDFRFC5869(t *testing.T) {
	key, _ := hex.DecodeString("077709362c2e32df0ddc3f0dc47bba6390b6c73bb50f9c3122ec844ad7c2b3e5")
	info, _ := hex.DecodeString("f0f1f2f3f4f5f6f7f8f9")
	got := hex.EncodeToString(expand(key, info, 42))
	if got != "3cb25f25faacd57a90434f64d0362f2a2d2d0a90cf1a5a4c5db02d56ecc4c5bf34007208d5b887185865" {
		t.Fatal(got)
	}
}
func TestRecordAuthenticationAndSequence(t *testing.T) {
	key := bytes.Repeat([]byte{3}, 16)
	iv := bytes.Repeat([]byte{7}, 12)
	record, err := sealRecord(0x17, []byte("synthetic native request"), key, iv, 4)
	if err != nil {
		t.Fatal(err)
	}
	plain, err := openRecord(record, key, iv, 4)
	if err != nil || string(plain) != "synthetic native request" {
		t.Fatalf("record roundtrip: %q %v", plain, err)
	}
	if _, err = openRecord(record, key, iv, 5); err == nil {
		t.Fatal("accepted wrong sequence")
	}
	for _, position := range []int{0, 5, len(record) - 1} {
		bad := append([]byte{}, record...)
		bad[position] ^= 1
		if _, err = openRecord(bad, key, iv, 4); err == nil {
			t.Fatal("accepted modified record")
		}
	}
	if _, err = recordNonce(nil, 1); err == nil {
		t.Fatal("accepted invalid IV")
	}
}
func TestPinnedSignatureRejectsWrongKeyAndModifiedData(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	public := string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}))
	data := digest([]byte("synthetic handshake transcript"))
	signature, err := ecdsa.SignASN1(rand.Reader, key, digest(data))
	if err != nil {
		t.Fatal(err)
	}
	if err = verifySignature(public, data, signature); err != nil {
		t.Fatal(err)
	}
	if err = verifySignature(serverPins[5], data, signature); err == nil {
		t.Fatal("accepted unpinned peer")
	}
	if err = verifySignature(public, []byte("changed"), signature); err == nil {
		t.Fatal("accepted modified transcript")
	}
}
func TestIndependentBusinessEnvelopeFixture(t *testing.T) {
	fixture, err := hex.DecodeString("9e65093555920046fdfa90d437e2f0b922d46623768c7870b390381087bb0d11fa5af671c9b301db000102030405060708090a0b0d4ecbc764b639df9549f03800165ea6")
	if err != nil {
		t.Fatal(err)
	}
	key := make([]byte, 24)
	for i := range key {
		key[i] = byte(i)
	}
	got, err := openEnvelope(fixture, key, nil)
	if err != nil || string(got) != "synthetic native session request" {
		t.Fatalf("known fixture: %q %v", got, err)
	}
	fixture[0] ^= 1
	if _, err = openEnvelope(fixture, key, nil); err == nil {
		t.Fatal("accepted modified business envelope")
	}
}
func TestNativeParserBounds(t *testing.T) {
	for _, input := range [][]byte{nil, {0}, {0, 0, 0, 8, 1}, {0xff, 0xff, 0xff, 0xff}} {
		if _, err := parseHello(input); err == nil {
			t.Fatal("accepted truncated hello")
		}
	}
	for _, input := range [][]byte{{0x12, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff}, {0x12, 4, 1}, {0}, {0x0b}} {
		if _, err := fields(input); err == nil {
			t.Fatal("accepted malformed protobuf")
		}
	}
	if _, err := getInt(message(pv(1, 1), pv(1, 2)), 1); err == nil {
		t.Fatal("accepted duplicate field")
	}
	if _, err := unbuffer(message(pv(1, 10), pb(2, []byte{1}))); err == nil {
		t.Fatal("accepted mismatched buffer length")
	}
}
func TestNativeHostRestrictions(t *testing.T) {
	for _, host := range []string{"long.weixin.qq.com", "sgshort.wechat.com"} {
		if !validHost(host) {
			t.Fatal(host)
		}
	}
	for _, host := range []string{"evil.example", "weixin.qq.com.evil.example", "long.weixin.qq.com:80", "user@long.weixin.qq.com", "long.weixin.qq.com/"} {
		if validHost(host) {
			t.Fatal(host)
		}
	}
}
