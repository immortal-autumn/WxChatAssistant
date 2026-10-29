package main

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/immortal-autumn/WxChatAssistant/internal/native"
)

type chatGroupContactResult struct {
	Peer    string
	Contact native.Contact
	Err     error
}

type groupContactBackend interface {
	groupContact(context.Context, string) chatGroupContactResult
}

func (c *chatService) groupContact(ctx context.Context, peer string) chatGroupContactResult {
	result := chatGroupContactResult{Peer: peer}
	if !strings.HasSuffix(peer, "@chatroom") || len(peer) > 256 || oneLine(peer) != peer {
		result.Err = errors.New("invalid group ID")
		return result
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
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
	raw, err := session.FetchContact(ctx, state.Account, peer)
	if err != nil {
		result.Err = err
		return result
	}
	result.Contact, result.Err = native.DecodeContact(raw)
	if result.Err != nil {
		return result
	}
	// Persist the server's contact, including its real group name. Other open
	// clients and the next startup consume the same ordinary contact journal.
	if err = c.store.journal(nativeJournal{Owner: c.owner, Batch: native.SyncBatch{Commands: []native.SyncCommand{{ID: 2, Payload: raw}}}}); err != nil {
		result.Err = err
		return result
	}
	result.Err = c.store.save("state.json", state)
	return result
}

func groupHasName(c native.Contact) bool {
	return oneLine(c.Remark) != "" || oneLine(c.Nickname) != "" || oneLine(c.Alias) != ""
}

func groupMemberSummary(members map[string]string, owner string) string {
	seen := map[string]bool{}
	var names []string
	for peer, name := range members {
		name = oneLine(name)
		if peer != owner && name != "" && name != peer && !seen[name] {
			seen[name] = true
			names = append(names, name)
		}
	}
	sort.Strings(names)
	if len(names) == 0 {
		return ""
	}
	label := strings.Join(names[:min(3, len(names))], ", ")
	if len(names) > 3 {
		label += ", …"
	}
	return "Group · " + label
}

func (m *chatModel) scheduleGroupNames() tea.Cmd {
	if m.offline || m.paused || m.groupNameBusy || time.Now().Before(m.nextGroupNameLookup) {
		return nil
	}
	backend, ok := m.backend.(groupContactBackend)
	if !ok {
		return nil
	}
	needsLookup := func(peer string) bool {
		return strings.HasSuffix(peer, "@chatroom") && !groupHasName(m.contacts[peer]) && !m.groupNameAttempted[peer]
	}
	peer := m.selected
	if !needsLookup(peer) {
		peer = ""
		for _, row := range m.rows {
			if needsLookup(row.Peer) {
				peer = row.Peer
				break
			}
		}
	}
	// Search can hide every unnamed group before its real name is known.
	// Continue filling metadata from the archive even with an empty result list.
	if peer == "" {
		for i := len(m.messages) - 1; i >= 0; i-- {
			if needsLookup(m.messages[i].Peer) {
				peer = m.messages[i].Peer
				break
			}
		}
	}
	if peer == "" {
		return nil
	}
	m.groupNameBusy = true
	m.groupNameAttempted[peer] = true
	m.nextGroupNameLookup = time.Now().Add(time.Second)
	return func() tea.Msg { return backend.groupContact(m.ctx, peer) }
}
