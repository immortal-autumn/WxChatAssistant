package native

import (
	"encoding/binary"
	"fmt"
	"time"
)

// Profiles are saved with an attempt/account; they must never be selected by
// changing process-wide globals while another account is in use.
const (
	DesktopProfile       = "desktop"
	PhoneProfile         = "phone"
	AndroidTabletProfile = "android-tablet"
)

type protocolProfile struct {
	name       string
	version    int64
	deviceType string
	deviceID   uint64
}

func profileFor(name string) (protocolProfile, error) {
	switch name {
	case "", DesktopProfile:
		return protocolProfile{DesktopProfile, -227271398, "UnifiedPCLinux x86_64", 39}, nil
	case "tablet", AndroidTabletProfile:
		// Version from the inspected official 8.0.79 APK; API 34 is an
		// experimental tablet OS profile, not a claim of device attestation.
		return protocolProfile{AndroidTabletProfile, 0x28004f30, "pad-android-34", 28}, nil
	case PhoneProfile:
		return protocolProfile{PhoneProfile, 0x28004f30, "android-34", 2}, nil
	default:
		return protocolProfile{}, fmt.Errorf("unsupported native profile %q", name)
	}
}
func (p protocolProfile) baseRequest(device []byte, uin, task uint64) []byte {
	return message(pb(1, []byte{0}), pv(2, uin), pb(3, device), pv(4, uint64(p.version)), pb(5, []byte(p.deviceType)), pv(6, task))
}
func (p protocolProfile) pollTimestamp(now time.Time) uint64 {
	if p.name != DesktopProfile {
		return uint64(now.Unix())
	}
	return (uint64(now.UnixMilli()) & 0xffffffff) / 1000
}
func (p protocolProfile) preloginHeader(cookie []byte, cgi, length, keyVersion uint64) []byte {
	version, device := uint32(0), uint64(0)
	if p.name != DesktopProfile {
		version, device = uint32(p.version), p.deviceID
	}
	header := binary.BigEndian.AppendUint32(nil, version)
	header = binary.BigEndian.AppendUint32(header, 0)
	header = append(header, cookie...)
	for _, v := range []uint64{cgi, length, length, keyVersion, device, 0} {
		header = binary.AppendUvarint(header, v)
	}
	return append(header, 0xff, 0, 0, 0, 0, 0, 1)
}

// NormalizeProfile accepts user-facing aliases while retaining existing saved profiles.
func NormalizeProfile(name string) (string, error) {
	p, err := profileFor(name)
	return p.name, err
}
