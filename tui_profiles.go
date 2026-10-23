package main

import (
	"context"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/kevmo314/wechat-cli/internal/native"
)

type profileOption struct{ Name, Label, Detail string }

var chatProfiles = []profileOption{
	{native.AndroidTabletProfile, "Tablet", "Tested alongside other clients. Recommended."},
	{native.DesktopProfile, "Desktop", "Uses the desktop session; another computer may be logged out."},
	{native.PhoneProfile, "Phone · experimental", "May replace your primary phone session. Not yet authenticated in testing."},
}

func profileLabel(profile string) string {
	switch profile {
	case native.AndroidTabletProfile, "tablet":
		return "Tablet"
	case native.PhoneProfile:
		return "Phone"
	default:
		return "Desktop"
	}
}
func profileIndex(profile string) int {
	for i, p := range chatProfiles {
		if p.Name == profile {
			return i
		}
	}
	return 0
}
func profilePickerView(index, w, h int) string {
	width := min(76, max(32, w-6))
	var lines []string
	lines = append(lines, chatTitle.Render("Choose a WeChat profile"), "", chatDim.Render("Each profile keeps its own credentials and message archive."), "")
	for i, p := range chatProfiles {
		prefix := "  "
		if i == index {
			prefix = "› "
		}
		label := prefix + p.Label
		style := chatDim
		if i == index {
			style = chatTitle
		}
		lines = append(lines, style.Render(label), ansi.Wrap("  "+p.Detail, width-6, ""), "")
	}
	lines = append(lines, chatDim.Render("↑↓ choose   Enter open   Esc cancel"))
	return lipgloss.Place(w, h, lipgloss.Center, lipgloss.Center, chatPanel(strings.Join(lines, "\n"), width, min(max(18, h-2), 22), true))
}

type startupProfilePicker struct {
	index, w, h int
	selected    string
}

func (m startupProfilePicker) Init() tea.Cmd { return nil }
func (m startupProfilePicker) View() string  { return profilePickerView(m.index, m.w, m.h) }
func (m startupProfilePicker) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "ctrl+q", "esc":
			return m, tea.Quit
		case "up":
			m.index = (m.index + len(chatProfiles) - 1) % len(chatProfiles)
		case "down":
			m.index = (m.index + 1) % len(chatProfiles)
		case "1", "2", "3":
			m.index = int(msg.Runes[0] - '1')
		case "enter":
			m.selected = chatProfiles[m.index].Name
			return m, tea.Quit
		}
	}
	return m, nil
}
func chooseProfile(ctx context.Context) (string, error) {
	model, err := tea.NewProgram(startupProfilePicker{w: 90, h: 26}, tea.WithAltScreen(), tea.WithContext(ctx)).Run()
	if err != nil {
		return "", err
	}
	picker, ok := model.(startupProfilePicker)
	if !ok {
		return "", fmt.Errorf("could not choose a profile")
	}
	if picker.selected == "" {
		return "", context.Canceled
	}
	return picker.selected, nil
}

type chatDraftState struct {
	Replies       map[string]native.Message `json:"replies,omitempty"`
	FailedReplies map[string]native.Message `json:"failed_replies,omitempty"`
	Owner         string                    `json:"owner"`
	Drafts        map[string]string         `json:"drafts"`
	Failed        map[string]string         `json:"failed,omitempty"`
	Unread        map[string]int            `json:"unread,omitempty"`
}
