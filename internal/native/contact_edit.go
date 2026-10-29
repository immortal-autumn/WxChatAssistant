package native

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"unicode"
	"unicode/utf8"
)

// ContactChange changes exactly one property of an existing WeChat contact.
type ContactChange struct {
	Remark *string
	Pinned *bool
}

func (c ContactChange) Validate(peer string) error {
	if peer == "" || len(peer) > 512 || !utf8.ValidString(peer) || containsControl(peer) {
		return errors.New("invalid contact ID")
	}
	if (c.Remark == nil) == (c.Pinned == nil) {
		return errors.New("change exactly one contact property")
	}
	if c.Remark != nil && (!utf8.ValidString(*c.Remark) || utf8.RuneCountInString(*c.Remark) > 50 || containsControl(*c.Remark)) {
		return errors.New("remarks must contain at most 50 characters without control characters")
	}
	return nil
}

func containsControl(s string) bool {
	for _, r := range s {
		if unicode.IsControl(r) {
			return true
		}
	}
	return false
}

// Oplog returns an outer status and one packed int32 status per operation.
func requireContactUpdateSuccess(response []byte) error {
	code, err := getInt(response, 1)
	if err != nil {
		return err
	}
	if int32(code) != 0 {
		return fmt.Errorf("contact update status %d", int32(code))
	}
	result, err := getBytes(response, 2)
	if err != nil {
		return err
	}
	count, err := getInt(result, 1)
	if err != nil || count != 1 {
		return errors.New("unexpected contact update result count")
	}
	fs, err := fields(result)
	if err != nil {
		return err
	}
	var codes []uint64
	for _, f := range fs {
		if f.number != 2 {
			continue
		}
		switch f.wire {
		case 0:
			codes = append(codes, f.scalar)
		case 2:
			for data := f.data; len(data) > 0; {
				v, n := binary.Uvarint(data)
				if n <= 0 {
					return errors.New("invalid packed contact update status")
				}
				codes = append(codes, v)
				data = data[n:]
			}
		default:
			return errors.New("invalid contact update status type")
		}
	}
	if len(codes) != 1 {
		return errors.New("missing or ambiguous contact update status")
	}
	if int32(codes[0]) != 0 {
		return fmt.Errorf("WeChat rejected contact update: status %d", int32(codes[0]))
	}
	return nil
}

func replaceContactFields(raw []byte, changes map[uint64][]byte) ([]byte, error) {
	fs, err := fields(raw)
	if err != nil {
		return nil, err
	}
	var result []byte
	for _, f := range fs {
		if _, replaced := changes[f.number]; replaced {
			continue
		}
		switch f.wire {
		case 0:
			result = append(result, pv(f.number, f.scalar)...)
		case 2:
			result = append(result, pb(f.number, f.data)...)
		default:
			return nil, errors.New("unsupported contact field encoding")
		}
	}
	// Stable order keeps journal fixtures and request construction deterministic.
	for _, number := range []uint64{7, 8, 10} {
		result = append(result, changes[number]...)
	}
	return result, nil
}

