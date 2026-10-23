package main

import (
	"context"
	"errors"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strings"
	"time"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/kevmo314/wechat-cli/internal/native"
)

var webURL = regexp.MustCompile(`https?://[^\s<>"']+`)

func validWebURL(value string) (string, bool) {
	if strings.ContainsFunc(value, unicode.IsControl) {
		return "", false
	}
	u, err := url.Parse(strings.TrimSpace(value))
	if err != nil || u.Hostname() == "" || u.User != nil || (u.Scheme != "https" && u.Scheme != "http") {
		return "", false
	}
	return u.String(), true
}
func messageLinks(message native.Message) []string {
	if message.Deleted {
		return nil
	}
	var result []string
	seen := map[string]bool{}
	add := func(value string) {
		if u, ok := validWebURL(value); ok && !seen[u] {
			seen[u] = true
			result = append(result, u)
		}
	}
	if message.Type == 49 {
		add(xmlText(message.Text, "url"))
		add(xmlText(message.Text, "sourceurl"))
		for _, value := range webURL.FindAllString(xmlText(message.Text, "title"), 20) {
			add(value)
		}
	}
	if message.Type == 1 {
		for _, value := range webURL.FindAllString(message.Text, 20) {
			value = strings.TrimRight(value, ".,!?;:")
			for strings.HasSuffix(value, ")") && strings.Count(value, ")") > strings.Count(value, "(") {
				value = strings.TrimSuffix(value, ")")
			}
			add(value)
		}
	}
	return result
}
func terminalLink(uri, label string) string {
	if uri, ok := validWebURL(uri); ok {
		return "\x1b]8;;" + uri + "\x1b\\" + label + "\x1b]8;;\x1b\\"
	}
	return label
}
func linkify(text string) string {
	return webURL.ReplaceAllStringFunc(text, func(value string) string {
		uri, ok := validWebURL(strings.TrimRight(value, ".,!?;:"))
		if !ok {
			return value
		}
		return terminalLink(uri, value)
	})
}

type chatLinkResult struct {
	Notice string
	Err    error
}

func openWebURL(uri string) tea.Msg {
	uri, ok := validWebURL(uri)
	if !ok {
		return chatLinkResult{Err: errors.New("only HTTP and HTTPS links can be opened")}
	}
	var command *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		command = exec.Command("open", uri)
	case "windows":
		command = exec.Command("rundll32", "url.dll,FileProtocolHandler", uri)
	default:
		if os.Getenv("DISPLAY") == "" && os.Getenv("WAYLAND_DISPLAY") == "" {
			return chatLinkResult{Notice: "No browser on this host. Ctrl+click the displayed URL to open it locally."}
		}
		command = exec.Command("xdg-open", uri)
	}
	// The user's explicitly opened browser outlives the terminal client.
	if err := command.Start(); err != nil {
		return chatLinkResult{Err: err}
	}
	go command.Wait()
	return chatLinkResult{Notice: "Opened link in the default browser"}
}

type replyBackend interface {
	reply(context.Context, native.Message, string, string) chatSendResult
}

