package main

import "github.com/immortal-autumn/WxChatAssistant/internal/native"

type chatFilter int

const (
	chatFilterMain chatFilter = iota
	chatFilterPeople
	chatFilterGroups
	chatFilterOfficial
	chatFilterMuted
	chatFilterAll
	chatFilterCount
)

func (f chatFilter) label() string {
	return [...]string{"Main", "People", "Groups", "Official", "Muted", "All"}[f]
}

func (f chatFilter) next(direction int) chatFilter {
	return (f + chatFilter(direction) + chatFilterCount) % chatFilterCount
}

func (m chatModel) contact(peer string) native.Contact {
	c := m.contacts[peer]
	c.Username = peer // IDs also identify groups and gh_ accounts before metadata arrives.
	return c
}

func (m chatModel) matchesChatFilter(peer string, filter chatFilter) bool {
	c := m.contact(peer)
	switch filter {
	case chatFilterMain:
		return !c.Official() && !c.Muted()
	case chatFilterPeople:
		return !c.Group() && !c.Official()
	case chatFilterGroups:
		return c.Group()
	case chatFilterOfficial:
		return c.Official()
	case chatFilterMuted:
		return c.Muted()
	default:
		return true
	}
}

func (m *chatModel) selectFirstMatchingChat() {
	if m.selectedIndex < 0 && len(m.rows) > 0 {
		m.selectPeer(m.rows[0].Peer)
		m.selectedIndex = 0
		m.ensureListVisible()
	}
}

func (m *chatModel) cycleChatFilter(direction int) {
	m.chatFilter = m.chatFilter.next(direction)
	m.listOffset = 0
	m.rebuildRows()
	m.selectFirstMatchingChat()
	if len(m.rows) == 0 {
		m.selectPeer("") // Save the old draft; an empty category has no recipient.
	}
}

func (m chatModel) foldedConversations() int {
	peers := map[string]bool{}
	for _, message := range m.messages {
		if visibleChatMessage(message) {
			peers[message.Peer] = true
		}
	}
	for peer, c := range m.contacts {
		if c.Pinned() {
			peers[peer] = true
		}
	}
	n := 0
	for peer := range peers {
		if !m.matchesChatFilter(peer, chatFilterMain) {
			n++
		}
	}
	return n
}

// Quiet categories retain their per-chat badges without ringing the terminal
// bell or increasing the attention count in the window title.
func (m chatModel) attentionUnread() int {
	n := 0
	for peer, count := range m.unread {
		if m.matchesChatFilter(peer, chatFilterMain) {
			n += count
		}
	}
	return n
}
