package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/immortal-autumn/WxChatAssistant/internal/native"
)

type chatReadResult struct {
	Peer, MessageID string
	Accepted        bool
	Err             error
}
type chatReadReceipt struct {
	MessageID string `json:"message_id"`
	Timestamp uint32 `json:"timestamp"`
	State     string `json:"state"`
}
type chatReadState struct {
	Owner    string                     `json:"owner"`
	Receipts map[string]chatReadReceipt `json:"receipts"`
}

func (c *chatService) markRead(ctx context.Context, m native.Message) chatReadResult {
	result := chatReadResult{Peer: m.Peer, MessageID: m.ID}
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
	var saved chatReadState
	if err = c.store.load("reads.json", &saved); err != nil && !errors.Is(err, os.ErrNotExist) {
		result.Err = err
		return result
	}
	if saved.Owner != c.owner || saved.Receipts == nil {
		saved = chatReadState{Owner: c.owner, Receipts: map[string]chatReadReceipt{}}
	}
	key := fmt.Sprintf("%x", sha256.Sum256([]byte(fmt.Sprintf("%s/%s/%s/%d", c.owner, m.Peer, m.ID, m.Timestamp))))
	if prior, ok := saved.Receipts[key]; ok {
		result.Accepted = prior.State == "accepted"
		if !result.Accepted {
			result.Err = errors.New("previous read-state submission did not have a confirmed outcome; not resubmitted")
		}
		return result
	}
	session, err := native.Connect(ctx, state.Account.Host)
	if err != nil {
		result.Err = err
		return result
	}
	defer session.Close()
	saved.Receipts[key] = chatReadReceipt{MessageID: m.ID, Timestamp: m.Timestamp, State: "submitting"}
	if err = c.store.save("reads.json", saved); err != nil {
		result.Err = err
		return result
	}
	err = session.MarkRead(ctx, state.Account, m, "wechat-cli-read-"+key)
	result.Accepted = err == nil
	receipt := saved.Receipts[key]
	receipt.State = "uncertain"
	if result.Accepted {
		receipt.State = "accepted"
	}
	saved.Receipts[key] = receipt
	if saveErr := c.store.save("reads.json", saved); saveErr != nil {
		result.Err = saveErr
		return result
	}
	if saveErr := c.store.save("state.json", state); saveErr != nil {
		result.Err = saveErr
		return result
	}
	result.Err = err
	return result
}

type chatReadDue struct{ Peer, MessageID string }

func (m chatModel) readBoundary() (native.Message, bool) {
	if m.offline || !m.chatVisible() || !m.viewport.AtBottom() {
		return native.Message{}, false
	}
	for i := len(m.messages) - 1; i >= 0; i-- {
		v := m.messages[i]
		_, recalled := native.RecallFor(v)
		if v.Peer == m.selected && (visibleChatMessage(v) || recalled) {
			return v, true
		}
	}
	return native.Message{}, false
}

type readStateBackend interface {
	markRead(context.Context, native.Message) chatReadResult
}

func markReadCommand(ctx context.Context, store Store, args []string) error {
	f := flags("mark-read")
	peer := f.String("to", "", "conversation ID to mark read through its latest archived message")
	if err := parse(f, args); err != nil {
		return err
	}
	if *peer == "" {
		return errors.New("--to is required")
	}
	s, err := openNativeStore(store)
	if err != nil {
		return err
	}
	state, err := s.state()
	if err != nil {
		return err
	}
	if state.Account == nil {
		return errors.New("not logged in")
	}
	history, err := s.history(state.Account.Username)
	if err != nil {
		return err
	}
	var boundary *native.Message
	for i := range history {
		m := &history[i]
		if m.Peer == *peer && visibleChatMessage(*m) && (boundary == nil || m.Timestamp >= boundary.Timestamp) {
			boundary = m
		}
	}
	if boundary == nil {
		return errors.New("conversation has no archived read boundary")
	}
	result := (&chatService{store: s, owner: state.Account.Username}).markRead(ctx, *boundary)
	if result.Err != nil {
		return result.Err
	}
	return json.NewEncoder(os.Stdout).Encode(map[string]any{"accepted": result.Accepted, "peer": *peer, "through_message_id": boundary.ID})
}
