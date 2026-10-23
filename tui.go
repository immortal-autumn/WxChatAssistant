package main

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/term"
	"github.com/kevmo314/wechat-cli/internal/native"
)

type chatBackend interface {
	sync(context.Context) chatSyncResult
	send(context.Context, string, string) chatSendResult
}
type chatTick struct{}
type chatConversation struct {
	Peer  string
	Last  native.Message
	Count int
}

const (
	focusChats = iota
	focusHistory
	focusCompose
	focusSearch
)

var (
	chatGreen  = lipgloss.AdaptiveColor{Light: "#166534", Dark: "#7ee787"}
	chatMuted  = lipgloss.AdaptiveColor{Light: "#64748b", Dark: "#8b949e"}
	chatBorder = lipgloss.AdaptiveColor{Light: "#cbd5e1", Dark: "#303d49"}
	chatTitle  = lipgloss.NewStyle().Bold(true).Foreground(chatGreen)
	chatDim    = lipgloss.NewStyle().Foreground(chatMuted)
)

type chatModel struct {
	ctx                            context.Context
	backend                        chatBackend
	owner, profile                 string
	offline                        bool
	width, height                  int
	focus                          int
	search                         textinput.Model
	composer                       textarea.Model
	viewport                       viewport.Model
	messages                       []native.Message
	contacts                       map[string]native.Contact
	seen                           map[string]bool
	rows                           []chatConversation
	selected                       string
	selectedIndex, listOffset      int
	drafts                         map[string]string
	unread                         map[string]int
	query                          string
	newChat                        bool
	syncing, sending, paused, help bool
	lastSync                       time.Time
	syncErr, notice                string
	retries                        int
	pendingPeer, pendingText       string
	pendingAt                      uint32
	failedDrafts                   map[string]string
	activity                       map[string]uint64
	latest                         map[string]native.Message
	activityClock                  uint64
	terminalFocused                bool
}

