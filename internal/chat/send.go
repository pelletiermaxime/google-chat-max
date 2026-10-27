package chat

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type SendError struct {
	Uncertain bool
	Detail    string
}

func (e *SendError) Error() string { return e.Detail }

func NewRequestID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	b[6] = (b[6] & 15) | 64
	b[8] = (b[8] & 63) | 128
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[:4], b[4:6], b[6:8], b[8:10], b[10:]), nil
}

// Send is called only after an explicit user action. Retries must keep the same
// request ID, destination and text so Google can return the original message.
func (c *Client) Send(ctx context.Context, space, thread, text, requestID string) (Message, error) {
	return c.send(ctx, space, thread, text, requestID, nil)
}

func (c *Client) SendQuoted(ctx context.Context, space, thread, text, requestID string, quote QuotedMessageMetadata) (Message, error) {
	if MessageSpace(quote.Name) != space || quote.LastUpdateTime.IsZero() {
		return Message{}, errors.New("invalid quoted message or missing version timestamp")
	}
	return c.send(ctx, space, thread, text, requestID, &quote)
}

func (c *Client) send(ctx context.Context, space, thread, text, requestID string, quote *QuotedMessageMetadata) (Message, error) {
	if !validSpace(space) || (thread != "" && !ValidThread(space, thread)) || len(requestID) != 36 || strings.TrimSpace(text) == "" {
		return Message{}, errors.New("invalid message destination, text, or request ID")
	}
	body := map[string]any{"text": text}
	if quote != nil {
		body["quotedMessageMetadata"] = map[string]string{"name": quote.Name, "lastUpdateTime": quote.LastUpdateTime.Format(time.RFC3339Nano)}
	}
	query := url.Values{"requestId": {requestID}}
	if thread != "" {
		body["thread"] = map[string]string{"name": thread}
		query.Set("messageReplyOption", "REPLY_MESSAGE_OR_FAIL")
	}
	data, err := json.Marshal(body)
	if err != nil || len(data) > 32000 {
		return Message{}, errors.New("message exceeds Google's 32000-byte limit")
	}
	req, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/"+space+"/messages?"+query.Encode(), bytes.NewReader(data))
	if err != nil {
		return Message{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := c.http.Do(req)
	if err != nil {
		return Message{}, &SendError{Uncertain: true, Detail: "Delivery is uncertain. The draft is kept; Retry safely uses the same request ID."}
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		if quote != nil && res.StatusCode == http.StatusBadRequest {
			return Message{}, &SendError{Detail: "Google could not send this quoted reply. Your draft is kept. Refresh the original message and select Quote reply again."}
		}
		return Message{}, &SendError{Uncertain: res.StatusCode >= 500, Detail: fmt.Sprintf("Google Chat could not confirm delivery (HTTP %d). Your draft is kept.", res.StatusCode)}
	}
	data, err = io.ReadAll(io.LimitReader(res.Body, maxResponseBytes+1))
	var m Message
	if err != nil || len(data) > maxResponseBytes || json.Unmarshal(data, &m) != nil || MessageSpace(m.Name) != space {
		return Message{}, &SendError{Uncertain: true, Detail: "Google accepted the request but returned incomplete delivery information. Retry safely to confirm."}
	}
	// Idempotent repeat responses may echo only part of the original message.
	// Hydrate metadata without turning an already accepted send into a failure.
	if m.Thread.Name == "" || m.CreateTime.IsZero() {
		var full Message
		if c.get(ctx, "/"+m.Name, url.Values{"fields": {messageFields}}, &full) == nil && full.Name == m.Name {
			m = full
		}
	}
	if thread != "" {
		m.ThreadReply = true
		if m.Thread.Name == "" {
			m.Thread.Name = thread
		}
	}
	if m.Text == "" {
		m.Text = text
	}
	if m.QuotedMessageMetadata == nil && quote != nil {
		copy := *quote
		m.QuotedMessageMetadata = &copy
	}
	if m.CreateTime.IsZero() {
		m.CreateTime = time.Now().UTC()
	}
	if m.Sender.Name == "" {
		c.identityMu.Lock()
		m.Sender.Name = c.self
		c.identityMu.Unlock()
	}
	if c.identities {
		users, _ := c.Members(ctx, space)
		for _, u := range users {
			if u.Name == m.Sender.Name {
				m.Sender = m.Sender.WithFallback(u)
				break
			}
		}
	}
	return m, nil
}
