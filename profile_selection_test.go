package main

import (
	"bytes"
	"github.com/immortal-autumn/WxChatAssistant/internal/native"
	"path/filepath"
	"testing"
)

func TestProfilesUseSeparateAccountDirectories(t *testing.T) {
	config := t.TempDir()
	base := Store{filepath.Join(config, "wechat-cli")}
	tablet, err := profileStore(base, config, "tablet", false)
	if err != nil {
		t.Fatal(err)
	}
	phone, err := profileStore(base, config, "phone", false)
	if err != nil {
		t.Fatal(err)
	}
	desktop, err := profileStore(base, config, "desktop", false)
	if err != nil {
		t.Fatal(err)
	}
	if tablet.Dir == phone.Dir || tablet.Dir == desktop.Dir || phone.Dir == desktop.Dir {
		t.Fatal("profiles share account state")
	}
	s, err := openNativeStore(tablet)
	if err != nil {
		t.Fatal(err)
	}
	a := &native.Account{Profile: native.AndroidTabletProfile, Host: "long.weixin.qq.com", Username: "me", UIN: 42, Device: bytes.Repeat([]byte{1}, 16), RequestKey: bytes.Repeat([]byte{2}, 24), ResponseKey: bytes.Repeat([]byte{3}, 24)}
	if err = s.save("state.json", nativeState{Account: a}); err != nil {
		t.Fatal(err)
	}
	selectedPhone, err := profileStore(tablet, config, "phone", true)
	if err != nil || selectedPhone.Dir == tablet.Dir {
		t.Fatal("phone would overwrite an explicit tablet config", err)
	}
	found, err := profileStore(base, config, "android-tablet", false)
	if err != nil || found.Dir != tablet.Dir {
		t.Fatal("tablet alias did not reuse saved profile", found, err)
	}
}
