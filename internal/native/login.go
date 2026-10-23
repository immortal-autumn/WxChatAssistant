package native

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"encoding/hex"
	"errors"
	"fmt"
	"time"
)

// LoginAttempt includes temporary approval material. Serialize it only to a
// private local state file, never to logs or ordinary command output.
type LoginAttempt struct {
	Host        string `json:"host"`
	Profile     string `json:"profile,omitempty"`
	Device      []byte `json:"device"`
	Image       []byte `json:"image"`
	Identifier  []byte `json:"identifier"`
	NotifyKey   []byte `json:"notify_key"`
	Cookie      []byte `json:"cookie"`
	Created     int64  `json:"created"`
	Status      uint32 `json:"status"`
	Credential3 []byte `json:"credential_3,omitempty"`
	Credential4 []byte `json:"credential_4,omitempty"`
}

func baseCode(data []byte) (int32, error) {
	base, err := getBytes(data, 1)
	if err != nil {
		return 0, err
	}
	code, err := getInt(base, 1)
	return int32(code), err
}
func requireSuccess(data []byte) error {
	code, err := baseCode(data)
	if err != nil {
		return err
	}
	if code != 0 {
		return fmt.Errorf("native API status %d", code)
	}
	return nil
}
func optionalBytes(data []byte, number uint64) ([]byte, error) {
	fs, err := fields(data)
	if err != nil {
		return nil, err
	}
	for _, f := range fs {
		if f.number == number {
			return getBytes(data, number)
		}
	}
	return nil, nil
}
func optionalInt(data []byte, number uint64) (uint64, error) {
	fs, err := fields(data)
	if err != nil {
		return 0, err
	}
	for _, f := range fs {
		if f.number == number {
			return getInt(data, number)
		}
	}
	return 0, nil
}
func optionalBuffer(data []byte, number uint64) ([]byte, error) {
	b, err := optionalBytes(data, number)
	if err != nil || b == nil {
		return b, err
	}
	return unbuffer(b)
}

func (s *Session) StartLogin(ctx context.Context, host string) (*LoginAttempt, error) {
	return s.StartLoginWithProfile(ctx, host, DesktopProfile)
}

// StartLoginWithProfile starts an isolated experimental tablet or desktop attempt.
func (s *Session) StartLoginWithProfile(ctx context.Context, host, name string) (*LoginAttempt, error) {
	return s.startLogin(ctx, host, name, 232)
}

// StartShortLoginProbe exercises the authenticated HTTP-carried MMTLS path.
func (s *Session) StartShortLoginProbe(ctx context.Context, host, name string) (*LoginAttempt, error) {
	return s.startLogin(ctx, host, name, 0)
}

func (s *Session) startLogin(ctx context.Context, host, name string, command uint32) (*LoginAttempt, error) {
	p, err := profileFor(name)
	if err != nil {
		return nil, err
	}
	device, err := randomBytes(16)
	if err != nil {
		return nil, err
	}
	if p.name == DesktopProfile {
		device = []byte("l" + hex.EncodeToString(device))
		device = device[:16]
	} else {
		device[0] = 0x49
	}
	key, err := randomBytes(16)
	if err != nil {
		return nil, err
	}
	request := message(pb(1, p.baseRequest(device, 0, 0)), pb(2, buffer(key)), pv(3, 0), pb(4, []byte("WeChat CLI")))
	response, cookie, err := s.preloginWithProfile(ctx, p, command, 502, request, nil)
	if err != nil {
		return nil, err
	}
	if err = requireSuccess(response); err != nil {
		return nil, err
	}
	image, err := optionalBuffer(response, 2)
	if err != nil {
		return nil, err
	}
	if len(image) < 8 || (!bytes.Equal(image[:8], []byte("\x89PNG\r\n\x1a\n")) && !bytes.Equal(image[:3], []byte{0xff, 0xd8, 0xff})) {
		return nil, errors.New("native login returned no PNG/JPEG QR")
	}
	id, err := getBytes(response, 3)
	if err != nil {
		return nil, err
	}
	notify, err := optionalBuffer(response, 5)
	if err != nil {
		return nil, err
	}
	if len(id) == 0 || len(id) > 4096 || len(notify) != 16 {
		return nil, errors.New("invalid native QR state")
	}
	return &LoginAttempt{Host: host, Profile: p.name, Device: device, Image: image, Identifier: id, NotifyKey: notify, Cookie: cookie, Created: time.Now().Unix()}, nil
}
func (s *Session) ProbeQRCode() ([]byte, error) {
	attempt, err := s.StartLogin(context.Background(), "long.weixin.qq.com")
	if err != nil {
		return nil, err
	}
	return attempt.Image, nil
}

