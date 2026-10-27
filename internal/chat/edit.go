package chat

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// Edit updates only the text of an existing message after an explicit user
// action. Never allow a missing message to be recreated by this request.
func (c *Client) Edit(ctx context.Context, name, text string) (Message, error) {
	if MessageSpace(name) == "" || strings.TrimSpace(text) == "" {
		return Message{}, errors.New("invalid message or empty edit")
	}
	data, err := json.Marshal(map[string]string{"text": text})
	if err != nil || len(data) > 32000 {
		return Message{}, errors.New("message exceeds Google's 32000-byte limit")
	}
	query := url.Values{"updateMask": {"text"}, "allowMissing": {"false"}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPatch, c.baseURL+"/"+name+"?"+query.Encode(), bytes.NewReader(data))
	if err != nil {
		return Message{}, errors.New("could not prepare the message edit")
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := c.http.Do(req)
	if err != nil {
		return Message{}, errors.New("Google Chat could not confirm the edit. Your changes are kept; refresh the message or retry saving")
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		switch res.StatusCode {
		case http.StatusUnauthorized, http.StatusForbidden:
			return Message{}, errors.New("Google Chat denied the edit. You can edit only your own messages and your gws login needs the chat.messages permission. Your changes are kept")
		case http.StatusNotFound:
			return Message{}, errors.New("this message is no longer available. Your changes are kept")
		default:
			return Message{}, fmt.Errorf("Google Chat could not confirm the edit (HTTP %d). Your changes are kept; refresh the message or retry saving", res.StatusCode)
		}
	}
	data, err = io.ReadAll(io.LimitReader(res.Body, maxResponseBytes+1))
	var m Message
	if err != nil || len(data) > maxResponseBytes || json.Unmarshal(data, &m) != nil || m.Name != name || m.Text == "" || m.LastUpdateTime.IsZero() {
		return Message{}, errors.New("Google accepted the edit but returned incomplete information. Your changes are kept; refresh the message or retry saving")
	}
	return m, nil
}
