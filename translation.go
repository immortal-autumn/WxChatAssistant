package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/kevmo314/wechat-cli/internal/native"
)

type chatTranslationResult struct {
	Key, Text string
	Err       error
}
type chatTranslationCache struct {
	Owner string            `json:"owner"`
	Items map[string]string `json:"items"`
}
type translationBackend interface {
	translate(context.Context, native.Message, string) chatTranslationResult
}

func translationText(m native.Message) (string, error) {
	if m.Deleted {
		return "", errors.New("deleted messages cannot be translated")
	}
	if m.Type == 1 && strings.TrimSpace(m.Text) != "" {
		return m.Text, nil
	}
	if m.Type == 49 {
		if title := xmlText(m.Text, "title"); title != "" {
			return title, nil
		}
	}
	return "", errors.New("select a text message or shared-item title to translate")
}
func (m chatModel) translationKey(message native.Message) string {
	return message.Peer + "/" + message.ID + "/" + m.language
}
func (c *chatService) translate(ctx context.Context, message native.Message, lang string) chatTranslationResult {
	result := chatTranslationResult{Key: message.Peer + "/" + message.ID + "/" + lang}
	text, err := translationText(message)
	if err != nil {
		result.Err = err
		return result
	}
	lock, err := c.store.lock(ctx)
	if err != nil {
		result.Err = err
		return result
	}
	defer lock.Close()
	var cache chatTranslationCache
	if err = c.store.load("translations.json", &cache); err != nil && !errors.Is(err, os.ErrNotExist) {
		result.Err = err
		return result
	}
	if cache.Owner != c.owner || cache.Items == nil {
		cache = chatTranslationCache{Owner: c.owner, Items: map[string]string{}}
	}
	digest := sha256.Sum256([]byte(text + "/" + lang))
	cacheKey := fmt.Sprintf("%x", digest)
	if cached := cache.Items[cacheKey]; cached != "" {
		result.Text = cached
		return result
	}
	state, err := c.store.state()
	if err != nil {
		result.Err = err
		return result
	}
	if state.Account == nil || state.Account.Username != c.owner {
		result.Err = errors.New("account changed")
		return result
	}
	session, err := native.Connect(ctx, state.Account.Host)
	if err != nil {
		result.Err = err
		return result
	}
	defer session.Close()
	result.Text, result.Err = session.Translate(ctx, state.Account, text, lang)
	if result.Err != nil {
		return result
	}
	cache.Items[cacheKey] = result.Text
	if err = c.store.save("translations.json", cache); err != nil {
		result.Err = err
		return result
	}
	result.Err = c.store.save("state.json", state)
	return result
}
func (m *chatModel) translateSelected() tea.Cmd {
	message, ok := m.selectedMessage()
	if !ok {
		return nil
	}
	key := m.translationKey(message)
	if m.translations[key] != "" {
		delete(m.translations, key)
		m.refreshHistory(m.viewport.AtBottom())
		return nil
	}
	if m.offline {
		m.notice = "Translation requires a connection to WeChat"
		return nil
	}
	if m.translationBusy[key] {
		return nil
	}
	backend, ok := m.backend.(translationBackend)
	if !ok {
		m.notice = "Translation is unavailable"
		return nil
	}
	if _, err := translationText(message); err != nil {
		m.notice = err.Error()
		return nil
	}
	m.translationBusy[key] = true
	m.notice = "Translating with WeChat…"
	lang := m.language
	return func() tea.Msg { return backend.translate(m.ctx, message, lang) }
}
func translateCommand(ctx context.Context, store Store, args []string) error {
	f := flags("translate")
	text := f.String("text", "", "text to translate with WeChat")
	id := f.String("id", "", "translate this archived message instead")
	lang := f.String("lang", "en", "target language, for example en or zh_CN")
	asJSON := f.Bool("json", false, "emit a JSON result")
	if err := parse(f, args); err != nil {
		return err
	}
	if !native.ValidTranslationLanguage(*lang) {
		return errors.New("invalid translation language")
	}
	if (*text == "") == (*id == "") {
		return errors.New("provide exactly one of --text or --id")
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
		return errors.New("not logged in")
	}
	message := native.Message{Type: 1, Text: *text, Peer: state.Account.Username, ID: "translation"}
	if *id != "" {
		history, err := s.history(state.Account.Username)
		if err != nil {
			return err
		}
		found := false
		for _, v := range history {
			if v.ID == *id {
				message = v
				found = true
				break
			}
		}
		if !found {
			return errors.New("message is not in the local archive")
		}
	}
	result := (&chatService{store: s, owner: state.Account.Username}).translate(ctx, message, *lang)
	if result.Err != nil {
		return result.Err
	}
	if *asJSON {
		return json.NewEncoder(os.Stdout).Encode(map[string]string{"language": *lang, "translation": result.Text})
	}
	fmt.Println(result.Text)
	return nil
}
