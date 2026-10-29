package main

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/immortal-autumn/WxChatAssistant/internal/native"
)

func longRoomNavigationModel() chatModel {
	m := categoryTestModel()
	names := []string{
		"短群名",
		"👨‍👩‍👧‍👦 家庭讨论群 " + strings.Repeat("中文长群名称 mixed English ", 10),
		"❤️ 技术交流群 " + strings.Repeat("中文长群名称", 20),
		"👋🏽 Mixed group " + strings.Repeat("A long room name ", 20),
	}
	for i := 0; i < 40; i++ {
		peer := fmt.Sprintf("long-%02d@chatroom", i)
		c := native.Contact{Username: peer, Nickname: names[i%len(names)]}
		if i%3 == 0 {
			zero := uint32(0)
			c.ChatRoomNotify = &zero
		}
		m.contacts[peer] = c
		m.addMessages([]native.Message{{ID: peer, Peer: peer, From: peer, Type: 1, Text: "Long room navigation", Timestamp: uint32(i + 10)}}, false)
	}
	m.rebuildRows()
	return m
}

func TestDisplayNamesUseTerminalCompatibleWidths(t *testing.T) {
	// Cursor advances measured in VTE 0.76: emoji selectors do not widen a
	// heart, and joined families / skin tones occupy separate character cells.
	// The display fallback adds a space before the skin-tone component.
	for _, test := range []struct {
		name  string
		cells int
	}{
		{"中文长群名", 10}, {"❤️", 1}, {"👨‍👩‍👧‍👦", 8}, {"👋🏽", 5}, {"🇨🇳", 2}, {"1️⃣", 1}, {"é", 1},
	} {
		m := testChatModel()
		m.contacts["alice"] = native.Contact{Username: "alice", Nickname: test.name}
		if got := ansi.StringWidth(m.name("alice")); got != test.cells {
			t.Errorf("%q: layout uses %d columns; terminal occupies %d", test.name, got, test.cells)
		}
		if m.contacts["alice"].Nickname != test.name {
			t.Fatal("display conversion changed the stored name")
		}
	}
}

func TestDisplayConversionPreservesLinksSearchAndSentText(t *testing.T) {
	raw := "❤️ Family 👨‍👩‍👧‍👦 👋🏽"
	displayed := chatDisplayText(raw)
	if chatDisplayText(displayed) != displayed {
		t.Fatal("repeated display conversion changed cell boundaries")
	}
	uri := "https://example.org/❤️"
	link := terminalLink(uri, raw)
	prefix := link[:strings.Index(link, "\x1b\\")+2]
	if got := chatDisplayText(link); !strings.HasPrefix(got, prefix) || !strings.Contains(got, displayed) {
		t.Fatalf("display conversion changed the hyperlink target or failed to convert its label: %q", got)
	}
	m := testChatModel()
	m.contacts["alice"] = native.Contact{Username: "alice", Nickname: raw}
	m.query = raw
	m.rebuildRows()
	if len(m.rows) != 1 || m.rows[0].Peer != "alice" {
		t.Fatal("search no longer accepts the original contact name")
	}
	m.composer.SetValue(raw)
	_ = m.View()
	cmd := m.submit()
	if cmd == nil {
		t.Fatal("fake send was not scheduled")
	}
	_ = cmd()
	if m.backend.(*fakeChatBackend).text != raw || m.contacts["alice"].Nickname != raw {
		t.Fatal("display conversion modified a message send or stored contact")
	}
}

func TestLongRoomNavigationKeepsFramesAndDraftsStable(t *testing.T) {
	for _, size := range [][2]int{{64, 18}, {80, 24}, {120, 40}, {190, 50}} {
		m := longRoomNavigationModel()
		m.resize(size[0], size[1])
		m.chatFilter = chatFilterGroups
		m.rebuildRows()
		m.selectFirstMatchingChat()
		original := m.selected
		m.composer.SetValue("Original draft ❤️ 👨‍👩‍👧‍👦")
		for _, key := range []tea.KeyType{tea.KeyDown, tea.KeyUp} {
			for i := 0; i < 25; i++ {
				next, _ := m.update(tea.KeyMsg{Type: key})
				m = next.(chatModel)
				view := m.View()
				if lipgloss.Height(view) != size[1] {
					t.Fatalf("%v: frame height changed during navigation", size)
				}
				if lipgloss.Width(strings.Split(view, "\n")[size[1]-1]) >= size[0] {
					t.Fatal("the last row used the terminal's bottom-right cell")
				}
				for _, line := range strings.Split(view, "\n") {
					if lipgloss.Width(line) > size[0] {
						t.Fatalf("%v: frame width overflowed during navigation", size)
					}
				}
				left := min(34, max(24, m.width/3))
				// Every list row stays at its mouse hit position, regardless of
				// whether its title contains wide characters or emoji sequences.
				for y := 2; y < m.height-3; y++ {
					line := strings.Split(view, "\n")[y]
					if ansi.Strip(ansi.Cut(line, left-1, left+1)) != "││" {
						t.Fatalf("%v: sidebar border moved on row %d", size, y)
					}
				}
			}
		}
		m.selectPeer(original)
		if m.composer.Value() != "Original draft ❤️ 👨‍👩‍👧‍👦" {
			t.Fatal("navigation or display conversion changed the draft")
		}
	}
}
