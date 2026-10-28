package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/immortal-autumn/WxChatAssistant/internal/native"
)

func journalTestStore(t *testing.T) nativeStore {
	t.Helper()
	s, err := openNativeStore(Store{t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func journalTestWrite(t *testing.T, s nativeStore, owner string, messages ...native.Message) {
	t.Helper()
	if err := s.journal(nativeJournal{Owner: owner, Messages: messages}); err != nil {
		t.Fatal(err)
	}
}

func TestTUIWatchesExternalSendWithoutServerEcho(t *testing.T) {
	for _, mode := range []string{"online", "sync-in-flight", "sync-paused", "offline"} {
		t.Run(mode, func(t *testing.T) {
			s := journalTestStore(t)
			m := testChatModel()
			journalTestWrite(t, s, m.owner, m.messages...)
			var err error
			m.journal, err = newChatJournalWatcher(s, m.owner)
			if err != nil {
				t.Fatal(err)
			}
			m.syncing = mode == "sync-in-flight"
			m.paused = mode == "sync-paused"
			m.offline = mode == "offline"
			if m.paused {
				m.syncErr = "server is unavailable"
			}
			m.composer.SetValue("Unsent draft for Alice")
			outgoing := native.Message{ID: "external-send", From: "me", To: "bob", Peer: "bob", Type: 1, Text: "Sent by another process", Timestamp: 200, Outgoing: true}
			// This is the same journal publication used after CLI send acceptance.
			journalTestWrite(t, s, m.owner, outgoing)
			next, cmd := m.update(chatJournalTick{})
			m = next.(chatModel)
			if cmd == nil {
				t.Fatal("local journal poll was not scheduled")
			}
			next, _ = m.update(cmd())
			m = next.(chatModel)
			if !m.seen[outgoing.ID] || m.latest["bob"].ID != outgoing.ID {
				t.Fatal("external accepted send did not update the conversation")
			}
			if m.selected != "alice" || m.composer.Value() != "Unsent draft for Alice" || m.unread["bob"] != 0 {
				t.Fatal("external send changed the draft/recipient or added an unread badge")
			}
			if m.syncing != (mode == "sync-in-flight") || m.paused != (mode == "sync-paused") || !m.lastSync.IsZero() || (m.paused && m.syncErr != "server is unavailable") {
				t.Fatal("local refresh changed network connection state")
			}
			m.selectPeer("bob")
			if !strings.Contains(ansi.Strip(m.viewport.View()), outgoing.Text) {
				t.Fatal("external send was not visible in the transcript")
			}
			if result := m.journal.read(m.ctx); result.Err != nil || len(result.Messages) != 0 {
				t.Fatal("unchanged journal was replayed", result.Err)
			}
			outgoing.Sequence = 42
			next, _ = m.update(chatSyncResult{Messages: []native.Message{outgoing}})
			m = next.(chatModel)
			count := 0
			for _, message := range m.messages {
				if message.ID == outgoing.ID {
					count++
					if message.Sequence != 42 {
						t.Fatal("server echo did not enrich the local send")
					}
				}
			}
			if count != 1 {
				t.Fatalf("server echo duplicated the external send: count=%d", count)
			}
		})
	}
}

func TestJournalWatcherBaselineAndAccountIsolation(t *testing.T) {
	s := journalTestStore(t)
	old := native.Message{ID: "old", From: "alice", Peer: "alice", Type: 1, Text: "Existing history"}
	journalTestWrite(t, s, "me", old)
	w, err := newChatJournalWatcher(s, "me")
	if err != nil {
		t.Fatal(err)
	}
	if result := w.read(context.Background()); result.Err != nil || len(result.Messages) != 0 {
		t.Fatal("baseline history replayed", result.Err)
	}
	journalTestWrite(t, s, "other-owner", native.Message{ID: "private-other-account", Peer: "other"})
	// Published between baseline capture and loading the startup snapshot.
	newMessage := native.Message{ID: "new", From: "bob", Peer: "bob", Type: 1, Text: "Startup race", Timestamp: 100}
	journalTestWrite(t, s, "me", newMessage)
	data, err := s.chatSnapshot("me")
	if err != nil {
		t.Fatal(err)
	}
	m := newChatModel(context.Background(), &fakeChatBackend{}, "me", native.DesktopProfile, data, true)
	result := w.read(m.ctx)
	if result.Err != nil || len(result.Messages) != 1 || result.Messages[0].ID != "new" {
		t.Fatal("new records were lost or mixed with another account", result.Err)
	}
	next, _ := m.update(result)
	m = next.(chatModel)
	if len(m.messages) != 2 || m.totalUnread() != 0 {
		t.Fatal("startup race duplicated history or counted existing messages as unread")
	}
}

func TestJournalWatcherRetriesBadFilesWithoutHidingNewSends(t *testing.T) {
	s := journalTestStore(t)
	w, err := newChatJournalWatcher(s, "me")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(s.dir, "batches"), 0700); err != nil {
		t.Fatal(err)
	}
	badPath := filepath.Join("batches", "000-bad.json")
	if err := os.WriteFile(filepath.Join(s.dir, badPath), []byte("{"), 0600); err != nil {
		t.Fatal(err)
	}
	journalTestWrite(t, s, "me", native.Message{ID: "valid", Peer: "alice", Type: 1, Outgoing: true})
	result := w.read(context.Background())
	if result.Err == nil || len(result.Messages) != 1 || result.Messages[0].ID != "valid" {
		t.Fatal("bad file hid a valid send or was silently ignored")
	}
	if err := s.save(badPath, nativeJournal{Owner: "me", Messages: []native.Message{{ID: "recovered", Peer: "alice", Type: 1}}}); err != nil {
		t.Fatal(err)
	}
	result = w.read(context.Background())
	if result.Err != nil || len(result.Messages) != 1 || result.Messages[0].ID != "recovered" {
		t.Fatal("failed file was not retried or valid record was replayed", result.Err)
	}
}

func TestJournalWatcherReadsWhileNetworkHoldsAccountLock(t *testing.T) {
	s := journalTestStore(t)
	w, err := newChatJournalWatcher(s, "me")
	if err != nil {
		t.Fatal(err)
	}
	journalTestWrite(t, s, "me", native.Message{ID: "accepted", Peer: "alice", Type: 1, Outgoing: true})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	lock, err := s.lock(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if result := w.read(ctx); result.Err != nil || len(result.Messages) != 1 {
		t.Fatal("archive refresh waited for the account lock", result.Err)
	}
	cancel()
	if result := w.read(ctx); !errors.Is(result.Err, context.Canceled) {
		t.Fatal("journal reader ignored cancellation")
	}
}

func TestTUIJournalPreservesScrollbackAndCountsUnreadOnce(t *testing.T) {
	m := refreshTestModel()
	m.viewport.SetYOffset(m.messageStarts["alice-40"])
	m.composer.SetValue("Keep my draft")
	message := native.Message{ID: "backfill", From: "alice", Peer: "alice", Type: 1, Text: "Earlier history", Timestamp: 50}
	next, _ := m.update(chatJournalResult{Messages: []native.Message{message}})
	m = next.(chatModel)
	if m.historyHits[m.viewport.YOffset].MessageID != "alice-40" || m.composer.Value() != "Keep my draft" {
		t.Fatal("journal refresh moved scrollback or changed the draft")
	}
	if m.unread["alice"] != 1 {
		t.Fatal("new incoming journal entry was not counted")
	}
	next, _ = m.update(chatSyncResult{Messages: []native.Message{message}})
	m = next.(chatModel)
	if m.unread["alice"] != 1 {
		t.Fatal("server replay double-counted the journal message")
	}
}
