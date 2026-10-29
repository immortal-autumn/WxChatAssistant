package main

import (
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/immortal-autumn/WxChatAssistant/internal/native"
)

type contactPage struct {
	Open, Editing, Busy bool
	Search, Editor      textinput.Model
	Index, Filter       int
	EditPeer, Notice    string
}

var contactFilters = []string{"All", "People", "Groups", "Pinned", "Official", "Muted"}

func (m *chatModel) openContacts() tea.Cmd {
	search := textinput.New()
	search.Prompt = "/ "
	search.Placeholder = "Search names, remarks, aliases, or IDs"
	search.CharLimit = 256
	editor := textinput.New()
	editor.Prompt = "Remark: "
	editor.Placeholder = "Leave empty to remove the remark"
	editor.CharLimit = 50
	m.contactUI = contactPage{Open: true, Search: search, Editor: editor, Busy: m.contactUI.Busy}
	m.help = false
	m.composer.Blur()
	for i, peer := range m.contactPeers() {
		if peer == m.selected {
			m.contactUI.Index = i
			break
		}
	}
	return m.contactUI.Search.Focus()
}

func (m chatModel) contactPeers() []string {
	known := map[string]bool{}
	for peer := range m.contacts {
		known[peer] = true
	}
	for _, message := range m.messages {
		if visibleChatMessage(message) {
			known[message.Peer] = true
		}
	}
	query := strings.ToLower(strings.TrimSpace(m.contactUI.Search.Value()))
	var peers []string
	for peer := range known {
		if peer == "" {
			continue
		}
		c := m.contact(peer)
		if (m.contactUI.Filter == 1 && (c.Group() || c.Official())) || (m.contactUI.Filter == 2 && !c.Group()) || (m.contactUI.Filter == 3 && !c.Pinned()) || (m.contactUI.Filter == 4 && !c.Official()) || (m.contactUI.Filter == 5 && !c.Muted()) {
			continue
		}
		text := strings.Join([]string{peer, c.Nickname, c.Remark, c.Alias, m.name(peer)}, " ")
		if query != "" && !strings.Contains(strings.ToLower(text), query) {
			continue
		}
		peers = append(peers, peer)
	}
	sort.Slice(peers, func(i, j int) bool {
		a, b := strings.ToLower(m.name(peers[i])), strings.ToLower(m.name(peers[j]))
		if a == b {
			return peers[i] < peers[j]
		}
		return a < b
	})
	return peers
}

func (m chatModel) contactPeer() string {
	peers := m.contactPeers()
	if len(peers) == 0 {
		return ""
	}
	return peers[min(m.contactUI.Index, len(peers)-1)]
}

func (m *chatModel) changeContact(peer string, change native.ContactChange) tea.Cmd {
	if m.contactUI.Busy {
		return nil
	}
	if m.offline {
		m.contactUI.Notice = "Contact changes require a connection to WeChat"
		return nil
	}
	if peer == "" || peer == m.owner || peer == "filehelper" {
		m.contactUI.Notice = "Choose a person or group to edit"
		return nil
	}
	if err := change.Validate(peer); err != nil {
		m.contactUI.Notice = err.Error()
		return nil
	}
	backend, ok := m.backend.(contactBackend)
	if !ok {
		m.contactUI.Notice = "Contact updates are unavailable"
		return nil
	}
	m.contactUI.Busy = true
	m.contactUI.Notice = "Saving to WeChat…"
	return func() tea.Msg { return backend.updateContact(m.ctx, peer, change) }
}

