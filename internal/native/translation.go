package native

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

func ValidTranslationLanguage(lang string) bool {
	if len(lang) < 2 || len(lang) > 16 {
		return false
	}
	for _, r := range lang {
		if !(r >= 'a' && r <= 'z') && !(r >= 'A' && r <= 'Z') && r != '_' && r != '-' {
			return false
		}
	}
	return true
}
func translationRequest(a *Account, text, lang string) ([]byte, error) {
	if err := a.Validate(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(text) == "" || len(text) > 16*1024 || !ValidTranslationLanguage(lang) {
		return nil, errors.New("invalid translation text or target language")
	}
	p, _ := profileFor(a.Profile)
	// The official Android WebTransDirectRequester uses scene 17, item ID 1,
	// and the original text in item field 2. No chat message is sent.
	item := message(pv(1, 1), pb(2, []byte(text)), pv(4, 17))
	return message(pb(1, p.baseRequest(a.Device, uint64(a.UIN), 0)), pv(2, 1), pb(3, item), pb(4, []byte(lang))), nil
}
func decodeTranslation(response []byte) (string, error) {
	if err := requireSuccess(response); err != nil {
		return "", err
	}
	count, err := getInt(response, 2)
	if err != nil || count != 1 {
		return "", errors.New("unexpected translation result count")
	}
	item, err := getBytes(response, 3)
	if err != nil {
		return "", err
	}
	id, err := getInt(item, 1)
	if err != nil || id != 1 {
		return "", errors.New("translation result identity mismatch")
	}
	code, err := getInt(item, 3)
	if err != nil {
		return "", err
	}
	if int32(code) != 0 {
		return "", fmt.Errorf("WeChat translation returned status %d", int32(code))
	}
	text, err := getBytes(item, 2)
	if err != nil {
		return "", err
	}
	if len(text) == 0 {
		return "", errors.New("WeChat returned an empty translation")
	}
	return string(text), nil
}
func (s *Session) Translate(ctx context.Context, a *Account, text, lang string) (string, error) {
	request, err := translationRequest(a, text, lang)
	if err != nil {
		return "", err
	}
	response, err := s.authenticated(ctx, a, 0, 631, request)
	if err != nil {
		return "", err
	}
	return decodeTranslation(response)
}
