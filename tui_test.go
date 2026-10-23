package main

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/kevmo314/wechat-cli/internal/native"
)

type fakeChatBackend struct {
	calls      int
	peer, text string
}

func (f *fakeChatBackend) sync(context.Context) chatSyncResult { return chatSyncResult{} }
func (f *fakeChatBackend) send(_ context.Context, peer, text string) chatSendResult {
	f.calls++
	f.peer = peer
	f.text = text
	return chatSendResult{Peer: peer, Text: text, Accepted: true, Message: native.Message{ID: "sent", Peer: peer, Text: text, Outgoing: true, Type: 1, Timestamp: 4}}
}
func testChatModel() chatModel {
	data := chatSnapshot{Contacts: map[string]native.Contact{"alice": {Username: "alice", Nickname: "Alice 王"}, "bob": {Username: "bob", Nickname: "Bob"}}, Messages: []native.Message{
		{ID: "1", From: "alice", To: "me", Peer: "alice", Type: 1, Text: "你好 👋 this is a message", Timestamp: 2},
		{ID: "2", From: "bob", To: "me", Peer: "bob", Type: 1, Text: "Hello", Timestamp: 1},
		{ID: "3", From: "me", To: "me", Peer: "me", Type: 51, Text: "INTERNAL_CONTROL", Timestamp: 3},
	}}
	return newChatModel(context.Background(), &fakeChatBackend{}, "me", native.AndroidTabletProfile, data, false)
}
func TestTUILayoutAndControlFiltering(t *testing.T) {
	for _, size := range [][2]int{{64, 18}, {80, 24}, {120, 40}} {
		m := testChatModel()
		m.resize(size[0], size[1])
		view := m.View()
		if strings.Contains(view, "INTERNAL_CONTROL") {
			t.Fatal("system control became a chat")
		}
		if !strings.Contains(view, "Alice") {
			t.Fatal("contact name not shown")
		}
		if h := lipgloss.Height(view); h > size[1] {
			t.Errorf("%v: view height %d", size, h)
		}
		for _, line := range strings.Split(view, "\n") {
			if w := lipgloss.Width(line); w > size[0] {
				t.Errorf("%v: line width %d: %q", size, w, ansi.Strip(line))
			}
		}
	}
}
func TestTUISendCapturesRecipientAndBlocksDoubleSubmit(t *testing.T) {
	m := testChatModel()
	backend := m.backend.(*fakeChatBackend)
	m.setFocus(focusCompose)
	m.composer.SetValue("Hello Alice")
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(chatModel)
	if m.composer.Value() != "" {
		t.Fatal("composer did not clear immediately after submit")
	}
	if cmd == nil || !m.sending {
		t.Fatal("explicit submit did not start")
	}
	next, duplicate := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(chatModel)
	if duplicate != nil {
		t.Fatal("double submit scheduled")
	}
	m.selectPeer("bob")
	m.composer.SetValue("Draft for Bob")
	result := cmd()
	if backend.calls != 1 || backend.peer != "alice" || backend.text != "Hello Alice" {
		t.Fatalf("recipient changed during send: %#v", backend)
	}
	next, _ = m.Update(result)
	m = next.(chatModel)
	if m.composer.Value() != "Draft for Bob" || m.selected != "bob" {
		t.Fatal("send completion destroyed another conversation's draft")
	}
	m.selectPeer("alice")
	if m.composer.Value() != "" {
		t.Fatal("accepted draft retained for accidental resend")
	}
}
func TestTUIDraftsSurviveIncomingReorderAndPasteDoesNotSend(t *testing.T) {
	m := testChatModel()
	m.setFocus(focusCompose)
	m.composer.SetValue("draft alice")
	next, _ := m.Update(chatSyncResult{Messages: []native.Message{{ID: "4", From: "bob", To: "me", Peer: "bob", Type: 1, Text: "new", Timestamp: 10}}})
	m = next.(chatModel)
	if m.selected != "alice" || m.composer.Value() != "draft alice" {
		t.Fatal("sync switched compose recipient")
	}
	m.selectPeer("bob")
	m.composer.SetValue("draft bob")
	m.selectPeer("alice")
	if m.composer.Value() != "draft alice" {
		t.Fatal("draft lost when switching")
	}
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("pasted\ntext"), Paste: true})
	m = next.(chatModel)
	if m.sending || m.backend.(*fakeChatBackend).calls != 0 {
		t.Fatal("paste sent a message")
	}
}
func TestTUIEscapesRemoteTerminalControl(t *testing.T) {
	m := testChatModel()
	m.contacts["alice"] = native.Contact{Username: "alice", Nickname: "Alice\x1b]52;c;dGVzdA==\a"}
	m.messages[0].Text = "hello\x1b[2J\x1b]0;malicious-title\a"
	m.refreshHistory(true)
	view := m.View()
	if strings.Contains(view, "malicious-title") || strings.Contains(view, "dGVzdA==") || strings.Contains(view, "\x1b[2J") {
		t.Fatal("remote terminal escape rendered")
	}
}
func TestTUIMediaPreviews(t *testing.T) {
	if got := messagePreview(native.Message{Type: 49, Text: "<msg><appmsg><title>A shared link</title></appmsg></msg>"}); got != "↗ A shared link" {
		t.Fatal(got)
	}
	if got := messagePreview(native.Message{Type: 3, Text: "raw image metadata"}); strings.Contains(got, "metadata") {
		t.Fatal(got)
	}
}

func TestCompactMessageLayout(t *testing.T) {
	m := testChatModel()
	m.resize(120, 30)
	if n := m.viewport.TotalLineCount(); n > 3 {
		t.Fatalf("two short messages should not consume three lines each: %d", n)
	}
}