func decryptCBC(data, key []byte) ([]byte, error) {
	if len(key) < 16 || len(data) == 0 || len(data)%16 != 0 {
		return nil, errors.New("invalid native CBC input")
	}
	block, err := aes.NewCipher(key[:16])
	if err != nil {
		return nil, err
	}
	plain := make([]byte, len(data))
	cipher.NewCBCDecrypter(block, key[:16]).CryptBlocks(plain, data)
	padding := int(plain[len(plain)-1])
	if padding < 1 || padding > 16 || padding > len(plain) {
		return nil, errors.New("invalid native CBC padding")
	}
	for _, v := range plain[len(plain)-padding:] {
		if int(v) != padding {
			return nil, errors.New("invalid native CBC padding")
		}
	}
	return plain[:len(plain)-padding], nil
}

func decodeLoginPoll(response, notifyKey []byte) (uint32, []byte, []byte, error) {
	if err := requireSuccess(response); err != nil {
		return 0, nil, nil, err
	}
	pkg, err := optionalBytes(response, 3)
	if err != nil {
		return 0, nil, nil, err
	}
	if pkg == nil {
		return 0, nil, nil, nil
	}
	ciphertext, err := optionalBuffer(pkg, 1)
	if err != nil {
		return 0, nil, nil, fmt.Errorf("decode native poll ciphertext: %w", err)
	}
	plain, err := decryptCBC(ciphertext, notifyKey)
	if err != nil {
		return 0, nil, nil, err
	}
	status, err := optionalInt(plain, 2)
	if err != nil {
		return 0, nil, nil, err
	}
	ext, err := optionalBuffer(plain, 17)
	if err != nil {
		return 0, nil, nil, err
	}
	if ext != nil {
		risk, err := optionalInt(ext, 1)
		if err != nil {
			return 0, nil, nil, err
		}
		if risk == 11 {
			return uint32(status), nil, nil, errors.New("native login requires phone/LAN device verification; this challenge is not implemented yet")
		}
	}
	c3, err := optionalBytes(plain, 3)
	if err != nil {
		return 0, nil, nil, err
	}
	c4, err := optionalBytes(plain, 4)
	if err != nil {
		return 0, nil, nil, err
	}
	if status == 2 && (len(c3) == 0 || len(c4) == 0 || len(c3) > 65536 || len(c4) > 65536) {
		return 0, nil, nil, errors.New("native approval is missing credentials")
	}
	if status > 2 {
		return uint32(status), nil, nil, fmt.Errorf("phone rejected or expired native login (status %d)", status)
	}
	return uint32(status), c3, c4, nil
}
func (s *Session) PollLogin(ctx context.Context, a *LoginAttempt) error {
	if a == nil || len(a.Device) != 16 || len(a.Identifier) == 0 || len(a.NotifyKey) != 16 || a.Created <= 0 || time.Now().Unix()-a.Created > 900 {
		return errors.New("invalid or expired native login attempt")
	}
	if a.Status == 2 {
		return nil
	}
	key, err := randomBytes(16)
	if err != nil {
		return err
	}
	p, err := profileFor(a.Profile)
	if err != nil {
		return err
	}
	timestamp := p.pollTimestamp(time.Now())
	request := message(pb(1, p.baseRequest(a.Device, 0, 0)), pb(2, buffer(key)), pb(3, a.Identifier), pv(4, timestamp), pv(5, 0))
	response, cookie, err := s.preloginWithProfile(ctx, p, 233, 503, request, a.Cookie)
	if err != nil {
		return fmt.Errorf("native poll transport: %w", err)
	}
	status, c3, c4, err := decodeLoginPoll(response, a.NotifyKey)
	if err != nil {
		return fmt.Errorf("native poll notification: %w", err)
	}
	if len(cookie) > 0 {
		a.Cookie = cookie
	}
	if status > a.Status {
		a.Status = status
	}
	a.Credential3 = c3
	a.Credential4 = c4
	return nil
}