func newChatModel(ctx context.Context, backend chatBackend, owner, profile string, data chatSnapshot, offline bool) chatModel {
	search := textinput.New()
	search.Prompt = "/ "
	search.Placeholder = "Search chats"
	search.CharLimit = 256
	composer := textarea.New()
	composer.Prompt = ""
	composer.ShowLineNumbers = false
	composer.Placeholder = "Write a message…"
	composer.CharLimit = 60 * 1024
	composer.SetHeight(3)
	composer.MaxWidth = 0
	composer.KeyMap.InsertNewline = key.NewBinding(key.WithKeys("alt+enter", "ctrl+j"))
	composer.KeyMap.Paste.SetEnabled(false) // Bracketed terminal paste works without a clipboard subprocess.
	m := chatModel{ctx: ctx, backend: backend, owner: owner, profile: profile, offline: offline, syncing: !offline, focus: focusChats, search: search, composer: composer, viewport: viewport.New(60, 12), contacts: data.Contacts, seen: map[string]bool{}, failedDrafts: map[string]string{}, activity: map[string]uint64{}, latest: map[string]native.Message{}, terminalFocused: true, drafts: map[string]string{}, unread: map[string]int{}, width: 100, height: 30}
	if m.contacts == nil {
		m.contacts = map[string]native.Contact{}
	}
	m.addMessages(data.Messages, false)
	m.rebuildRows()
	m.resize(100, 30)
	return m
}
func (m chatModel) Init() tea.Cmd {
	if m.offline {
		return nil
	}
	return m.syncCommand()
}
func (m chatModel) syncCommand() tea.Cmd { return func() tea.Msg { return m.backend.sync(m.ctx) } }
func chatWait(d time.Duration) tea.Cmd {
	return tea.Tick(d, func(time.Time) tea.Msg { return chatTick{} })
}
func (m *chatModel) addMessages(messages []native.Message, incoming bool) {
	for _, v := range messages {
		if m.seen[v.ID] {
			continue
		}
		m.seen[v.ID] = true
		m.messages = append(m.messages, v)
		if incoming && visibleChatMessage(v) {
			m.activityClock++
			m.activity[v.Peer] = m.activityClock
			m.latest[v.Peer] = v
		}
		if incoming && !v.Outgoing && visibleChatMessage(v) && (v.Peer != m.selected || !m.terminalFocused || !m.viewport.AtBottom()) {
			m.unread[v.Peer]++
		}
	}
	sort.SliceStable(m.messages, func(i, j int) bool { return m.messages[i].Timestamp < m.messages[j].Timestamp })
}
func visibleChatMessage(v native.Message) bool { return v.Peer != "" && v.Type != 51 && v.Type != 9999 }
func chatSafeText(s string) string {
	s = ansi.Strip(strings.ToValidUTF8(s, "�"))
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) && r != '\n' && r != '\t' {
			return -1
		}
		return r
	}, s)
}
func oneLine(s string) string { return strings.Join(strings.Fields(chatSafeText(s)), " ") }
func (m chatModel) name(peer string) string {
	if c, ok := m.contacts[peer]; ok {
		for _, name := range []string{c.Remark, c.Nickname, c.Alias} {
			if name = oneLine(name); name != "" {
				return name
			}
		}
	}
	if peer == m.owner {
		return "Saved messages"
	}
	if peer == "filehelper" {
		return "File Transfer"
	}
	return oneLine(peer)
}
func xmlText(s, element string) string {
	if len(s) > 128*1024 {
		return ""
	}
	decoder := xml.NewDecoder(strings.NewReader(s))
	for {
		token, err := decoder.Token()
		if err != nil {
			return ""
		}
		if start, ok := token.(xml.StartElement); ok && start.Name.Local == element {
			var text string
			if decoder.DecodeElement(&text, &start) == nil {
				return chatSafeText(text)
			}
			return ""
		}
	}
}
func messagePreview(v native.Message) string {
	switch v.Type {
	case 1:
		return chatSafeText(v.Text)
	case 3:
		return "▧ Image"
	case 34:
		return "◉ Voice message"
	case 43, 62:
		return "▷ Video"
	case 47:
		return "☺ Sticker"
	case 48:
		return "⌖ Location"
	case 49:
		if title := xmlText(v.Text, "title"); title != "" {
			return "↗ " + title
		}
		return "↗ Shared item"
	case 37:
		return "Contact request"
	case 10002:
		if text := xmlText(v.Text, "replacemsg"); text != "" {
			return text
		}
		return "Conversation updated"
	case 10000:
		return chatSafeText(v.Text)
	default:
		return "Unsupported message · available in the CLI archive"
	}
}
func (m *chatModel) rebuildRows() {
	peers := map[string]chatConversation{}
	for _, v := range m.messages {
		if !visibleChatMessage(v) {
			continue
		}
		c := peers[v.Peer]
		c.Peer = v.Peer
		c.Last = v
		c.Count++
		peers[v.Peer] = c
	}
	if m.newChat {
		for peer := range m.contacts {
			if _, ok := peers[peer]; !ok {
				peers[peer] = chatConversation{Peer: peer}
			}
		}
	}
	if m.selected != "" {
		if _, ok := peers[m.selected]; !ok {
			peers[m.selected] = chatConversation{Peer: m.selected}
		}
	}
	m.rows = nil
	query := strings.ToLower(strings.TrimSpace(m.query))
	for peer, c := range peers {
		if latest, ok := m.latest[peer]; ok {
			c.Last = latest
		}
		if query != "" && !strings.Contains(strings.ToLower(m.name(c.Peer)+" "+c.Peer+" "+m.contacts[c.Peer].Alias), query) {
			continue
		}
		m.rows = append(m.rows, c)
	}
	sort.Slice(m.rows, func(i, j int) bool {
		if m.activity[m.rows[i].Peer] != m.activity[m.rows[j].Peer] {
			return m.activity[m.rows[i].Peer] > m.activity[m.rows[j].Peer]
		}
		if m.rows[i].Last.Timestamp != m.rows[j].Last.Timestamp {
			return m.rows[i].Last.Timestamp > m.rows[j].Last.Timestamp
		}
		return m.name(m.rows[i].Peer) < m.name(m.rows[j].Peer)
	})
	if len(m.rows) == 0 && m.newChat && strings.TrimSpace(m.query) != "" && !strings.ContainsAny(m.query, " \n\r\t") {
		m.rows = []chatConversation{{Peer: strings.TrimSpace(m.query)}}
	}
	m.selectedIndex = -1
	for i, c := range m.rows {
		if c.Peer == m.selected {
			m.selectedIndex = i
			break
		}
	}
	if m.selectedIndex < 0 && len(m.rows) > 0 {
		m.selectPeer(m.rows[0].Peer)
		m.selectedIndex = 0
	}
	m.ensureListVisible()
}
func (m *chatModel) selectPeer(peer string) {
	if peer == m.selected {
		return
	}
	if m.selected != "" {
		m.drafts[m.selected] = m.composer.Value()
	}
	m.selected = peer
	m.composer.SetValue(m.drafts[peer])
	m.unread[peer] = 0
	m.refreshHistory(true)
}
func (m *chatModel) ensureListVisible() {
	slots := max(1, (m.height-8)/3)
	if m.selectedIndex < m.listOffset {
		m.listOffset = max(0, m.selectedIndex)
	}
	if m.selectedIndex >= m.listOffset+slots {
		m.listOffset = m.selectedIndex - slots + 1
	}
	m.listOffset = min(m.listOffset, max(0, len(m.rows)-slots))
}
func (m *chatModel) setFocus(focus int) tea.Cmd {
	m.focus = focus
	m.search.Blur()
	m.composer.Blur()
	switch focus {
	case focusSearch:
		return m.search.Focus()
	case focusCompose:
		m.unread[m.selected] = 0
		return m.composer.Focus()
	}
	return nil
}
func (m *chatModel) resize(w, h int) {
	m.width = w
	m.height = h
	left := min(34, max(24, w/3))
	inner := max(16, w-left-4)
	m.search.Width = max(10, left-8)
	m.composer.SetWidth(inner)
	m.composer.SetHeight(3)
	m.viewport.Width = inner
	m.viewport.Height = max(1, h-12)
	m.ensureListVisible()
	m.refreshHistory(true)
}
func (m *chatModel) refreshHistory(bottom bool) {
	var parts []string
	lastDate := ""
	for _, v := range m.messages {
		if v.Peer != m.selected || !visibleChatMessage(v) {
			continue
		}
		date := time.Unix(int64(v.Timestamp), 0).Format("Monday, Jan 2, 2006")
		if date != lastDate {
			parts = append(parts, chatDim.Render("── "+date+" ──"))
			lastDate = date
		}
		text := messagePreview(v)
		sender := m.name(v.From)
		if v.Outgoing {
			sender = "You"
		} else if strings.HasSuffix(v.From, "@chatroom") {
			if first, rest, ok := strings.Cut(text, ":\n"); ok && len(first) < 256 {
				sender = m.name(first)
				text = rest
			}
		}
		label := ansi.Truncate(sender, max(8, m.viewport.Width/3), "…") + " " + time.Unix(int64(v.Timestamp), 0).Format("15:04") + "  "
		style := chatDim
		if v.Outgoing {
			style = chatTitle
		}
		parts = append(parts, ansi.Wrap(style.Render(label)+text, max(1, m.viewport.Width), ""))
	}
	if m.sending && m.pendingPeer == m.selected {
		parts = append(parts, ansi.Wrap(chatDim.Render("You · sending  ")+chatSafeText(m.pendingText), max(1, m.viewport.Width), ""))
	}
	if len(parts) == 0 {
		parts = []string{chatDim.Render("No messages yet."), "", chatDim.Render("Press Tab to compose a message.")}
	}
	m.viewport.SetContent(strings.Join(parts, "\n"))
	if bottom {
		m.viewport.GotoBottom()
	}
}
func (m *chatModel) submit() tea.Cmd {
	if m.sending || m.selected == "" {
		return nil
	}
	if m.offline {
		m.notice = "Offline mode · sending is disabled"
		return nil
	}
	peer, text := m.selected, m.composer.Value()
	if strings.TrimSpace(text) == "" {
		return nil
	}
	if len(text) > 60*1024 {
		m.notice = "Message exceeds 60 KiB"
		return nil
	}
	m.sending = true
	m.notice = "Sending…"
	m.pendingPeer, m.pendingText, m.pendingAt = peer, text, uint32(time.Now().Unix())
	delete(m.drafts, peer)
	m.composer.Reset()
	m.refreshHistory(true)
	return func() tea.Msg { return m.backend.send(m.ctx, peer, text) }
}
func (m chatModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.resize(msg.Width, msg.Height)
		return m, nil
	case chatTick:
		if m.offline || m.paused || m.syncing {
			return m, nil
		}
		m.syncing = true
		return m, m.syncCommand()
	case chatSyncResult:
		m.syncing = false
		if msg.Err != nil {
			m.syncErr = chatSafeText(msg.Err.Error())
			m.retries++
			if msg.Retry && !m.offline {
				return m, chatWait(time.Duration(min(60, 3*(1<<min(m.retries, 4)))) * time.Second)
			}
			m.paused = true
			return m, nil
		}
		bottom := m.viewport.AtBottom()
		for peer, c := range msg.Contacts {
			m.contacts[peer] = c
		}
		m.addMessages(msg.Messages, true)
		m.rebuildRows()
		m.refreshHistory(bottom)
		m.lastSync = time.Now()
		m.syncErr = ""
		m.retries = 0
		if msg.More {
			m.syncing = true
			return m, m.syncCommand()
		}
		return m, chatWait(3 * time.Second)
	case chatSendResult:
		m.sending = false
		if msg.Accepted {
			m.addMessages([]native.Message{msg.Message}, true)

			m.rebuildRows()
			m.refreshHistory(true)
			m.notice = "Sent · accepted by WeChat"
		}
		if msg.Err != nil {
			if !msg.Accepted {
				if m.selected == msg.Peer && m.composer.Value() == "" {
					m.composer.SetValue(msg.Text)
				} else if m.drafts[msg.Peer] == "" && m.selected != msg.Peer {
					m.drafts[msg.Peer] = msg.Text
				} else {
					m.failedDrafts[msg.Peer] = msg.Text
				}
			}
			m.notice = chatSafeText(msg.Err.Error()) + " · check before retrying; Ctrl+Y restores failed text"
		}
		m.pendingPeer, m.pendingText = "", ""
		m.refreshHistory(true)
		return m, nil
	case tea.KeyMsg:
		if !msg.Paste {
			switch msg.String() {
			case "ctrl+c", "ctrl+q":
				return m, tea.Quit
			case "ctrl+k":
				m.newChat = false
				return m, m.setFocus(focusSearch)
			case "ctrl+n":
				m.newChat = true
				m.query = ""
				m.search.SetValue("")
				m.rebuildRows()
				return m, m.setFocus(focusSearch)
			case "ctrl+y":
				if text := m.failedDrafts[m.selected]; text != "" {
					m.failedDrafts[m.selected] = m.composer.Value()
					m.composer.SetValue(text)
				}
				return m, nil
			case "ctrl+s":
				return m, m.submit()
			case "pgup":
				m.viewport.HalfPageUp()
				return m, nil
			case "pgdown":
				m.viewport.HalfPageDown()
				return m, nil
			case "ctrl+home":
				m.viewport.GotoTop()
				return m, nil
			case "ctrl+end":
				m.viewport.GotoBottom()
				return m, nil
			case "esc":
				if m.help {
					m.help = false
					return m, nil
				}
				if m.focus == focusSearch {
					m.query = ""
					m.search.SetValue("")
					m.newChat = false
					m.rebuildRows()
				}
				return m, m.setFocus(focusChats)
			case "tab":
				return m, m.setFocus((m.focus + 1) % 3)
			case "shift+tab":
				return m, m.setFocus((m.focus + 2) % 3)
			}
			if m.help {
				m.help = false
				return m, nil
			}
			if m.focus == focusChats || m.focus == focusHistory {
				switch msg.String() {
				case "q":
					return m, tea.Quit
				case "?":
					m.help = true
					return m, nil
				case "/":
					return m, m.setFocus(focusSearch)
				case "n":
					m.newChat = true
					m.rebuildRows()
					return m, m.setFocus(focusSearch)
				case "r":
					if !m.syncing && !m.offline {
						m.paused = false
						m.syncing = true
						return m, m.syncCommand()
					}
					return m, nil
				}
			}
			if m.focus == focusCompose && msg.String() == "enter" {
				return m, m.submit()
			}
			if m.focus == focusSearch && msg.String() == "enter" {
				return m, m.setFocus(focusCompose)
			}
			if (m.focus == focusChats || m.focus == focusSearch) && len(m.rows) > 0 {
				delta := 0
				switch msg.String() {
				case "up":
					delta = -1
				case "down":
					delta = 1
				case "k":
					if m.focus == focusChats {
						delta = -1
					}
				case "j":
					if m.focus == focusChats {
						delta = 1
					}
				case "enter":
					return m, m.setFocus(focusCompose)
				}
				if delta != 0 {
					m.selectedIndex = max(0, min(len(m.rows)-1, m.selectedIndex+delta))
					m.selectPeer(m.rows[m.selectedIndex].Peer)
					m.ensureListVisible()
					return m, nil
				}
			}
		}
	}
	var cmd tea.Cmd
	switch m.focus {
	case focusSearch:
		m.search, cmd = m.search.Update(msg)
		if m.query != m.search.Value() {
			m.query = m.search.Value()
			m.rebuildRows()
			m.refreshHistory(true)
		}
	case focusCompose:
		m.composer, cmd = m.composer.Update(msg)
	case focusHistory:
		m.viewport, cmd = m.viewport.Update(msg)
	}
	if _, ok := msg.(tea.MouseMsg); ok && m.focus != focusHistory {
		m.viewport, cmd = m.viewport.Update(msg)
	}
	return m, cmd
}
func chatPanel(content string, w, h int, focused bool) string {
	color := chatBorder
	if focused {
		color = chatGreen
	}
	return lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(color).Padding(0, 1).Width(max(1, w-2)).Height(max(1, h-2)).Render(content)
}
func (m chatModel) View() string {
	if m.width < 64 || m.height < 18 {
		return "WeChat\nResize the terminal to at least 64 × 18.\nCtrl+C to quit."
	}
	if m.help {
		help := "WeChat · keyboard shortcuts\n\nTab / Shift+Tab   Switch panes\n↑ ↓ / j k         Select conversation\nEnter             Compose / send\nAlt+Enter         Insert a new line\nCtrl+S            Send from any pane\nCtrl+K / /        Search conversations\nCtrl+N / n        Find a contact / start a chat\nPgUp / PgDn       Scroll messages\nCtrl+Home / End   Oldest / newest message\nr                 Retry or refresh sync\nEsc               Back to conversations\nCtrl+C / Ctrl+Q   Quit\n\nDrafts stay with their recipient. Sends are never retried automatically.\nImages, voice, and files appear as previews; media transfer is not available.\n\nPress any key to return."
		return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, chatPanel(ansi.Wrap(help, m.width-10, ""), min(m.width-4, 78), min(m.height-2, 25), true))
	}
	state := "Connecting"
	if !m.lastSync.IsZero() {
		state = "Connected · " + m.lastSync.Format("15:04:05")
	}
	if m.syncing {
		state = "Syncing…"
	}
	if m.offline {
		state = "Offline archive"
	}
	if m.syncErr != "" {
		state = "Sync stopped · r to retry"
	}
	profile := "Desktop"
	if m.profile == native.AndroidTabletProfile {
		profile = "Tablet"
	}
	brand := chatTitle.Render("●  WeChat") + chatDim.Render("  "+profile)
	top := brand + strings.Repeat(" ", max(1, m.width-lipgloss.Width(brand)-lipgloss.Width(state)-2)) + chatDim.Render(state)
	leftW := min(34, max(24, m.width/3))
	bodyH := m.height - 3
	rightW := m.width - leftW
	leftInner := leftW - 4
	title := "CONVERSATIONS"
	if m.newChat {
		title = "CONTACTS"
	}
	sidebar := chatTitle.Render(title) + chatDim.Render(fmt.Sprintf("  %d", len(m.rows))) + "\n"
	if m.focus == focusSearch {
		sidebar += m.search.View()
	} else if m.query != "" {
		sidebar += chatDim.Render("/ " + ansi.Truncate(oneLine(m.query), leftInner-2, "…"))
	} else {
		sidebar += chatDim.Render("/ Search    n New chat")
	}
	sidebar += "\n\n"
	slots := max(1, (m.height-8)/3)
	for i := m.listOffset; i < min(len(m.rows), m.listOffset+slots); i++ {
		row := m.rows[i]
		name := m.name(row.Peer)
		if m.unread[row.Peer] > 0 {
			name += fmt.Sprintf(" · %d", m.unread[row.Peer])
		}
		preview := "Start a conversation"
		if row.Count > 0 {
			preview = oneLine(messagePreview(row.Last))
		}
		if draft := m.drafts[row.Peer]; draft != "" {
			preview = "Draft: " + oneLine(draft)
		}
		text := ansi.Truncate(name, leftInner, "…") + "\n" + ansi.Truncate(preview, leftInner, "…")
		if row.Peer == m.selected {
			text = lipgloss.NewStyle().Background(lipgloss.Color("#174b37")).Foreground(lipgloss.Color("#ffffff")).Width(leftInner).Render(text)
		} else {
			text = chatDim.Render(text)
		}
		sidebar += text + "\n\n"
	}
	if len(m.rows) == 0 {
		sidebar += chatDim.Render("No matching conversations.\nCtrl+N to find a contact.")
	}
	name := m.name(m.selected)
	if m.selected == "" {
		name = "Welcome to WeChat"
	}
	inner := rightW - 4
	header := ansi.Truncate(chatTitle.Render(name)+chatDim.Render("  "+oneLine(m.selected)), inner, "…") + "\n" + chatDim.Render(strings.Repeat("─", inner))
	label := "MESSAGE  ·  Enter send  ·  Alt+Enter newline"
	if m.sending {
		label = "SENDING…"
	}
	if m.offline {
		label = "OFFLINE  ·  Drafts only"
	}
	thread := header + "\n" + m.viewport.View() + "\n" + chatDim.Render(strings.Repeat("─", inner)) + "\n" + chatDim.Render(ansi.Truncate(label, inner, "…")) + "\n" + m.composer.View()
	body := lipgloss.JoinHorizontal(lipgloss.Top, chatPanel(sidebar, leftW, bodyH, m.focus == focusChats || m.focus == focusSearch), chatPanel(thread, rightW, bodyH, m.focus == focusHistory || m.focus == focusCompose))
	status := m.notice
	if status == "" {
		status = m.syncErr
	}
	if status == "" {
		status = "Drafts stay with each conversation.  Messages are sent only on Enter or Ctrl+S."
	}
	footer := " Tab panes   ↑↓ chats   / search   n new chat   PgUp/PgDn scroll   ? help   Ctrl+C quit"
	return top + "\n" + body + "\n" + chatDim.Render(ansi.Truncate(oneLine(status), m.width, "…")) + "\n" + chatDim.Render(ansi.Truncate(footer, m.width, "…"))
}

