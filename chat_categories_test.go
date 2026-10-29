package main

import (
	"context"
	"encoding/binary"
	"reflect"
	"sort"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/immortal-autumn/WxChatAssistant/internal/native"
)

func categoryTestModel() chatModel {
	zero, one, verified := uint32(0), uint32(1), uint32(24)
	contacts := map[string]native.Contact{
		"alice":            {Nickname: "Alice"},
		"active@chatroom":  {Nickname: "Active group", ChatRoomNotify: &one},
		"muted@chatroom":   {Nickname: "Muted group", ChatRoomNotify: &zero, Flags: 0x800, FlagMask: 0x800},
		"unknown@chatroom": {Nickname: "Unknown notification state"},
		"official":         {Nickname: "Verified publisher", VerifyFlag: &verified},
		"gh_example":       {Nickname: "Publisher"},
	}
	var messages []native.Message
	for peer, c := range contacts {
		c.Username = peer
		contacts[peer] = c
		messages = append(messages, native.Message{ID: peer, Peer: peer, From: peer, Type: 1, Text: "Archived content", Timestamp: 1})
	}
	return newChatModel(context.Background(), &fakeChatBackend{}, "me", native.DesktopProfile, chatSnapshot{Contacts: contacts, Messages: messages}, false)
}

func assertCategoryRows(t *testing.T, m chatModel, expected ...string) {
	t.Helper()
	var actual []string
	for _, row := range m.rows {
		actual = append(actual, row.Peer)
	}
	sort.Strings(actual)
	sort.Strings(expected)
	if !reflect.DeepEqual(actual, expected) {
		t.Fatalf("%s rows: got %v, want %v", m.chatFilter.label(), actual, expected)
	}
}

func TestChatCategoriesDefaultAndKeyboardSwitching(t *testing.T) {
	m := categoryTestModel()
	assertCategoryRows(t, m, "alice", "active@chatroom", "unknown@chatroom")
	if m.foldedConversations() != 3 {
		t.Fatal("folded conversation count is incorrect")
	}
	m.selectPeer("alice")
	m.rebuildRows()
	m.composer.SetValue("Alice draft")
	for _, expected := range [][]string{
		{"alice"},
		{"active@chatroom", "muted@chatroom", "unknown@chatroom"},
		{"gh_example", "official"},
		{"muted@chatroom"},
		{"alice", "active@chatroom", "muted@chatroom", "unknown@chatroom", "gh_example", "official"},
		{"alice", "active@chatroom", "unknown@chatroom"},
	} {
		next, _ := m.update(tea.KeyMsg{Type: tea.KeyTab})
		m = next.(chatModel)
		assertCategoryRows(t, m, expected...)
		if m.selectedIndex < 0 || m.rows[m.selectedIndex].Peer != m.selected || !m.composer.Focused() {
			t.Fatal("manual category switch did not select a visible chat with composer focus")
		}
	}
	m.selectPeer("alice")
	if m.composer.Value() != "Alice draft" {
		t.Fatal("category switches lost the draft")
	}
	next, _ := m.update(tea.KeyMsg{Type: tea.KeyShiftTab})
	if next.(chatModel).chatFilter != chatFilterAll {
		t.Fatal("reverse category switch did not wrap to All")
	}
}

func TestQuietIncomingMessagesKeepBadgesWithoutAttention(t *testing.T) {
	m := categoryTestModel()
	m.selectPeer("alice")
	m.rebuildRows()
	m.composer.SetValue("Keep recipient and draft")
	var incoming []native.Message
	for _, peer := range []string{"muted@chatroom", "official", "gh_example"} {
		incoming = append(incoming, native.Message{ID: "new-" + peer, Peer: peer, From: peer, Type: 1, Text: "New", Timestamp: 99})
	}
	next, _ := m.update(chatSyncResult{Messages: incoming})
	m = next.(chatModel)
	assertCategoryRows(t, m, "alice", "active@chatroom", "unknown@chatroom")
	if m.totalUnread() != 3 || m.attentionUnread() != 0 || m.selected != "alice" || m.composer.Value() != "Keep recipient and draft" {
		t.Fatal("quiet activity changed the recipient, draft, or attention count")
	}
	incoming = append(incoming, native.Message{ID: "new-active", Peer: "active@chatroom", From: "active@chatroom", Type: 1, Text: "New", Timestamp: 100})
	next, _ = m.update(chatJournalResult{Messages: incoming})
	m = next.(chatModel)
	if m.totalUnread() != 4 || m.attentionUnread() != 1 {
		t.Fatal("ordinary activity was not counted or replay double-counted quiet badges")
	}
	m.chatFilter = chatFilterMuted
	m.rebuildRows()
	if !strings.Contains(ansi.Strip(m.View()), "MUTE · Muted group · 1") {
		t.Fatal("muted category lost its unread badge")
	}
}

