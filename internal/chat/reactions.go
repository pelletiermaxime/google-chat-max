package chat

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

type CustomEmoji struct {
	Name              string `json:"name"`
	UID               string `json:"uid"`
	EmojiName         string `json:"emojiName"`
	TemporaryImageURI string `json:"temporaryImageUri"`
}
type Emoji struct {
	Unicode     string       `json:"unicode,omitempty"`
	CustomEmoji *CustomEmoji `json:"customEmoji,omitempty"`
}

func (e Emoji) Key() string {
	if e.CustomEmoji != nil {
		id := e.CustomEmoji.UID
		if id == "" {
			id = strings.TrimPrefix(e.CustomEmoji.Name, "customEmojis/")
		}
		return "custom:" + id
	}
	return "unicode:" + e.Unicode
}
func (e Emoji) Label() string {
	if e.CustomEmoji != nil {
		if e.CustomEmoji.EmojiName != "" {
			return e.CustomEmoji.EmojiName
		}
		return "Custom emoji"
	}
	return e.Unicode
}
func (e Emoji) valid() bool {
	if e.CustomEmoji != nil {
		name := e.CustomEmoji.Name
		return e.Unicode == "" && strings.HasPrefix(name, "customEmojis/") && strings.Count(name, "/") == 1 && len(name) > 13 && len(name) < 256 && !strings.ContainsAny(name, "?#%\\\r\n\t ")
	}
	return e.Unicode != "" && len(e.Unicode) <= 64
}

type ReactionSummary struct {
	Emoji Emoji `json:"emoji"`
	Count int   `json:"reactionCount"`
}
type Reaction struct {
	Name  string `json:"name"`
	Emoji Emoji  `json:"emoji"`
	User  User   `json:"user"`
}
type Reactions struct {
	Summaries []ReactionSummary
	Mine      []Reaction
	All       []Reaction
}

func (r Reactions) Has(emoji Emoji) bool {
	for _, reaction := range r.Mine {
		if reaction.Emoji.Key() == emoji.Key() {
			return true
		}
	}
	return false
}

func (c *Client) CustomEmojis(ctx context.Context) ([]CustomEmoji, error) {
	query := url.Values{"pageSize": {"200"}, "fields": {"customEmojis(name,uid,emojiName,temporaryImageUri),nextPageToken"}}
	var emojis []CustomEmoji
	for range 5 {
		var page struct {
			Emojis []CustomEmoji `json:"customEmojis"`
			Next   string        `json:"nextPageToken"`
		}
		if err := c.get(ctx, "/customEmojis", query, &page); err != nil {
			return nil, err
		}
		emojis = append(emojis, page.Emojis...)
		if len(emojis) > 1000 {
			return nil, errors.New("custom emoji list exceeds the 1000 emoji limit")
		}
		if page.Next == "" {
			return emojis, nil
		}
		query.Set("pageToken", page.Next)
	}
	return nil, errors.New("custom emoji list exceeds the five-page limit")
}

func ReactionMessage(name string) string {
	parts := strings.Split(name, "/")
	if len(parts) != 6 || parts[4] != "reactions" || parts[5] == "" || parts[5] == "." || parts[5] == ".." || strings.ContainsAny(name, "?#%\\\r\n\t ") {
		return ""
	}
	message := strings.Join(parts[:4], "/")
	if MessageSpace(message) == "" {
		return ""
	}
	return message
}

func (c *Client) myReactions(ctx context.Context, message string) ([]Reaction, error) {
	if MessageSpace(message) == "" {
		return nil, errors.New("invalid reaction message")
	}
	self, err := c.ResolveIdentity(ctx)
	if err != nil {
		return nil, err
	}
	return c.listReactions(ctx, message, self)
}