func nativeTUI(ctx context.Context, store Store, args []string) error {
	f := flags("tui")
	offline := f.Bool("offline", false, "browse the local archive without connecting or sending")
	if err := parse(f, args); err != nil {
		return err
	}
	if !term.IsTerminal(os.Stdin.Fd()) || !term.IsTerminal(os.Stdout.Fd()) {
		return errors.New("the TUI requires a terminal; use wechat read --json or wechat history for pipelines")
	}
	s, err := openNativeStore(store)
	if err != nil {
		return err
	}
	state, err := s.state()
	if err != nil {
		return err
	}
	if state.Account == nil {
		if *offline {
			return errors.New("no saved account; run wechat login first")
		}
		if err = nativeLogin(ctx, store, s, []string{"--profile", native.AndroidTabletProfile}); err != nil {
			return err
		}
		state, err = s.state()
		if err != nil {
			return err
		}
	}
	reader, err := store.readerLock()
	if err != nil {
		return err
	}
	defer reader.Close()
	data, err := s.chatSnapshot(state.Account.Username)
	if err != nil {
		return err
	}
	client := &chatService{store: s, owner: state.Account.Username}
	uiCtx, cancel := context.WithCancel(ctx)
	model := newChatModel(uiCtx, client, state.Account.Username, state.Account.Profile, data, *offline)
	_, err = tea.NewProgram(model, tea.WithAltScreen(), tea.WithMouseCellMotion(), tea.WithContext(uiCtx)).Run()
	cancel()
	client.close()
	if errors.Is(err, tea.ErrProgramKilled) && ctx.Err() != nil {
		return ctx.Err()
	}
	if errors.Is(err, io.EOF) {
		return nil
	}
	return err
}