func (m *chatModel) openMessageActions() {
	m.actionsOpen = true
	m.actionsIndex = 0
	m.actionsLinkIndex = 0
	m.actionsDetail = nil
	m.actionsScroll = 0
	m.actionsNotice = ""
	m.actionsItems = nil
	for i := len(m.messages) - 1; i >= 0; i-- {
		v := m.messages[i]
		if v.Peer == m.selected && visibleChatMessage(v) {
			m.actionsItems = append(m.actionsItems, v)
		}
	}
}
func (m chatModel) actionMessage() (native.Message, bool) {
	if m.actionsDetail != nil {
		return *m.actionsDetail, true
	}
	if len(m.actionsItems) == 0 {
		return native.Message{}, false
	}
	return m.actionsItems[m.actionsIndex], true
}
func (m chatModel) referencedMessage(message native.Message) (native.Message, string, bool) {
	ref, ok := native.ReferenceFor(message)
	if !ok {
		return native.Message{}, "", false
	}
	for _, v := range m.messages {
		if v.ID == ref.ID && v.Peer == message.Peer {
			return v, ref.Name, true
		}
	}
	referenced := native.Message{ID: ref.ID, From: ref.From, Sender: ref.Sender, Peer: message.Peer, Type: ref.Type, Text: ref.Content, Outgoing: ref.Sender == m.owner}
	all := append(append([]native.Message{}, m.messages...), referenced)
	native.ProjectMessages(all)
	return all[len(all)-1], ref.Name, true
}
func (m chatModel) messageActionKey(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "esc", "ctrl+e":
		m.actionsOpen = false
		return m, nil
	case "ctrl+c", "ctrl+q":
		return m, tea.Quit
	case "up":
		m.actionsIndex = max(0, m.actionsIndex-1)
		m.actionsLinkIndex = 0
		m.actionsDetail = nil
		return m, nil
	case "down":
		m.actionsIndex = min(max(0, len(m.actionsItems)-1), m.actionsIndex+1)
		m.actionsLinkIndex = 0
		m.actionsDetail = nil
		return m, nil
	case "pgup":
		if m.actionsDetail != nil {
			m.actionsScroll = max(0, m.actionsScroll-max(1, m.height-12))
			return m, nil
		}
		m.actionsIndex = max(0, m.actionsIndex-5)
		m.actionsDetail = nil
		return m, nil
	case "pgdown":
		if m.actionsDetail != nil {
			m.actionsScroll += max(1, m.height-12)
			return m, nil
		}
		m.actionsIndex = min(max(0, len(m.actionsItems)-1), m.actionsIndex+5)
		m.actionsDetail = nil
		return m, nil
	case "b", "backspace":
		m.actionsDetail = nil
		return m, nil
	}
	message, ok := m.actionMessage()
	if !ok {
		return m, nil
	}
	switch key {
	case "r":
		if message.Deleted {
			m.actionsNotice = "Deleted messages cannot be quoted"
			return m, nil
		}
		m.replyTargets[message.Peer] = message
		m.actionsOpen = false
		m.notice = "Reply target selected · Ctrl+X to cancel"
		return m, m.setFocus(focusCompose)
	case "enter":
		m.actionsDetail = &message
		m.actionsDetailName = m.replyName(message)
		m.actionsScroll = 0
		return m, nil
	case "v":
		if message.Deleted {
			m.actionsNotice = "This message was deleted"
			return m, nil
		}
		if quoted, name, ok := m.referencedMessage(message); ok {
			m.actionsDetailName = name
			m.actionsScroll = 0
			m.actionsDetail = &quoted
			m.actionsLinkIndex = 0
			m.actionsNotice = "Quoted message"
		} else {
			m.actionsNotice = "This message has no quoted reference"
		}
		return m, nil

	}
	return m, nil
}
func (m chatModel) messageActionsView() string {
	width := m.width - 4
	inner := width - 4
	lines := []string{chatTitle.Render("Message actions · " + m.name(m.selected)), ""}
	if m.actionsDetail == nil {
		count := max(1, (m.height-14)/2)
		start := max(0, m.actionsIndex-count+1)
		for i := start; i < min(len(m.actionsItems), start+count); i++ {
			v := m.actionsItems[i]
			sender := m.name(v.From)
			if v.Outgoing {
				sender = "You"
			} else if v.Sender != "" {
				sender = m.senderName(v.Peer, v.Sender)
			}
			line := time.Unix(int64(v.Timestamp), 0).Format("Jan 2 15:04") + "  " + sender + "  " + oneLine(messagePreview(v))
			line = ansi.Truncate(line, inner-2, "…")
			if i == m.actionsIndex {
				line = chatTitle.Render("› " + line)
			} else {
				line = "  " + chatDim.Render(line)
			}
			lines = append(lines, line)
		}
	}
	if message, ok := m.actionMessage(); ok {
		label := "Selected message"
		if m.actionsDetail != nil {
			label = "Message details"
			if m.actionsDetailName != "" {
				label += " · " + oneLine(m.actionsDetailName)
			}
		}
		lines = append(lines, "", chatTitle.Render(label))
		preview := strings.Split(hangingMessage(messagePreview(message), inner), "\n")
		limit := 5
		start := 0
		if m.actionsDetail != nil {
			limit = max(1, m.height-12)
			start = min(m.actionsScroll, max(0, len(preview)-limit))
		}
		lines = append(lines, preview[start:min(len(preview), start+limit)]...)

	} else {
		lines = append(lines, chatDim.Render("No matching messages in the synchronized history."))
	}
	if m.actionsNotice != "" {
		lines = append(lines, "", chatDim.Render(ansi.Wrap(oneLine(m.actionsNotice), inner, "")))
	}
	lines = append(lines, "", chatDim.Render("↑↓ choose · Enter details · r reply · v quoted message · Esc close"))
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, chatPanel(strings.Join(lines, "\n"), width, m.height-2, true))
}
func (m chatModel) replyName(target native.Message) string {
	if target.Outgoing {
		return "You"
	}
	if target.Sender != "" {
		return m.senderName(target.Peer, target.Sender)
	}
	return m.name(target.From)
}
