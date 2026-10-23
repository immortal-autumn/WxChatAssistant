package native

import (
	"context"
	"encoding/xml"
	"errors"
	"strings"
)

func decodeRoomMembers(data []byte) (map[string]string, error) {
	result := map[string]string{}
	fs, err := fields(data)
	if err != nil {
		return nil, err
	}
	for _, f := range fs {
		if f.number != 2 {
			continue
		}
		if f.wire != 2 || len(result) >= 5000 {
			return nil, errors.New("invalid room member list")
		}
		user, err := getBytes(f.data, 1)
		if err != nil || len(user) == 0 {
			return nil, errors.New("room member has no identity")
		}
		nickname, err := optionalBytes(f.data, 2)
		if err != nil {
			return nil, err
		}
		display, err := optionalBytes(f.data, 3)
		if err != nil {
			return nil, err
		}
		name := string(display)
		if name == "" {
			name = string(nickname)
		}
		if _, ok := result[string(user)]; ok {
			return nil, errors.New("duplicate room member")
		}
		result[string(user)] = name
	}
	count, err := optionalInt(data, 1)
	if err != nil {
		return nil, err
	}
	if count != 0 && count != uint64(len(result)) {
		return nil, errors.New("room member count mismatch")
	}
	return result, nil
}
func (s *Session) RoomMembers(ctx context.Context, a *Account, room string) (map[string]string, error) {
	if !strings.HasSuffix(room, "@chatroom") || len(room) > 256 || strings.ContainsAny(room, "\x00\r\n") {
		return nil, errors.New("invalid group ID")
	}
	if err := a.Validate(); err != nil {
		return nil, err
	}
	p, _ := profileFor(a.Profile)
	request := message(pb(1, p.baseRequest(a.Device, uint64(a.UIN), 0)), pb(2, []byte(room)), pv(3, 0))
	response, err := s.authenticated(ctx, a, 0, 551, request)
	if err != nil {
		return nil, err
	}
	if err = requireSuccess(response); err != nil {
		return nil, err
	}
	echo, err := optionalBytes(response, 2)
	if err != nil {
		return nil, err
	}
	if len(echo) > 0 && string(echo) != room {
		return nil, errors.New("group response identity mismatch")
	}
	data, err := getBytes(response, 4)
	if err != nil {
		return nil, err
	}
	return decodeRoomMembers(data)
}
func contactRoomMembers(raw []byte) map[string]string {
	result := map[string]string{}
	fs, err := fields(raw)
	if err != nil {
		return result
	}
	for _, f := range fs {
		if f.number != 15 || f.wire != 2 {
			continue
		}
		user, err := getBytes(f.data, 1)
		if err != nil {
			continue
		}
		name, err := getBytes(f.data, 2)
		if err != nil {
			continue
		}
		u, err := optionalBytes(user, 1)
		if err != nil {
			continue
		}
		n, err := optionalBytes(name, 1)
		if err == nil && len(u) > 0 {
			result[string(u)] = string(n)
		}
	}
	if data, err := optionalBytes(raw, 43); err == nil && len(data) > 0 && len(data) < 256*1024 {
		var room struct {
			Members []struct {
				Username string `xml:"UserName,attr"`
				Display  string `xml:"DisplayName"`
			} `xml:"Member"`
		}
		if xml.Unmarshal(data, &room) == nil {
			for _, member := range room.Members {
				if member.Username != "" && member.Display != "" {
					result[member.Username] = member.Display
				}
			}
		}
	}
	return result
}