func contactUpdatePayload(raw []byte, peer string, change ContactChange) ([]byte, []byte, error) {
	if err := change.Validate(peer); err != nil {
		return nil, nil, err
	}
	contact, err := DecodeContact(raw)
	if err != nil || contact.Username != peer {
		return nil, nil, errors.New("contact response identity mismatch")
	}
	// Carry the required ModContact identity fields from a fresh server fetch.
	// Optional properties are omitted unless explicitly edited. In particular,
	// a pin change must never overwrite the contact's remark or notification state.
	var update []byte
	for n := uint64(1); n <= 9; n++ {
		if n == 7 || n == 8 {
			continue
		}
		if n == 5 || n == 9 {
			v, err := optionalInt(raw, n)
			if err != nil {
				return nil, nil, err
			}
			update = append(update, pv(n, v)...)
		} else {
			v, err := optionalBytes(raw, n)
			if err != nil {
				return nil, nil, err
			}
			update = append(update, pb(n, v)...)
		}
	}
	changes := map[uint64][]byte{}
	mask, value := uint64(0), uint64(0)
	if change.Pinned != nil {
		mask = 0x800
		if *change.Pinned {
			value = mask
		}
	} else {
		changes[10] = pb(10, pb(1, []byte(*change.Remark)))
		update = append(update, changes[10]...)
	}
	update = append(update, message(pv(7, mask), pv(8, value))...)
	// The local journal keeps the full fetched contact with only the requested
	// property changed, and preserves its other flags.
	changes[7] = pv(7, uint64(contact.FlagMask)|mask)
	changes[8] = pv(8, (uint64(contact.Flags)&^mask)|value)
	projected, err := replaceContactFields(raw, changes)
	if err != nil {
		return nil, nil, err
	}
	cmd := message(pv(1, 2), pb(2, buffer(update)))
	request := pb(1, message(pv(1, 1), pb(2, cmd)))
	return request, projected, nil
}

// FetchContact reads an existing contact or group without changing its properties.
// The raw ModContact is returned so callers can persist it in the sync journal.
func (s *Session) FetchContact(ctx context.Context, a *Account, peer string) ([]byte, error) {
	if peer == "" || len(peer) > 512 || !utf8.ValidString(peer) || containsControl(peer) {
		return nil, errors.New("invalid contact ID")
	}
	if err := a.Validate(); err != nil {
		return nil, err
	}
	p, _ := profileFor(a.Profile)
	request := message(pb(1, p.baseRequest(a.Device, uint64(a.UIN), 0)), pv(2, 1), pb(3, pb(1, []byte(peer))))
	response, err := s.authenticated(ctx, a, 0, 182, request)
	if err != nil {
		return nil, err
	}
	return decodeContactLookup(response, peer)
}

func decodeContactLookup(response []byte, peer string) ([]byte, error) {
	var err error
	if err = requireSuccess(response); err != nil {
		return nil, err
	}
	count, err := getInt(response, 2)
	if err != nil || count != 1 {
		return nil, errors.New("contact lookup did not return exactly one contact")
	}
	fs, err := fields(response)
	if err != nil {
		return nil, err
	}
	var codes []uint64
	for _, f := range fs {
		if f.number != 4 {
			continue
		}
		switch f.wire {
		case 0:
			codes = append(codes, f.scalar)
		case 2:
			for data := f.data; len(data) > 0; {
				code, n := binary.Uvarint(data)
				if n <= 0 {
					return nil, errors.New("invalid contact lookup status")
				}
				codes = append(codes, code)
				data = data[n:]
			}
		default:
			return nil, errors.New("invalid contact lookup status type")
		}
	}
	if len(codes) > 1 {
		return nil, errors.New("unexpected contact lookup status count")
	}
	if len(codes) == 1 && int32(codes[0]) != 0 {
		return nil, fmt.Errorf("contact lookup returned status %d", int32(codes[0]))
	}
	raw, err := getBytes(response, 3)
	if err != nil {
		return nil, err
	}
	contact, err := DecodeContact(raw)
	if err != nil {
		return nil, fmt.Errorf("invalid contact lookup result: %w", err)
	}
	if contact.Username != peer {
		return nil, errors.New("contact response identity mismatch")
	}
	return raw, nil
}

func (s *Session) UpdateContact(ctx context.Context, a *Account, peer string, change ContactChange) ([]byte, error) {
	if err := change.Validate(peer); err != nil {
		return nil, err
	}
	raw, err := s.FetchContact(ctx, a, peer)
	if err != nil {
		return nil, err
	}
	request, projected, err := contactUpdatePayload(raw, peer, change)
	if err != nil {
		return nil, err
	}
	response, err := s.authenticated(ctx, a, 0, 681, request)
	if err != nil {
		return nil, fmt.Errorf("contact update outcome unknown; check WeChat before retrying: %w", err)
	}
	if err = requireContactUpdateSuccess(response); err != nil {
		return nil, err
	}
	return projected, nil
}
