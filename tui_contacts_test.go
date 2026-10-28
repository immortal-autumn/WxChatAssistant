package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/immortal-autumn/WxChatAssistant/internal/native"
)

type fakeContactBackend struct {
	fakeChatBackend
	calls  int
	peer   string
	change native.ContactChange
}

func (f *fakeContactBackend) updateContact(_ context.Context, peer string, change native.ContactChange) chatContactResult {
	f.calls++
	f.peer, f.change = peer, change
	return chatContactResult{Peer: peer, Accepted: true, Contact: native.Contact{Username: peer, Remark: *change.Remark}}
}

func contactKey(t *testing.T, m chatModel, key tea.KeyMsg) (chatModel, tea.Cmd) {
	t.Helper()
	next, cmd := m.update(key)
	return next.(chatModel), cmd
}

func TestContactsEditCapturesTargetAndWaitsForAcceptance(t *testing.T) {
	m := testChatModel()
	backend := &fakeContactBackend{}
	m.backend = backend
	m.composer.SetValue("Unsent chat draft")
	m.openContacts()
	if m.chatVisible() {
		t.Fatal("contacts page marked a hidden chat read")
	}
	m, _ = contactKey(t, m, tea.KeyMsg{Type: tea.KeyCtrlR})
	if !m.contactUI.Editing || m.contactUI.EditPeer != "alice" {
		t.Fatal("remark editor did not capture the contact")
	}
	m.contactUI.Editor.SetValue("New name")
	m, cmd := contactKey(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil || !m.contactUI.Busy || m.contacts["alice"].Remark != "" {
		t.Fatal("contact changed before server acceptance")
	}
	_, duplicate := contactKey(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if duplicate != nil {
		t.Fatal("duplicate contact update submitted")
	}
	m, _ = contactKey(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	m, _ = contactKey(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	m.selectPeer("bob")
	next, _ := m.update(cmd())
	m = next.(chatModel)
	if backend.calls != 1 || backend.peer != "alice" || m.contacts["alice"].Remark != "New name" || m.contacts["bob"].Remark != "" {
		t.Fatal("contact edit applied to the wrong person")
	}
	if m.drafts["alice"] != "Unsent chat draft" {
		t.Fatal("contact page lost the chat draft")
	}
}

func TestContactsFailureOfflineSearchAndLayout(t *testing.T) {
	m := testChatModel()
	m.openContacts()
	m.contactUI.Busy = true
	next, _ := m.update(chatContactResult{Peer: "alice", Err: errors.New("rejected")})
	m = next.(chatModel)
	if m.contactUI.Busy || m.contacts["alice"].Remark != "" || !strings.Contains(m.contactUI.Notice, "rejected") {
		t.Fatal("failed contact edit changed local data")
	}
	m.offline = true
	pin := true
	if cmd := m.changeContact("alice", native.ContactChange{Pinned: &pin}); cmd != nil || m.contacts["alice"].Pinned() {
		t.Fatal("offline contact edit was submitted")
	}
	m.contactUI.Search.SetValue("Alice 王")
	if peers := m.contactPeers(); len(peers) != 1 || peers[0] != "alice" {
		t.Fatal("contact search did not match Unicode names")
	}
	for _, size := range [][2]int{{64, 18}, {80, 24}, {120, 40}} {
		m.resize(size[0], size[1])
		for _, editing := range []bool{false, true} {
			m.contactUI.Editing = editing
			view := m.View()
			if lipgloss.Height(view) > size[1] {
				t.Fatal("contacts page too tall")
			}
			for _, line := range strings.Split(view, "\n") {
				if lipgloss.Width(line) > size[0] {
					t.Fatal("contacts page too wide")
				}
			}
		}
	}
	m.contactUI.Search.SetValue("not a contact")
	m, cmd := contactKey(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil || !m.contactUI.Open {
		t.Fatal("empty search opened a chat")
	}
}

func TestPinnedContactAppearsWithoutArchivedMessages(t *testing.T) {
	m := testChatModel()
	for _, pinned := range []bool{true, false} {
		flags := uint32(0)
		if pinned {
			flags = 0x800
		}
		next, _ := m.update(chatContactResult{Peer: "carol", Accepted: true, Contact: native.Contact{Username: "carol", Nickname: "Carol", Flags: flags, FlagMask: 0x800}})
		m = next.(chatModel)
		found := false
		for _, row := range m.rows {
			found = found || row.Peer == "carol"
		}
		if found != pinned {
			t.Fatal("pin state did not update the conversation list")
		}
	}
}
