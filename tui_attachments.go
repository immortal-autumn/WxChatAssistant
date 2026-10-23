package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/kevmo314/wechat-cli/internal/native"
)

type attachmentBackend interface {
	attachment(context.Context, native.Message) chatAttachmentResult
}

func (m *chatModel) openAttachments() {
	m.mediaOpen = true
	m.mediaIndex = 0
	m.mediaPath = ""
	m.mediaPreview = ""
	m.mediaErr = ""
	m.mediaItems = nil
	for i := len(m.messages) - 1; i >= 0; i-- {
		v := m.messages[i]
		if v.Peer == m.selected {
			if _, ok := native.AttachmentFor(v); ok {
				m.mediaItems = append(m.mediaItems, v)
			}
		}
	}
}
func (m chatModel) attachmentKey(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "esc", "ctrl+o":
		m.mediaOpen = false
		return m, nil
	case "ctrl+c", "ctrl+q":
		return m, tea.Quit
	case "b":
		m.mediaPath = ""
		m.mediaPreview = ""
		m.mediaErr = ""
		return m, nil
	case "up":
		m.mediaIndex = max(0, m.mediaIndex-1)
		return m, nil
	case "down":
		m.mediaIndex = min(max(0, len(m.mediaItems)-1), m.mediaIndex+1)
		return m, nil
	case "enter":
		if m.offline {
			m.mediaErr = "Offline mode · downloads disabled"
			return m, nil
		}
		if m.mediaLoading || len(m.mediaItems) == 0 {
			return m, nil
		}
		backend, ok := m.backend.(attachmentBackend)
		if !ok {
			m.mediaErr = "Attachment downloads are unavailable"
			return m, nil
		}
		m.mediaLoading = true
		m.mediaErr = ""
		message := m.mediaItems[m.mediaIndex]
		width, height := m.width, m.height
		return m, func() tea.Msg {
			result := backend.attachment(m.ctx, message)
			if result.Err == nil {
				if meta, _ := native.AttachmentFor(message); meta.Kind == "image" {
					preview, err := imagePreview(result.Path, max(1, width-10), max(1, height-10))
					result.Preview = preview
					if err != nil {
						result.Err = err
					}
				}
			}
			return result
		}
	}
	return m, nil
}
func (m chatModel) attachmentsView() string {
	width := m.width - 4
	inner := width - 4
	lines := []string{chatTitle.Render("Attachments · " + m.name(m.selected)), ""}
	if m.mediaPath != "" {
		lines = append(lines, chatDim.Render(ansi.Truncate("Saved: "+m.mediaPath, inner, "…")), "")
		if m.mediaPreview != "" {
			lines = append(lines, m.mediaPreview)
		} else {
			lines = append(lines, "The file has been saved to the path above.")
		}
	} else if len(m.mediaItems) == 0 {
		lines = append(lines, chatDim.Render("No attachments in the synchronized history."))
	} else {
		count := max(1, m.height-10)
		start := max(0, m.mediaIndex-count+1)
		for i := start; i < min(len(m.mediaItems), start+count); i++ {
			v := m.mediaItems[i]
			meta, _ := native.AttachmentFor(v)
			label := fmt.Sprintf("%s  %-6s  %s", time.Unix(int64(v.Timestamp), 0).Format("Jan 02 15:04"), meta.Kind, oneLine(meta.Name))
			label = ansi.Truncate(label, inner-2, "…")
			if i == m.mediaIndex {
				label = chatTitle.Render("› " + label)
			} else {
				label = "  " + chatDim.Render(label)
			}
			lines = append(lines, label)
		}
	}
	if m.mediaLoading {
		lines = append(lines, "", chatTitle.Render("Downloading…"))
	}
	if m.mediaErr != "" {
		lines = append(lines, "", ansi.Wrap(chatSafeText(m.mediaErr), inner, ""))
	}
	lines = append(lines, "", chatDim.Render("↑↓ choose  Enter download / preview  b back  Esc close"))
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, chatPanel(strings.Join(lines, "\n"), width, m.height-2, true))
}