func (m chatModel) contactKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	ui := &m.contactUI
	key := msg.String()
	if !msg.Paste {
		switch key {
		case "ctrl+c", "ctrl+q":
			return m, tea.Quit
		case "esc":
			if ui.Editing {
				ui.Editing = false
				ui.Editor.Blur()
				return m, ui.Search.Focus()
			}
			ui.Open = false
			return m, m.setFocus(focusCompose)
		}
	}
	if ui.Editing {
		if !msg.Paste && key == "enter" {
			value := ui.Editor.Value()
			cmd := m.changeContact(ui.EditPeer, native.ContactChange{Remark: &value})
			return m, cmd
		}
		if !ui.Busy {
			var cmd tea.Cmd
			ui.Editor, cmd = ui.Editor.Update(msg)
			return m, cmd
		}
		return m, nil
	}
	if !msg.Paste {
		switch key {
		case "up", "down", "pgup", "pgdown", "home", "end":
			count := len(m.contactPeers())
			switch key {
			case "up":
				ui.Index--
			case "down":
				ui.Index++
			case "pgup":
				ui.Index -= max(1, m.height-16)
			case "pgdown":
				ui.Index += max(1, m.height-16)
			case "home":
				ui.Index = 0
			case "end":
				ui.Index = count - 1
			}
			ui.Index = max(0, min(ui.Index, count-1))
			return m, nil
		case "tab", "shift+tab":
			direction := 1
			if key == "shift+tab" {
				direction = len(contactFilters) - 1
			}
			ui.Filter = (ui.Filter + direction) % len(contactFilters)
			ui.Index = 0
			return m, nil
		case "enter":
			if peer := m.contactPeer(); peer != "" {
				ui.Open = false
				if !m.matchesChatFilter(peer, m.chatFilter) {
					m.chatFilter = chatFilterAll
				}
				m.query = ""
				m.search.SetValue("")
				m.selectPeer(peer)
				m.rebuildRows()
				return m, m.setFocus(focusCompose)
			}
			return m, nil
		case "ctrl+r":
			if peer := m.contactPeer(); peer != "" && !ui.Busy {
				ui.EditPeer = peer
				ui.Editor.SetValue(m.contacts[peer].Remark)
				ui.Editor.CursorEnd()
				ui.Editing = true
				ui.Search.Blur()
				return m, ui.Editor.Focus()
			}
			return m, nil
		case "ctrl+p":
			peer := m.contactPeer()
			pin := !m.contacts[peer].Pinned()
			cmd := m.changeContact(peer, native.ContactChange{Pinned: &pin})
			return m, cmd
		}
	}
	var cmd tea.Cmd
	before := ui.Search.Value()
	ui.Search, cmd = ui.Search.Update(msg)
	if before != ui.Search.Value() {
		ui.Index = 0
	}
	return m, cmd
}

func (m chatModel) contactsView() string {
	if m.width < 64 || m.height < 18 {
		return "Contacts\nResize the terminal to at least 64 × 18.\nEsc to return."
	}
	inner := m.width - 6
	m.contactUI.Search.Width = max(1, inner-2)
	m.contactUI.Editor.Width = max(1, inner-10)
	var filters []string
	for i, name := range contactFilters {
		if i == m.contactUI.Filter {
			name = "[" + name + "]"
		}
		filters = append(filters, name)
	}
	peers := m.contactPeers()
	lines := []string{chatTitle.Render("CONTACTS") + chatDim.Render(fmt.Sprintf("  %d known contacts / chats", len(peers))), chatDim.Render(ansi.Truncate(strings.Join(filters, " · "), inner, "…")), m.contactUI.Search.View(), ""}
	slots := max(1, m.height-16)
	index := min(m.contactUI.Index, max(0, len(peers)-1))
	start := min(max(0, index-slots/2), max(0, len(peers)-slots))
	for i := start; i < min(len(peers), start+slots); i++ {
		peer := peers[i]
		prefix, suffix := "  ", ""
		if m.contacts[peer].Pinned() {
			suffix = " · PIN"
		}
		if strings.HasSuffix(peer, "@chatroom") {
			suffix += " · Group"
		}
		if m.contact(peer).Official() {
			suffix += " · Official"
		}
		if m.contact(peer).Muted() {
			suffix += " · Muted"
		}
		style := lipgloss.NewStyle()
		if i == index {
			prefix = "› "
			style = chatTitle
		}
		lines = append(lines, style.Render(ansi.Truncate(prefix+m.name(peer)+suffix+"  "+oneLine(peer), inner, "…")))
	}
	for len(lines) < 4+slots {
		lines = append(lines, "")
	}
	peer := m.contactPeer()
	c := m.contacts[peer]
	lines = append(lines, chatDim.Render(strings.Repeat("─", inner)))
	if peer == "" {
		lines = append(lines, "No matching contacts.", "", "")
	} else {
		lines = append(lines, "Name: "+oneLine(c.Nickname)+"   Alias: "+oneLine(c.Alias), "Remark: "+oneLine(c.Remark), "ID: "+oneLine(peer))
	}
	if m.contactUI.Editing {
		lines = append(lines, m.contactUI.Editor.View(), chatDim.Render("Enter saves to WeChat · empty removes remark · Esc cancels"))
	} else {
		lines = append(lines, chatDim.Render("Ctrl+R edit remark · Ctrl+P pin/unpin on WeChat"), chatDim.Render("Type to search · ↑↓ select · Tab filter · Enter chat · Esc back"))
	}
	lines = append(lines, chatDim.Render(chatSafeText(m.contactUI.Notice)))
	return chatPanel(strings.Join(lines, "\n"), m.width, m.height, true)
}
