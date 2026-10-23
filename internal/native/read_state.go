package native

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
)

func readStateRequest(a *Account, m Message, requestID string) ([]byte, error) {
	if err := a.Validate(); err != nil {
		return nil, err
	}
	id, err := strconv.ParseUint(m.ID, 10, 64)
	if err != nil || id == 0 || m.Timestamp == 0 || m.Peer == "" || len(m.Peer) > 512 || strings.ContainsAny(m.Peer, "\x00\r\n") || requestID == "" || len(requestID) > 256 {
		return nil, errors.New("invalid read-state boundary")
	}
	argument, err := json.Marshal(map[string]string{"messageSvrId": m.ID, "MsgCreateTime": strconv.FormatUint(uint64(m.Timestamp), 10)})
	if err != nil {
		return nil, err
	}
	p, _ := profileFor(a.Profile)
	return message(pb(1, p.baseRequest(a.Device, uint64(a.UIN), 0)), pv(2, 1), pb(3, []byte(a.Username)), pb(4, []byte(m.Peer)), pb(5, []byte(requestID)), pb(8, message(pb(1, []byte("lastMessage")), pb(2, argument)))), nil
}

// MarkRead notifies the account's other devices through a frozen whole-thread
// boundary. This is account read-state synchronization, not a recipient receipt.
func (s *Session) MarkRead(ctx context.Context, a *Account, m Message, requestID string) error {
	request, err := readStateRequest(a, m, requestID)
	if err != nil {
		return err
	}
	response, err := s.authenticated(ctx, a, 0, 251, request)
	if err != nil {
		return err
	}
	return requireSuccess(response)
}
