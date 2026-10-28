package main

import (
	"context"
	"path/filepath"
	"sort"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/immortal-autumn/WxChatAssistant/internal/native"
)

type chatJournalTick struct{}
type chatJournalResult struct {
	Messages []native.Message
	Contacts map[string]native.Contact
	Err      error
}

// Journals are immutable and published by atomic rename. Read new files without
// waiting for the network reader or acquiring its account lock.
type chatJournalWatcher struct {
	store nativeStore
	owner string
	seen  map[string]bool
}

// Capture the baseline before loading the initial snapshot: a file published
// between these steps is either already in the snapshot or read by the watcher.
func newChatJournalWatcher(store nativeStore, owner string) (*chatJournalWatcher, error) {
	files, err := filepath.Glob(filepath.Join(store.dir, "batches", "*.json"))
	if err != nil {
		return nil, err
	}
	w := &chatJournalWatcher{store: store, owner: owner, seen: map[string]bool{}}
	for _, path := range files {
		w.seen[path] = true
	}
	return w, nil
}

// read is called by one command at a time, with the next poll scheduled only
// after its result is processed by the model.
func (w *chatJournalWatcher) read(ctx context.Context) chatJournalResult {
	result := chatJournalResult{Contacts: map[string]native.Contact{}}
	files, err := filepath.Glob(filepath.Join(w.store.dir, "batches", "*.json"))
	if err != nil {
		result.Err = err
		return result
	}
	sort.Strings(files)
	for _, path := range files {
		if err := ctx.Err(); err != nil {
			result.Err = err
			return result
		}
		if w.seen[path] {
			continue
		}
		var record nativeJournal
		if err := w.store.load(filepath.Join("batches", filepath.Base(path)), &record); err != nil {
			if result.Err == nil {
				result.Err = err
			}
			continue // Retry this file without hiding other newly committed records.
		}
		w.seen[path] = true
		if record.Owner != w.owner {
			continue
		}
		result.Messages = append(result.Messages, record.orderedMessages()...)
		mergeContacts(result.Contacts, record.Batch.Commands)
	}
	return result
}

func (m chatModel) journalCommand() tea.Cmd {
	if m.journal == nil {
		return nil
	}
	return func() tea.Msg { return m.journal.read(m.ctx) }
}

func (m chatModel) journalWait() tea.Cmd {
	if m.journal == nil {
		return nil
	}
	return tea.Tick(time.Second, func(time.Time) tea.Msg { return chatJournalTick{} })
}

func (m *chatModel) mergeChatUpdate(messages []native.Message, contacts map[string]native.Contact) {
	bottom := m.viewport.AtBottom()
	for peer, c := range contacts {
		previous := m.contacts[peer]
		if len(c.Members) == 0 {
			c.Members = previous.Members
		}
		c.Flags = (previous.Flags &^ c.FlagMask) | (c.Flags & c.FlagMask)
		m.contacts[peer] = c
	}
	m.addMessages(messages, true)
	m.rebuildRows()
	m.refreshHistory(bottom)
}
