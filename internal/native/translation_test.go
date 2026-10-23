package native

import (
	"bytes"
	"testing"
)

func TestTranslationWireAndResult(t *testing.T) {
	a := &Account{Host: "long.weixin.qq.com", Profile: AndroidTabletProfile, Device: bytes.Repeat([]byte{1}, 16), UIN: 42, Username: "me", RequestKey: bytes.Repeat([]byte{2}, 24), ResponseKey: bytes.Repeat([]byte{3}, 24)}
	req, err := translationRequest(a, "你好", "en")
	if err != nil {
		t.Fatal(err)
	}
	item, err := getBytes(req, 3)
	if err != nil {
		t.Fatal(err)
	}
	text, _ := getBytes(item, 2)
	scene, _ := getInt(item, 4)
	lang, _ := getBytes(req, 4)
	if string(text) != "你好" || scene != 17 || string(lang) != "en" {
		t.Fatal("translation request lost text, scene, or language")
	}
	response := func(id, code uint64) []byte {
		return message(pb(1, pv(1, 0)), pv(2, 1), pb(3, message(pv(1, id), pb(2, []byte("Hello")), pv(3, code))))
	}
	if got, err := decodeTranslation(response(1, 0)); err != nil || got != "Hello" {
		t.Fatalf("translation: %q %v", got, err)
	}
	for _, invalid := range [][]byte{nil, response(2, 0), response(1, 5)} {
		if _, err := decodeTranslation(invalid); err == nil {
			t.Fatal("accepted malformed or unsuccessful translation")
		}
	}
	for _, lang := range []string{"", "e", "en\n", "中文", "en/../../"} {
		if _, err := translationRequest(a, "Hello", lang); err == nil {
			t.Fatalf("accepted language %q", lang)
		}
	}
}
