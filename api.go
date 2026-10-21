package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const defaultBaseURL = "https://ilinkai.weixin.qq.com"
const protocolVersion = "2.4.10"

// Client speaks the iLink bot protocol, independently of OpenClaw.
type Client struct {
	BaseURL, Token string
	HTTP           *http.Client
}

func newClient(base, token string) *Client {
	return &Client{base, token, &http.Client{Timeout: 40 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}

type APIError struct{ Code int }

func (e *APIError) Error() string {
	if e.Code == -14 {
		return "WeChat session expired (-14); run wechat login again"
	}
	return fmt.Sprintf("WeChat API error %d", e.Code)
}

func (c *Client) request(ctx context.Context, method, endpoint string, body map[string]any, auth bool, out any) error {
	var reader io.Reader
	if body != nil {
		if auth {
			body["base_info"] = map[string]string{"channel_version": protocolVersion, "bot_agent": "WeChatCLI/0.1.0"}
		}
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(c.BaseURL, "/")+"/ilink/bot/"+endpoint, reader)
	if err != nil {
		return errors.New("invalid API URL")
	}
	req.Header.Set("iLink-App-Id", "bot")
	req.Header.Set("iLink-App-ClientVersion", strconv.Itoa(2<<16|4<<8|10))
	if method == http.MethodPost {
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("AuthorizationType", "ilink_bot_token")
		var b [4]byte
		if _, err := rand.Read(b[:]); err != nil {
			return err
		}
		req.Header.Set("X-WECHAT-UIN", base64.StdEncoding.EncodeToString([]byte(strconv.FormatUint(uint64(binary.BigEndian.Uint32(b[:])), 10))))
	}
	if auth {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		// url.Error includes login secrets in its URL; don't expose it.
		var ue *url.Error
		if errors.As(err, &ue) {
			return fmt.Errorf("API transport: %w", ue.Err)
		}
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("API HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return err
	}
	var status struct {
		Ret     int `json:"ret"`
		Errcode int `json:"errcode"`
	}
	if err := json.Unmarshal(data, &status); err != nil {
		return errors.New("API returned invalid JSON")
	}
	if status.Ret != 0 {
		return &APIError{status.Ret}
	}
	if status.Errcode != 0 {
		return &APIError{status.Errcode}
	}
	if out != nil {
		if err := json.Unmarshal(data, out); err != nil {
			return errors.New("unexpected API response format")
		}
	}
	return nil
}

type QRCode struct {
	Code    string `json:"qrcode"`
	Content string `json:"qrcode_img_content"`
}
type LoginStatus struct {
	Status       string `json:"status"`
	Token        string `json:"bot_token"`
	BotID        string `json:"ilink_bot_id"`
	UserID       string `json:"ilink_user_id"`
	BaseURL      string `json:"baseurl"`
	RedirectHost string `json:"redirect_host"`
}

func (c *Client) qr(ctx context.Context, tokens []string) (QRCode, error) {
	var result QRCode
	err := c.request(ctx, "POST", "get_bot_qrcode?bot_type=3", map[string]any{"local_token_list": tokens}, false, &result)
	if err == nil && (result.Code == "" || result.Content == "") {
		err = errors.New("QR response missing code or content")
	}
	return result, err
}
func (c *Client) loginStatus(ctx context.Context, code, verify string) (LoginStatus, error) {
	q := url.Values{"qrcode": {code}}
	if verify != "" {
		q.Set("verify_code", verify)
	}
	var result LoginStatus
	err := c.request(ctx, "GET", "get_qrcode_status?"+q.Encode(), nil, false, &result)
	return result, err
}

type Message struct {
	From    string `json:"from_user_id"`
	Context string `json:"context_token"`
	Type    int    `json:"message_type"`
	Items   []struct {
		Type int `json:"type"`
		Text *struct {
			Text string `json:"text"`
		} `json:"text_item"`
		Voice *struct {
			Text string `json:"text"`
		} `json:"voice_item"`
	} `json:"item_list"`
}

func (m Message) text() string {
	var parts []string
	for _, item := range m.Items {
		switch {
		case item.Text != nil:
			parts = append(parts, item.Text.Text)
		case item.Voice != nil && item.Voice.Text != "":
			parts = append(parts, item.Voice.Text)
		default:
			parts = append(parts, fmt.Sprintf("[attachment type=%d]", item.Type))
		}
	}
	return strings.Join(parts, "\n")
}

type Updates struct {
	Messages  []json.RawMessage `json:"msgs"`
	Cursor    string            `json:"get_updates_buf"`
	TimeoutMS int               `json:"longpolling_timeout_ms"`
}

func (c *Client) updates(ctx context.Context, cursor string) (Updates, error) {
	var result Updates
	err := c.request(ctx, "POST", "getupdates", map[string]any{"get_updates_buf": cursor}, true, &result)
	return result, err
}
func (c *Client) send(ctx context.Context, to, text, token string) (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	id := "wechat-cli-" + hex.EncodeToString(b[:])
	msg := map[string]any{"from_user_id": "", "to_user_id": to, "client_id": id, "message_type": 2, "message_state": 2, "context_token": token, "item_list": []any{map[string]any{"type": 1, "text_item": map[string]string{"text": text}}}}
	return id, c.request(ctx, "POST", "sendmessage", map[string]any{"msg": msg}, true, nil)
}

// Only Tencent HTTPS hosts may receive credentials returned by login.
func trustedBase(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Port() != "" || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return "", errors.New("invalid Tencent API URL")
	}
	host := strings.ToLower(u.Hostname())
	if host != "ilinkai.weixin.qq.com" && !strings.HasSuffix(host, ".weixin.qq.com") {
		return "", errors.New("API host is outside weixin.qq.com")
	}
	return strings.TrimRight(u.String(), "/"), nil
}
func isTimeout(err error) bool { var ne net.Error; return errors.As(err, &ne) && ne.Timeout() }
func pause(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
