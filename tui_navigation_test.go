package main

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/immortal-autumn/WxChatAssistant/internal/native"
	"github.com/muesli/termenv"
)

func TestNavigationAtListBoundariesDoesNotScheduleWork(t *testing.T) {
	m := longRoomNavigationModel()
	m.chatFilter = chatFilterAll
	m.rebuildRows()
	for _, edge := range []struct {
		index int
		key   tea.KeyType
	}{{0, tea.KeyUp}, {len(m.rows) - 1, tea.KeyDown}} {
		m.selectPeer(m.rows[edge.index].Peer)
		m.rebuildRows()
		m.composer.SetValue("Keep the boundary draft")
		peer, offset, view := m.selected, m.listOffset, m.View()
		for i := 0; i < 100; i++ {
			next, cmd := m.Update(tea.KeyMsg{Type: edge.key})
			m = next.(chatModel)
			if cmd != nil {
				t.Fatal("a repeated key beyond the list boundary scheduled work")
			}
			if m.selected != peer || m.listOffset != offset || m.View() != view || m.composer.Value() != "Keep the boundary draft" {
				t.Fatal("a repeated key beyond the list boundary changed the view or draft")
			}
		}
	}
}

func TestUnicodeLineSeparatorsAreLaidOutBeforeChatRendering(t *testing.T) {
	profile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(profile) })
	m := longRoomNavigationModel()
	raw := "First line\u2028Second line\u2029Third line"
	message := native.Message{ID: "unicode-separators", Peer: "long-00@chatroom", From: "long-00@chatroom", Type: 1, Text: raw, Timestamp: 100}
	m.addMessages([]native.Message{message}, false)
	m.chatFilter = chatFilterAll
	m.rebuildRows()
	for _, size := range [][2]int{{64, 18}, {80, 24}, {190, 50}} {
		m.resize(size[0], size[1])
		for _, key := range []tea.KeyType{tea.KeyDown, tea.KeyUp} {
			for i := 0; i < len(m.rows)+20; i++ {
				next, _ := m.Update(tea.KeyMsg{Type: key})
				m = next.(chatModel)
				view := m.View()
				if lipgloss.Height(view) != size[1] || strings.ContainsAny(view, "\u2028\u2029") {
					t.Fatal("navigation emitted an unhandled separator or changed the frame height")
				}
			}
		}
		m.selectPeer(message.Peer)
		if view := m.viewport.View(); strings.ContainsAny(view, "\u2028\u2029") || !strings.Contains(view, "Third line") {
			t.Fatal("Unicode separators were not converted into physical message lines")
		}
	}
	for _, saved := range m.messages {
		if saved.ID == message.ID && saved.Text != raw {
			t.Fatal("rendering modified the archived message")
		}
	}
}
