package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestMessageProtocol(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != "POST" || r.Header.Get("Authorization") != "Bearer secret" || r.Header.Get("AuthorizationType") != "ilink_bot_token" {
			t.Errorf("incorrect auth or method")
		}
		uin, err := base64.StdEncoding.DecodeString(r.Header.Get("X-WECHAT-UIN"))
		if err != nil {
			t.Error(err)
		}
		if _, err := strconv.ParseUint(string(uin), 10, 32); err != nil {
			t.Error(err)
		}
		if r.Header.Get("iLink-App-ClientVersion") != "132106" || r.Header.Get("iLink-App-Id") != "bot" {
			t.Error("incorrect app metadata")
		}
		var body map[string]json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		var info map[string]string
		json.Unmarshal(body["base_info"], &info)
		if info["channel_version"] != protocolVersion || info["bot_agent"] != "WeChatCLI/0.1.0" {
			t.Error("incorrect base_info")
		}
		switch r.URL.Path {
		case "/ilink/bot/getupdates":
			if string(body["get_updates_buf"]) != `"cursor-1"` {
				t.Error("missing cursor")
			}
			fmt.Fprint(w, `{"ret":0,"get_updates_buf":"cursor-2","msgs":[{"message_id":9007199254740993,"from_user_id":"user","message_type":1,"context_token":"context","item_list":[{"type":1,"text_item":{"text":"你好"}}]}]}`)
		case "/ilink/bot/sendmessage":
			var msg map[string]any
			json.Unmarshal(body["msg"], &msg)
			if msg["to_user_id"] != "user" || msg["context_token"] != "context" || msg["message_type"] != float64(2) || msg["message_state"] != float64(2) || msg["client_id"] == "" {
				t.Error("incorrect outgoing envelope")
			}
			items := msg["item_list"].([]any)
			if items[0].(map[string]any)["text_item"].(map[string]any)["text"] != "hello" {
				t.Error("text lost")
			}
			fmt.Fprint(w, `{"ret":0}`)
		default:
			t.Errorf("unexpected endpoint %s", r.URL.Path)
		}
	}))
	defer server.Close()
	client := newClient(server.URL, "secret")
	updates, err := client.updates(context.Background(), "cursor-1")
	if err != nil {
		t.Fatal(err)
	}
	if updates.Cursor != "cursor-2" || !strings.Contains(string(updates.Messages[0]), "9007199254740993") {
		t.Fatal("cursor or integer precision lost")
	}
	var msg Message
	json.Unmarshal(updates.Messages[0], &msg)
	if msg.text() != "你好" {
		t.Fatal("text mismatch")
	}
	if _, err := client.send(context.Background(), msg.From, "hello", msg.Context); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatal(calls)
	}
}

func TestLoginRefreshAndVerification(t *testing.T) {
	qrCount, polls, shown, verified := 0, 0, 0, 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Error("credentials leaked to login")
		}
		switch r.URL.Path {
		case "/ilink/bot/get_bot_qrcode":
			qrCount++
			if r.Method != "POST" || r.URL.Query().Get("bot_type") != "3" {
				t.Error("incorrect QR request")
			}
			var body map[string]json.RawMessage
			json.NewDecoder(r.Body).Decode(&body)
			if body["base_info"] != nil || string(body["local_token_list"]) != `[]` {
				t.Error("incorrect QR body")
			}
			fmt.Fprintf(w, `{"qrcode":"qr%d","qrcode_img_content":"https://example.com/pair"}`, qrCount)
		case "/ilink/bot/get_qrcode_status":
			polls++
			if r.Method != "GET" || r.Header.Get("AuthorizationType") != "" || r.Header.Get("X-WECHAT-UIN") != "" {
				t.Error("incorrect QR status headers")
			}
			switch polls {
			case 1:
				fmt.Fprint(w, `{"status":"expired"}`)
			case 2:
				if r.URL.Query().Get("qrcode") != "qr2" {
					t.Error("QR not refreshed")
				}
				fmt.Fprint(w, `{"status":"need_verifycode"}`)
			case 3:
				if r.URL.Query().Get("verify_code") != "123456" {
					t.Error("verification missing")
				}
				fmt.Fprint(w, `{"status":"confirmed","bot_token":"secret","ilink_bot_id":"bot","baseurl":"https://ilinkai.weixin.qq.com"}`)
			default:
				t.Error("extra poll")
			}
		}
	}))
	defer server.Close()
	status, err := performLogin(context.Background(), newClient(server.URL, ""), []string{}, func(q QRCode) error { shown++; return nil }, func(context.Context) (string, error) { verified++; return "123456", nil })
	if err != nil {
		t.Fatal(err)
	}
	if status.Token != "secret" || shown != 2 || verified != 1 {
		t.Fatal("login flow incomplete")
	}
}

func TestErrorsAndCancellation(t *testing.T) {
	for _, response := range []string{`{"ret":-14}`, `{"errcode":-14}`} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, response) }))
		_, err := newClient(server.URL, "secret").updates(context.Background(), "")
		var apiErr *APIError
		if !errors.As(err, &apiErr) || apiErr.Code != -14 {
			t.Errorf("unexpected error %v", err)
		}
		server.Close()
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err := newClient(server.URL, "").loginStatus(ctx, "secret-qr", "")
	if !errors.Is(err, context.DeadlineExceeded) || strings.Contains(err.Error(), "secret-qr") {
		t.Errorf("cancellation or redaction failed: %v", err)
	}
}

func TestTrustedBase(t *testing.T) {
	for _, u := range []string{"http://ilinkai.weixin.qq.com", "https://weixin.qq.com.evil.test", "https://evil.test", "https://user:pass@ilinkai.weixin.qq.com", "https://ilinkai.weixin.qq.com/path", "https://ilinkai.weixin.qq.com?token=x"} {
		if _, err := trustedBase(u); err == nil {
			t.Errorf("accepted %s", u)
		}
	}
	if _, err := trustedBase(defaultBaseURL); err != nil {
		t.Fatal(err)
	}
}

func TestStoreConcurrentUpdatesAndReaderLock(t *testing.T) {
	store := Store{filepath.Join(t.TempDir(), "private")}
	if err := store.update(func(s *State) error { s.Token = "secret"; return nil }); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if err := store.update(func(s *State) error { s.Contexts[strconv.Itoa(i)] = "context"; return nil }); err != nil {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()
	state, err := store.load()
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Contexts) != 20 || state.Token != "secret" {
		t.Fatal("lost concurrent updates")
	}
	stat, err := os.Stat(filepath.Join(store.Dir, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	if stat.Mode().Perm() != 0600 {
		t.Fatalf("credentials permissions: %o", stat.Mode().Perm())
	}
	lock, err := store.readerLock()
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if second, err := store.readerLock(); err == nil {
		second.Close()
		t.Fatal("multiple readers allowed")
	}
}
