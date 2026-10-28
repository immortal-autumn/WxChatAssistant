package main

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/immortal-autumn/WxChatAssistant/internal/native"
)

func TestTranslationDisplayAndDeletedMessages(t *testing.T) {
	m := testChatModel()
	message, ok := m.selectedMessage()
	if !ok {
		t.Fatal("no selected message")
	}
	key := m.translationKey(message)
	next, _ := m.update(chatTranslationResult{Key: key, Text: "Hello"})
	m = next.(chatModel)
	if !strings.Contains(m.viewport.View(), "Translation: Hello") {
		t.Fatal("translation not shown inline")
	}
	m.translateSelected()
	if strings.Contains(m.viewport.View(), "Translation: Hello") {
		t.Fatal("translation did not toggle off")
	}
	for _, message := range []native.Message{{Type: 1, Deleted: true, Text: "deleted"}, {Type: 3}, {Type: 1, Text: "  "}} {
		if _, err := translationText(message); err == nil {
			t.Fatal("accepted unsupported translation")
		}
	}
}

func TestDirectReplyAndClickableMessage(t *testing.T) {
	m := testChatModel()
	next, _ := m.update(tea.KeyMsg{Type: tea.KeyCtrlR})
	m = next.(chatModel)
	if m.actionsOpen || m.replyTargets["alice"].ID != "1" {
		t.Fatal("reply required an action panel")
	}
	m.addMessages([]native.Message{{ID: "4", Peer: "alice", From: "alice", Type: 1, Text: "https://example.com", Timestamp: 4}}, false)
	m.refreshHistory(false)
	row := m.messageStarts["4"]
	left := min(34, max(24, m.width/3))
	hit := m.historyHits[row]
	if len(hit.Links) != 1 {
		t.Fatal("missing clickable URL")
	}
	_, cmd := m.mouseAction(tea.MouseMsg{X: left + 2 + hit.Links[0].Start, Y: 4 + row - m.viewport.YOffset, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	if cmd == nil {
		t.Fatal("click did not schedule opening the URL")
	}
	// Do not execute the command: tests must not launch a browser.
}
