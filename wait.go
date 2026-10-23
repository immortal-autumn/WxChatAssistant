package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/gofrs/flock"
	"github.com/kevmo314/wechat-cli/internal/native"
)

func waitMatches(m native.Message, peer string, since uint32) bool {
	return !m.Outgoing && visibleChatMessage(m) && m.Type != 10000 && m.Type != 10002 && m.Timestamp >= since && (peer == "" || peer == m.Peer || peer == m.From)
}
func waitMessage(ctx context.Context, store Store, args []string) error {
	f := flags("wait")
	timeout := f.Duration("timeout", 0, "maximum wait (0 waits until interrupted)")
	asJSON := f.Bool("json", false, "emit the next incoming message as JSON")
	from := f.String("from", "", "optional sender or conversation ID")
	if err := parse(f, args); err != nil {
		return err
	}
	if *timeout < 0 {
		return errors.New("timeout cannot be negative")
	}
	if *timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, *timeout)
		defer cancel()
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
		return errors.New("not logged in; run wechat login first")
	}
	since := uint32(time.Now().Unix())
	owner := state.Account.Username
	seenMessages := map[string]bool{}
	history, err := s.history(owner)
	if err != nil {
		return err
	}
	for _, m := range history {
		seenMessages[m.ID] = true
	}
	seenFiles := map[string]bool{}
	initial, err := filepath.Glob(filepath.Join(s.dir, "batches", "*.json"))
	if err != nil {
		return err
	}
	for _, p := range initial {
		seenFiles[p] = true
	}
	service := &chatService{store: s, owner: owner}
	defer service.close()
	if err = store.init(); err != nil {
		return err
	}
	for {
		if err = ctx.Err(); err != nil {
			return err
		}
		current, err := s.state()
		if err != nil {
			return err
		}
		if current.Account == nil || current.Account.Username != owner {
			return errors.New("account changed while waiting")
		}
		files, err := filepath.Glob(filepath.Join(s.dir, "batches", "*.json"))
		if err != nil {
			return err
		}
		sort.Strings(files)
		for _, path := range files {
			if seenFiles[path] {
				continue
			}
			var record nativeJournal
			if err = s.load(filepath.Join("batches", filepath.Base(path)), &record); err != nil {
				return err
			}
			seenFiles[path] = true
			if record.Owner != owner {
				continue
			}
			for _, m := range record.Messages {
				if seenMessages[m.ID] {
					continue
				}
				seenMessages[m.ID] = true
				if !waitMatches(m, *from, since) {
					continue
				}
				if *asJSON {
					return json.NewEncoder(os.Stdout).Encode(m)
				}
				_, err = fmt.Fprintf(os.Stdout, "%s %s: %s\n", time.Unix(int64(m.Timestamp), 0).Format(time.RFC3339), oneLine(m.From), chatSafeText(messagePreview(m)))
				return err
			}
		}
		// A TUI/reader may already own sync. Watch its atomic journal writes instead
		// of competing for a second cursor. Otherwise sync one page under the lock.
		pause := 300 * time.Millisecond
		reader := flock.New(filepath.Join(store.Dir, "reader.lock"))
		locked, lockErr := reader.TryLock()
		if lockErr != nil {
			reader.Close()
			return lockErr
		}
		if locked {
			pause = time.Second
			result := service.sync(ctx)
			reader.Close()
			if result.Err != nil {
				return result.Err
			}
			if result.More || len(result.Messages) > 0 {
				continue
			}
		} else {
			reader.Close()
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(pause):
		}
	}
}
