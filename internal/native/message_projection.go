package native

import (
	"encoding/xml"
	"strconv"
	"strings"
	"unicode"
)

type Recall struct{ Peer, Target, LegacyTarget, Notice string }

func RecallFor(m Message) (Recall, bool) {
	if m.Type != 10002 || len(m.Text) > 128*1024 {
		return Recall{}, false
	}
	var data struct {
		Kind   string `xml:"type,attr"`
		Recall struct {
			Session  string `xml:"session"`
			ID       string `xml:"newmsgid"`
			LegacyID string `xml:"msgid"`
			Notice   string `xml:"replacemsg"`
		} `xml:"revokemsg"`
	}
	text := m.Text
	if i := strings.IndexByte(text, '<'); i >= 0 {
		text = text[i:]
	}
	if xml.Unmarshal([]byte(text), &data) != nil || data.Kind != "revokemsg" {
		return Recall{}, false
	}
	peer := data.Recall.Session
	if peer == "" {
		peer = m.Peer
	}
	if peer == "" || (data.Recall.ID == "" && data.Recall.LegacyID == "") {
		return Recall{}, false
	}
	return Recall{Peer: peer, Target: data.Recall.ID, LegacyTarget: data.Recall.LegacyID, Notice: data.Recall.Notice}, true
}
func NormalizeMessage(m *Message) {
	if m.Sender == "" && strings.HasSuffix(m.From, "@chatroom") && !m.Outgoing {
		if sender, body, ok := strings.Cut(m.Text, ":\n"); ok && len(sender) > 0 && len(sender) <= 256 && !strings.ContainsAny(sender, "<>:/ ") && !strings.ContainsFunc(sender, unicode.IsControl) {
			m.Sender = sender
			m.Text = body
		}
	}
	if r, ok := RecallFor(*m); ok {
		m.Peer = r.Peer
		m.RecallTarget = r.Target
	}
}

// ProjectMessages adds display state without changing the retained original
// payload. Recall identifiers stay scoped to their conversation.
func ProjectMessages(messages []Message) {
	index := map[string]int{}
	legacy := map[string]int{}
	for i := range messages {
		NormalizeMessage(&messages[i])
		m := messages[i]
		index[m.Peer+"/"+m.ID] = i
		if m.LegacyID != 0 {
			legacy[m.Peer+"/"+strconv.FormatUint(uint64(m.LegacyID), 10)] = i
		}
	}
	for i := range messages {
		event := &messages[i]
		r, ok := RecallFor(*event)
		if !ok {
			continue
		}
		target, found := index[r.Peer+"/"+r.Target]
		if !found && r.LegacyTarget != "" {
			target, found = legacy[r.Peer+"/"+r.LegacyTarget]
		}
		if !found || target == i {
			continue
		}
		messages[target].Deleted = true
		messages[target].DeleteNotice = r.Notice
		event.Hidden = true
	}
}