func (c *Client) listReactions(ctx context.Context, message, user string) ([]Reaction, error) {
	// The API returns HTTP 500 for users/me in this filter. Use the resolved ID.
	query := url.Values{"pageSize": {"200"}, "fields": {"reactions(name,emoji,user(name,displayName)),nextPageToken"}}
	pages, limit := 5, 1000
	if user != "" {
		query.Set("filter", "user.name = "+strconv.Quote(user))
		pages, limit = 2, 400
	}
	var reactions []Reaction
	for range pages {
		var page struct {
			Reactions []Reaction `json:"reactions"`
			Next      string     `json:"nextPageToken"`
		}
		if err := c.get(ctx, "/"+message+"/reactions", query, &page); err != nil {
			return nil, err
		}
		for _, r := range page.Reactions {
			if (user != "" && r.User.Name != user) || ReactionMessage(r.Name) != message {
				return nil, errors.New("reaction ownership could not be verified")
			}
			reactions = append(reactions, r)
		}
		if len(reactions) > limit {
			return nil, errors.New("reaction list exceeds its limit")
		}
		if page.Next == "" {
			return reactions, nil
		}
		query.Set("pageToken", page.Next)
	}
	return nil, errors.New("reaction list exceeds its page limit")
}

func (c *Client) Reactions(ctx context.Context, message string) (Reactions, error) {
	if MessageSpace(message) == "" {
		return Reactions{}, errors.New("invalid reaction message")
	}
	self, err := c.ResolveIdentity(ctx)
	if err != nil {
		return Reactions{}, err
	}
	all, err := c.listReactions(ctx, message, "")
	if err != nil {
		return Reactions{}, err
	}
	var m Message
	if err := c.get(ctx, "/"+message, url.Values{"fields": {"name,emojiReactionSummaries"}}, &m); err != nil {
		return Reactions{}, err
	}
	state := Reactions{Summaries: m.EmojiReactionSummaries, All: all}
	for _, reaction := range all {
		if reaction.User.Name == self {
			state.Mine = append(state.Mine, reaction)
		}
	}
	return state, nil
}

// SetReaction applies a desired state, so retrying an uncertain request never
// toggles a successfully added reaction back off. Only the caller's verified
// reactions are eligible for deletion. It runs only after a user action.
func (c *Client) SetReaction(ctx context.Context, message string, emoji Emoji, want bool) (Reactions, error) {
	if MessageSpace(message) == "" || !emoji.valid() {
		return Reactions{}, errors.New("invalid reaction target or emoji")
	}
	mine, err := c.myReactions(ctx, message)
	if err != nil {
		return Reactions{}, err
	}
	found := false
	for _, r := range mine {
		if r.Emoji.Key() != emoji.Key() {
			continue
		}
		found = true
		if !want {
			if err := c.writeReaction(ctx, http.MethodDelete, "/"+r.Name, nil); err != nil {
				return Reactions{}, err
			}
		}
	}
	if want && !found {
		var body any = map[string]any{"emoji": map[string]string{"unicode": emoji.Unicode}}
		if emoji.CustomEmoji != nil {
			body = map[string]any{"emoji": map[string]any{"customEmoji": map[string]string{"name": emoji.CustomEmoji.Name}}}
		}
		if err := c.writeReaction(ctx, http.MethodPost, "/"+message+"/reactions", body); err != nil {
			return Reactions{}, err
		}
	}
	return c.Reactions(ctx, message)
}

func (c *Client) writeReaction(ctx context.Context, method, path string, body any) error {
	var data []byte
	if body != nil {
		data, _ = json.Marshal(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, bytes.NewReader(data))
	if err != nil {
		return errors.New("could not prepare reaction")
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := c.http.Do(req)
	if err != nil {
		return errors.New("reaction could not be confirmed; retry to check its state")
	}
	defer res.Body.Close()
	io.Copy(io.Discard, io.LimitReader(res.Body, 4096))
	if res.StatusCode >= 200 && res.StatusCode < 300 {
		return nil
	}
	if method == http.MethodDelete && res.StatusCode == http.StatusNotFound {
		return nil
	}
	return errors.New("reaction could not be confirmed; check access or retry")
}
