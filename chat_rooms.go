package main

import (
	"context"
	"errors"
	"os"
	"time"

	"github.com/immortal-autumn/WxChatAssistant/internal/native"
)

type chatRoomResult struct {
	Peer    string
	Members map[string]string
	Err     error
}
type chatRoomEntry struct {
	Members map[string]string `json:"members"`
	Fetched int64             `json:"fetched"`
}
type chatRoomCache struct {
	Owner string                   `json:"owner"`
	Rooms map[string]chatRoomEntry `json:"rooms"`
}
type roomBackend interface {
	room(context.Context, string) chatRoomResult
}

func (c *chatService) room(ctx context.Context, peer string) chatRoomResult {
	result := chatRoomResult{Peer: peer}
	lock, err := c.store.lock(ctx)
	if err != nil {
		result.Err = err
		return result
	}
	defer lock.Close()
	var cache chatRoomCache
	if err = c.store.load("rooms.json", &cache); err != nil && !errors.Is(err, os.ErrNotExist) {
		result.Err = err
		return result
	}
	if cache.Owner != c.owner || cache.Rooms == nil {
		cache = chatRoomCache{Owner: c.owner, Rooms: map[string]chatRoomEntry{}}
	}
	if entry, ok := cache.Rooms[peer]; ok && time.Now().Unix()-entry.Fetched < 24*60*60 {
		result.Members = entry.Members
		return result
	}
	state, err := c.store.state()
	if err != nil {
		result.Err = err
		return result
	}
	if state.Account == nil || state.Account.Username != c.owner {
		result.Err = errors.New("account changed")
		return result
	}
	session, err := native.Connect(ctx, state.Account.Host)
	if err != nil {
		result.Err = err
		return result
	}
	defer session.Close()
	result.Members, result.Err = session.RoomMembers(ctx, state.Account, peer)
	if result.Err != nil {
		return result
	}
	cache.Rooms[peer] = chatRoomEntry{Members: result.Members, Fetched: time.Now().Unix()}
	if err = c.store.save("rooms.json", cache); err != nil {
		result.Err = err
		return result
	}
	result.Err = c.store.save("state.json", state)
	return result
}
func (m chatModel) senderName(room, sender string) string {
	if sender == m.owner {
		return "You"
	}
	if sender == "" {
		return "Group member"
	}
	if c, ok := m.contacts[sender]; ok && oneLine(c.Remark) != "" {
		return oneLine(c.Remark)
	}
	if name := oneLine(m.contacts[room].Members[sender]); name != "" {
		return name
	}
	return m.name(sender)
}

// Contact remarks are local/private. Outgoing quotes use public nicknames or
// group display names rather than transmitting the user's personal remark.
func publicQuoteName(contacts map[string]native.Contact, target native.Message) string {
	sender := target.Sender
	if sender == "" {
		sender = target.From
	}
	if name := oneLine(contacts[target.Peer].Members[sender]); name != "" {
		return name
	}
	if name := oneLine(contacts[sender].Nickname); name != "" {
		return name
	}
	return oneLine(sender)
}
