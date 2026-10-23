package native

import (
	"encoding/hex"
	"testing"
	"time"
)

func TestProfileHeaderAndTimestamp(t *testing.T) {
	tablet, err := profileFor(AndroidTabletProfile)
	if err != nil {
		t.Fatal(err)
	}
	// Independently specified wire order: version, UIN, CGI 502, two
	// lengths, key 10014, device type 28, checksum, and extension trailer.
	want := "28004f3000000000f60305059e4e1c00ff000000000001"
	if got := hex.EncodeToString(tablet.preloginHeader(nil, 502, 5, 10014)); got != want {
		t.Fatalf("tablet header: %s", got)
	}
	now := time.Unix(1800000000, 987000000)
	if tablet.pollTimestamp(now) != 1800000000 {
		t.Fatal("tablet inherited truncated desktop clock")
	}
	desktop, err := profileFor("") // Existing state has no profile field.
	if err != nil {
		t.Fatal(err)
	}
	if desktop.name != DesktopProfile || desktop.deviceID != 39 {
		t.Fatal("legacy account profile changed")
	}
	if desktop.pollTimestamp(now) != (uint64(now.UnixMilli())&0xffffffff)/1000 {
		t.Fatal("desktop clock changed")
	}
	if got := hex.EncodeToString(desktop.preloginHeader(nil, 502, 5, 10014)); got != "0000000000000000f60305059e4e0000ff000000000001" {
		t.Fatalf("desktop header changed: %s", got)
	}
}

func TestUnknownAccountProfileRejected(t *testing.T) {
	if _, err := profileFor("ipad"); err == nil {
		t.Fatal("unsupported profile silently downgraded")
	}
	if err := (&Account{Profile: "ipad"}).Validate(); err == nil {
		t.Fatal("accepted unsupported saved profile")
	}
}
