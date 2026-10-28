package main

import (
	"context"
	"errors"
	"fmt"

	"github.com/immortal-autumn/WxChatAssistant/internal/native"
)

type chatContactResult struct {
	Peer     string
	Contact  native.Contact
	Accepted bool
	Err      error
}

type contactBackend interface {
	updateContact(context.Context, string, native.ContactChange) chatContactResult
}

func (c *chatService) updateContact(ctx context.Context, peer string, change native.ContactChange) chatContactResult {
	result := chatContactResult{Peer: peer}
	if result.Err = change.Validate(peer); result.Err != nil {
		return result
	}
	lock, err := c.store.lock(ctx)
	if err != nil {
		result.Err = err
		return result
	}
	defer lock.Close()
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
	raw, err := session.UpdateContact(ctx, state.Account, peer, change)
	if err != nil {
		result.Err = err
		return result
	}
	result.Accepted = true
	result.Contact, result.Err = native.DecodeContact(raw)
	if result.Err != nil {
		return result
	}
	if err = c.store.journal(nativeJournal{Owner: c.owner, Batch: native.SyncBatch{Commands: []native.SyncCommand{{ID: 2, Payload: raw}}}}); err != nil {
		result.Err = fmt.Errorf("accepted by WeChat, but saving contact failed: %w", err)
		return result
	}
	if err = c.store.save("state.json", state); err != nil {
		result.Err = fmt.Errorf("accepted by WeChat, but saving session failed: %w", err)
	}
	return result
}
