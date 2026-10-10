// Package telegram implements privacy-safe Telegram Bot API requests.
package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/Diaszano/election-bot/internal/application/ports"
	"github.com/Diaszano/election-bot/internal/application/services"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

type Chat struct {
	ID   int64  `json:"id"`
	Type string `json:"type"`
}
type Message struct {
	ID   int64  `json:"message_id"`
	Chat Chat   `json:"chat"`
	Text string `json:"text"`
}
type Update struct {
	ID      int64    `json:"update_id"`
	Message *Message `json:"message"`
}
type BotIdentity struct {
	Username string `json:"username"`
}
type Client struct {
	http         *http.Client
	base, token  string
	interval     time.Duration
	mu           sync.Mutex
	global       time.Time
	chats        map[int64]time.Time
	blockedUntil time.Time
}

func New(c *http.Client, base, token string, interval time.Duration) *Client {
	return &Client{http: c, base: strings.TrimRight(base, "/"), token: token, interval: interval, chats: map[int64]time.Time{}}
}
func (c *Client) wait(ctx context.Context, chat int64, send bool) error {
	c.mu.Lock()
	now := time.Now()
	next := maxTime(now, c.blockedUntil)
	if c.interval > 0 {
		next = maxTime(next, c.global)
		if chat != 0 {
			next = maxTime(next, c.chats[chat])
			step := c.interval
			if send {
				step = max(step, 3*time.Second)
			}
			c.chats[chat] = next.Add(step)
		}
		c.global = next.Add(50 * time.Millisecond)
	}
	c.mu.Unlock()
	timer := time.NewTimer(max(time.Until(next), 0))
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
func maxTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return b
	}
	return a
}
func (c *Client) call(ctx context.Context, method string, chat int64, input any, out any) (err error) {
	operation := map[string]string{"sendMessage": "telegram.send_message", "editMessageText": "telegram.edit_message", "getUpdates": "telegram.get_updates", "getMe": "telegram.get_me"}[method]
	ctx, finish := services.Operation(ctx, operation)
	defer func() { finish(err) }()
	if e := c.wait(ctx, chat, method == "sendMessage"); e != nil {
		return e
	}
	services.Counter(ctx, "telegram_api_requests")
	started := time.Now()
	defer services.Duration(ctx, "telegram_api_request_duration", started)
	body, e := json.Marshal(input)
	if e != nil {
		return &ports.ExternalError{Kind: "payload"}
	}
	req, e := http.NewRequestWithContext(ctx, "POST", c.base+"/bot"+c.token+"/"+method, bytes.NewReader(body))
	if e != nil {
		return &ports.ExternalError{Kind: "payload"}
	}
	req.Header.Set("Content-Type", "application/json")
	res, e := c.http.Do(req)
	if e != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return &ports.ExternalError{Kind: "transient"}
	}
	defer res.Body.Close()
	b, e := io.ReadAll(io.LimitReader(res.Body, 1024*1024+1))
	if e != nil || len(b) > 1024*1024 {
		return &ports.ExternalError{Kind: "transient"}
	}
	var envelope struct {
		OK          bool            `json:"ok"`
		Code        int             `json:"error_code"`
		Description string          `json:"description"`
		Result      json.RawMessage `json:"result"`
		Parameters  struct {
			Retry int `json:"retry_after"`
		} `json:"parameters"`
	}
	if e = json.Unmarshal(b, &envelope); e != nil {
		return &ports.ExternalError{Kind: "transient"}
	}
	if !envelope.OK || res.StatusCode >= 400 {
		code := envelope.Code
		if code == 0 {
			code = res.StatusCode
		}
		kind := "payload"
		desc := strings.ToLower(envelope.Description)
		switch {
		case code == 429:
			kind = "rate_limited"
		case code == 403:
			kind = "blocked"
		case code >= 500:
			kind = "transient"
		case strings.Contains(desc, "message is not modified"):
			kind = "not_modified"
		case strings.Contains(desc, "message to edit not found") || strings.Contains(desc, "message can't be edited"):
			kind = "missing_message"
		case strings.Contains(desc, "chat not found"):
			kind = "invalid_chat"
		}
		wait := time.Duration(max(envelope.Parameters.Retry, 0)) * time.Second
		if kind == "rate_limited" {
			wait = max(wait, time.Second)
			c.mu.Lock()
			c.blockedUntil = maxTime(c.blockedUntil, time.Now().Add(wait))
			c.mu.Unlock()
		}
		return &ports.ExternalError{Kind: kind, RetryAfter: wait}
	}
	if out != nil {
		if e = json.Unmarshal(envelope.Result, out); e != nil {
			return &ports.ExternalError{Kind: "transient"}
		}
	}
	return nil
}
func (c *Client) Send(ctx context.Context, chat int64, text string) (int64, error) {
	var m Message
	e := c.call(ctx, "sendMessage", chat, map[string]any{"chat_id": chat, "text": text, "parse_mode": "HTML"}, &m)
	if e == nil && m.ID == 0 {
		return 0, &ports.ExternalError{Kind: "transient"}
	}
	if e == nil {
		services.Counter(ctx, "telegram_messages_sent")
	}
	return m.ID, e
}
func (c *Client) Edit(ctx context.Context, chat, id int64, text string) error {
	e := c.call(ctx, "editMessageText", chat, map[string]any{"chat_id": chat, "message_id": id, "text": text, "parse_mode": "HTML"}, nil)
	if e == nil {
		services.Counter(ctx, "telegram_messages_edited")
	}
	return e
}
func (c *Client) GetUpdates(ctx context.Context, offset int64, timeout int) ([]Update, error) {
	var u []Update
	e := c.call(ctx, "getUpdates", 0, map[string]any{"offset": offset, "timeout": timeout, "allowed_updates": []string{"message"}}, &u)
	return u, e
}
func (c *Client) GetMe(ctx context.Context) (BotIdentity, error) {
	var b BotIdentity
	e := c.call(ctx, "getMe", 0, map[string]any{}, &b)
	return b, e
}
