package main

import (
	"context"
	"encoding/binary"
	"errors"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/immortal-autumn/WxChatAssistant/internal/native"
)

type fakeGroupContactBackend struct {
	fakeChatBackend
	peers []string
	err   error
}

func (b *fakeGroupContactBackend) groupContact(_ context.Context, peer string) chatGroupContactResult {
	b.peers = append(b.peers, peer)
	return chatGroupContactResult{Peer: peer, Contact: native.Contact{Username: peer, Nickname: "学习交流群"}, Err: b.err}
}

func groupNameModel() chatModel {
	m := testChatModel()
	m.backend = &fakeGroupContactBackend{}
	m.addMessages([]native.Message{
		{ID: "group-a", From: "a@chatroom", Peer: "a@chatroom", Type: 1, Text: "A", Timestamp: 10},
		{ID: "group-b", From: "b@chatroom", Peer: "b@chatroom", Type: 1, Text: "B", Timestamp: 20},
	}, false)
	m.rebuildRows()
	return m
}

func TestGroupNamesPrioritizeSelectionAndUpdateWithoutChangingDraft(t *testing.T) {
	m := groupNameModel()
	m.selectPeer("a@chatroom")
	m.rebuildRows()
	m.composer.SetValue("Keep this draft")
	m.contacts[m.selected] = native.Contact{Username: m.selected, Members: map[string]string{"alice": "Group Alice"}, Flags: 0x800}
	cmd := m.scheduleGroupNames()
	if cmd == nil || m.scheduleGroupNames() != nil {
		t.Fatal("missing group name was not scheduled exactly once")
	}
	next, _ := m.update(cmd())
	m = next.(chatModel)
	backend := m.backend.(*fakeGroupContactBackend)
	if len(backend.peers) != 1 || backend.peers[0] != "a@chatroom" || m.name("a@chatroom") != "学习交流群" {
		t.Fatal("selected group name was not fetched and displayed")
	}
	if m.selected != "a@chatroom" || m.composer.Value() != "Keep this draft" || !m.contacts[m.selected].Pinned() || m.contacts[m.selected].Members["alice"] != "Group Alice" {
		t.Fatal("name lookup lost the recipient, draft, pin, or roster")
	}
	if !strings.Contains(ansi.Strip(m.View()), "学习交流群") {
		t.Fatal("resolved group name is absent from the rendered view")
	}
	if m.scheduleGroupNames() != nil {
		t.Fatal("group lookups were not rate limited")
	}
	m.nextGroupNameLookup = time.Time{}
	cmd = m.scheduleGroupNames()
	if cmd == nil {
		t.Fatal("unselected unnamed group was not scheduled in the background")
	}
	next, _ = m.update(cmd())
	m = next.(chatModel)
	if m.name("b@chatroom") != "学习交流群" || len(backend.peers) != 2 {
		t.Fatal("background lookup failed or repeated the resolved group")
	}
}

func TestGroupNameLookupSkipsKnownNamesAndOfflineSessions(t *testing.T) {
	for _, mode := range []string{"offline", "paused", "already-named"} {
		t.Run(mode, func(t *testing.T) {
			m := groupNameModel()
			m.offline = mode == "offline"
			m.paused = mode == "paused"
			if mode == "already-named" {
				m.contacts["a@chatroom"] = native.Contact{Username: "a@chatroom", Remark: "My group"}
				m.contacts["b@chatroom"] = native.Contact{Username: "b@chatroom", Nickname: "Another group"}
			}
			if m.scheduleGroupNames() != nil {
				t.Fatal("unnecessary group metadata request")
			}
		})
	}
}

func TestGroupNameLookupContinuesWhileSearching(t *testing.T) {
	m := groupNameModel()
	m.query = "A name that has not been fetched"
	m.rebuildRows()
	if len(m.rows) != 0 {
		t.Fatal("fixture search should hide unnamed groups")
	}
	if m.scheduleGroupNames() == nil {
		t.Fatal("filtering the sidebar prevented fetching group names")
	}
}

func TestGroupNameLookupFailureRetriesOnlyAfterRefresh(t *testing.T) {
	m := groupNameModel()
	backend := m.backend.(*fakeGroupContactBackend)
	backend.err = errors.New("lookup unavailable")
	for i := 0; i < 2; i++ {
		m.nextGroupNameLookup = time.Time{}
		cmd := m.scheduleGroupNames()
		if cmd == nil {
			t.Fatal("group was not attempted")
		}
		next, _ := m.update(cmd())
		m = next.(chatModel)
	}
	m.nextGroupNameLookup = time.Time{}
	if m.scheduleGroupNames() != nil || m.groupNameErr == "" {
		t.Fatal("failed lookups looped or did not report their failure")
	}
	next, _ := m.update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}, Alt: true})
	m = next.(chatModel)
	if m.scheduleGroupNames() == nil {
		t.Fatal("manual refresh did not permit a failed lookup to retry")
	}
}