func TestMetadataFoldingPreservesActiveRecipient(t *testing.T) {
	m := categoryTestModel()
	m.selectPeer("active@chatroom")
	m.rebuildRows()
	m.composer.SetValue("Group draft")
	zero := uint32(0)
	next, _ := m.update(chatSyncResult{Contacts: map[string]native.Contact{"active@chatroom": {Username: "active@chatroom", Nickname: "Renamed group", ChatRoomNotify: &zero}}})
	m = next.(chatModel)
	assertCategoryRows(t, m, "alice", "unknown@chatroom")
	if m.selected != "active@chatroom" || m.composer.Value() != "Group draft" || m.selectedIndex != -1 {
		t.Fatal("metadata refresh changed the active draft recipient")
	}
	// Only an explicit navigation action may change the recipient.
	next, _ = m.update(tea.KeyMsg{Type: tea.KeyDown})
	m = next.(chatModel)
	if m.selected == "active@chatroom" || m.drafts["active@chatroom"] != "Group draft" {
		t.Fatal("navigation did not preserve the folded chat's draft")
	}
}

func TestEmptyCategoryAndContactLookup(t *testing.T) {
	m := testChatModel()
	m.composer.SetValue("Alice draft")
	m.chatFilter = chatFilterGroups
	m.cycleChatFilter(1) // Official, with no official conversations.
	if len(m.rows) != 0 || m.selected != "" || m.composer.Value() != "" || m.drafts["alice"] != "Alice draft" {
		t.Fatal("empty category retained a hidden recipient or lost the draft")
	}
	m = categoryTestModel()
	m.openContacts()
	m.contactUI.Filter = 4
	if len(m.contactPeers()) != 2 {
		t.Fatal("contact page official filter failed")
	}
	m.contactUI.Filter = 5
	if peer := m.contactPeer(); peer != "muted@chatroom" {
		t.Fatal("contact page muted filter failed")
	}
	next, _ := m.update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(chatModel)
	if m.selected != "muted@chatroom" || m.chatFilter != chatFilterAll || m.contactUI.Open || m.selectedIndex < 0 {
		t.Fatal("opening a folded contact did not reveal the selected conversation")
	}
	m.chatFilter = chatFilterMain
	next, _ = m.update(tea.KeyMsg{Type: tea.KeyCtrlN})
	m = next.(chatModel)
	if !m.newChat || m.chatFilter != chatFilterAll || m.focus != focusSearch {
		t.Fatal("new chat lookup excluded folded contacts")
	}
}

func TestNotificationSyncAndArchiveReplay(t *testing.T) {
	s := journalTestStore(t)
	peer := "active@chatroom"
	full := groupContactRecord(peer, "Original name")
	full = binary.AppendUvarint(binary.AppendUvarint(full, 17<<3), 1)
	if err := s.journal(nativeJournal{Owner: "me", Batch: native.SyncBatch{Commands: []native.SyncCommand{{ID: 2, Payload: full}}}}); err != nil {
		t.Fatal(err)
	}
	w, err := newChatJournalWatcher(s, "me")
	if err != nil {
		t.Fatal(err)
	}
	// Keep only the field-1 room ID, then append command-20 status=0.
	delta := groupContactRecord(peer, "")
	delta = append(delta[:len(delta)-4:len(delta)-4], 0x10, 0x00)
	if err := s.journal(nativeJournal{Owner: "me", Batch: native.SyncBatch{Commands: []native.SyncCommand{{ID: 20, Payload: delta}}}}); err != nil {
		t.Fatal(err)
	}
	m := categoryTestModel()
	result := w.read(m.ctx)
	if result.Err != nil || !result.Contacts[peer].Muted() {
		t.Fatal("journal watcher did not decode notification update", result.Err)
	}
	next, _ := m.update(result)
	m = next.(chatModel)
	if !m.contact(peer).Muted() || m.name(peer) != "Active group" {
		t.Fatal("notification delta lost existing group metadata")
	}
	data, err := s.chatSnapshot("me")
	if err != nil || !data.Contacts[peer].Muted() || data.Contacts[peer].Nickname != "Original name" {
		t.Fatal("notification state did not survive archive replay", err)
	}
	other, err := s.chatSnapshot("another-owner")
	if err != nil || len(other.Contacts) != 0 {
		t.Fatal("classification metadata crossed account boundaries", err)
	}
	// The phone can re-enable notifications while the TUI remains open.
	delta[len(delta)-1] = 1
	if err := s.journal(nativeJournal{Owner: "me", Batch: native.SyncBatch{Commands: []native.SyncCommand{{ID: 20, Payload: delta}}}}); err != nil {
		t.Fatal(err)
	}
	next, _ = m.update(w.read(m.ctx))
	m = next.(chatModel)
	assertCategoryRows(t, m, "alice", "active@chatroom", "unknown@chatroom")
	if m.contact(peer).Muted() || m.name(peer) != "Active group" {
		t.Fatal("unmuting did not restore the chat to Main with its name intact")
	}
}

func TestCategoryLayoutsFitTerminal(t *testing.T) {
	for _, size := range [][2]int{{64, 18}, {80, 24}, {120, 40}} {
		m := categoryTestModel()
		m.resize(size[0], size[1])
		for f := chatFilterMain; f < chatFilterCount; f++ {
			m.chatFilter = f
			m.rebuildRows()
			view := m.View()
			if lipgloss.Height(view) != size[1] {
				t.Fatalf("%s: category frame height changed", f.label())
			}
			for _, line := range strings.Split(view, "\n") {
				if lipgloss.Width(line) > size[0] {
					t.Fatalf("%s: category frame overflowed width", f.label())
				}
			}
		}
	}
}