func TestGroupNameResultRejectsWrongIdentity(t *testing.T) {
	m := groupNameModel()
	next, _ := m.update(chatGroupContactResult{Peer: "a@chatroom", Contact: native.Contact{Username: "b@chatroom", Nickname: "Wrong name"}})
	m = next.(chatModel)
	if groupHasName(m.contacts["a@chatroom"]) || groupHasName(m.contacts["b@chatroom"]) || m.groupNameErr == "" {
		t.Fatal("a mismatched lookup named the wrong group")
	}
}

func groupContactRecord(peer, name string) []byte {
	field := func(number uint64, value []byte) []byte {
		b := binary.AppendUvarint(nil, number<<3|2)
		b = binary.AppendUvarint(b, uint64(len(value)))
		return append(b, value...)
	}
	return append(field(1, field(1, []byte(peer))), field(2, field(1, []byte(name)))...)
}

func TestGroupNamesPersistAndFlowThroughLocalJournal(t *testing.T) {
	s := journalTestStore(t)
	w, err := newChatJournalWatcher(s, "me")
	if err != nil {
		t.Fatal(err)
	}
	peer, name := "a@chatroom", "学习交流群"
	raw := groupContactRecord(peer, name)
	if err := s.journal(nativeJournal{Owner: "me", Batch: native.SyncBatch{Commands: []native.SyncCommand{{ID: 2, Payload: raw}}}}); err != nil {
		t.Fatal(err)
	}
	m := groupNameModel()
	next, _ := m.update(w.read(m.ctx))
	m = next.(chatModel)
	if m.name(peer) != name {
		t.Fatal("an already open TUI did not receive the cached group name")
	}
	data, err := s.chatSnapshot("me")
	if err != nil {
		t.Fatal(err)
	}
	reopened := newChatModel(context.Background(), &fakeChatBackend{}, "me", native.DesktopProfile, data, true)
	if reopened.name(peer) != name {
		t.Fatal("group name was lost on an offline restart")
	}
	other, err := s.chatSnapshot("other-owner")
	if err != nil || len(other.Contacts) != 0 {
		t.Fatal("group metadata leaked into another account", err)
	}
}

func TestGroupNameServiceRejectsInvalidTargetAndChangedAccount(t *testing.T) {
	c := &chatService{store: journalTestStore(t), owner: "me"}
	for _, peer := range []string{"alice", "bad\n@chatroom", "\xff@chatroom", strings.Repeat("x", 257) + "@chatroom"} {
		if result := c.groupContact(context.Background(), peer); result.Err == nil || !strings.Contains(result.Err.Error(), "invalid group ID") {
			t.Fatal("invalid group ID was not rejected before authentication")
		}
	}
	if result := c.groupContact(context.Background(), "a@chatroom"); result.Err == nil || !strings.Contains(result.Err.Error(), "account changed") {
		t.Fatal("group lookup continued without its original account")
	}
}

func TestGroupMemberLabelUsesOwnerScopedRosterCache(t *testing.T) {
	s := journalTestStore(t)
	peer := "legacy@chatroom"
	if err := s.save("rooms.json", chatRoomCache{Owner: "me", Rooms: map[string]chatRoomEntry{
		peer: {Members: map[string]string{"me": "My own name", "alice": "Alice", "bob": "Bob"}},
	}}); err != nil {
		t.Fatal(err)
	}
	data, err := s.chatSnapshot("me")
	if err != nil {
		t.Fatal(err)
	}
	m := newChatModel(context.Background(), &fakeChatBackend{}, "me", native.DesktopProfile, data, true)
	label := m.name(peer)
	if !strings.HasPrefix(label, "Group · ") || !strings.Contains(label, "Alice") || !strings.Contains(label, "Bob") || strings.Contains(label, "My own name") {
		t.Fatal("unnamed group lacks a clear member-based label")
	}
	for i := 0; i < 10; i++ {
		if m.name(peer) != label {
			t.Fatal("member-based group label changed between renders")
		}
	}
	if groupHasName(m.contacts[peer]) {
		t.Fatal("generated label must not prevent fetching the real group name")
	}
	if err := s.journal(nativeJournal{Owner: "me", Batch: native.SyncBatch{Commands: []native.SyncCommand{{ID: 2, Payload: groupContactRecord(peer, "Real group name")}}}}); err != nil {
		t.Fatal(err)
	}
	data, err = s.chatSnapshot("me")
	if err != nil {
		t.Fatal(err)
	}
	m = newChatModel(context.Background(), &fakeChatBackend{}, "me", native.DesktopProfile, data, true)
	if m.name(peer) != "Real group name" {
		t.Fatal("member-based label overrode the real group name")
	}
	other, err := s.chatSnapshot("other-owner")
	if err != nil || len(other.Contacts) != 0 {
		t.Fatal("cached group members leaked into another account", err)
	}
}
