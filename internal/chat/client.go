// Package chat provides read-only access to Google Chat using the host's gws login.
package chat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"golang.org/x/oauth2"
)

const HistoryLimit = 60
const maxResponseBytes = 4 << 20

type Space struct {
	Name                string    `json:"name"`
	DisplayName         string    `json:"displayName"`
	SpaceType           string    `json:"spaceType"`
	SpaceThreadingState string    `json:"spaceThreadingState"`
	LastActiveTime      time.Time `json:"lastActiveTime"`
	SingleUserBotDM     bool      `json:"singleUserBotDm"`
	Participants        []User    `json:"-"`
	AvatarURL           string    `json:"-"`
}

func (s Space) Title() string {
	if s.DisplayName != "" {
		return s.DisplayName
	}
	kind := "Conversation"
	if s.SpaceType == "DIRECT_MESSAGE" {
		kind = "Direct message"
	} else if s.SpaceType == "GROUP_CHAT" {
		kind = "Group chat"
	}
	return kind + " · " + strings.TrimPrefix(s.Name, "spaces/")
}

type Message struct {
	Name                   string                 `json:"name"`
	CreateTime             time.Time              `json:"createTime"`
	LastUpdateTime         time.Time              `json:"lastUpdateTime"`
	Text                   string                 `json:"text"`
	FallbackText           string                 `json:"fallbackText"`
	Cards                  []Card                 `json:"cards"`
	CardsV2                []CardWithID           `json:"cardsV2"`
	Sender                 User                   `json:"sender"`
	QuotedMessageMetadata  *QuotedMessageMetadata `json:"quotedMessageMetadata,omitempty"`
	EmojiReactionSummaries []ReactionSummary      `json:"emojiReactionSummaries"`
	Thread                 struct {
		Name string `json:"name"`
	} `json:"thread"`
	ThreadReply  bool          `json:"threadReply"`
	Attachment   []Attachment  `json:"attachment"`
	AttachedGIFs []AttachedGIF `json:"attachedGifs"`
}

func (m Message) Author() string {
	if m.Sender.DisplayName != "" {
		return m.Sender.DisplayName
	}
	if m.Sender.Name != "" {
		return "User " + strings.TrimPrefix(m.Sender.Name, "users/")
	}
	return "Unknown sender"
}

type Client struct {
	project     string
	http        *http.Client
	baseURL     string
	tokens      oauth2.TokenSource
	identities  bool
	identityURL string
	identityMu  sync.Mutex
	self        string
	members     map[string]memberCache
}

func (c *Client) get(ctx context.Context, path string, query url.Values, out any) error {
	return c.getURL(ctx, c.baseURL+path+"?"+query.Encode(), out)
}

func (c *Client) getURL(ctx context.Context, endpoint string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	res, err := c.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return errors.New("could not reach Google Chat. Check your connection and gws login, then refresh")
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		// Keep server response bodies out of errors and logs.
		switch res.StatusCode {
		case http.StatusUnauthorized, http.StatusForbidden:
			return fmt.Errorf("Google Chat access denied (HTTP %d). Check your gws login and Chat read permissions", res.StatusCode)
		case http.StatusTooManyRequests:
			return errors.New("Google Chat is rate limiting requests. Wait a moment, then refresh")
		default:
			return fmt.Errorf("Google Chat returned HTTP %d. Try refreshing", res.StatusCode)
		}
	}
	data, err := io.ReadAll(io.LimitReader(res.Body, maxResponseBytes+1))
	if err != nil {
		return errors.New("could not read Google Chat's response")
	}
	if len(data) > maxResponseBytes {
		return errors.New("Google Chat response exceeded the 4 MiB limit")
	}
	if err := json.Unmarshal(data, out); err != nil {
		return errors.New("Google Chat returned an invalid response")
	}
	return nil
}

const spaceFields = "name,displayName,spaceType,spaceThreadingState,lastActiveTime,singleUserBotDm"

// Space resolves metadata for a conversation first discovered through an event.
func (c *Client) Space(ctx context.Context, name string) (Space, error) {
	if !validSpace(name) {
		return Space{}, errors.New("invalid conversation resource name")
	}
	var space Space
	if err := c.get(ctx, "/"+name, url.Values{"fields": {spaceFields}}, &space); err != nil {
		return Space{}, err
	}
	if space.Name != name || space.SpaceType == "" {
		return Space{}, errors.New("Google Chat returned incomplete conversation metadata")
	}
	return space, nil
}

func (c *Client) Spaces(ctx context.Context) ([]Space, error) {
	var spaces []Space
	query := url.Values{"pageSize": {"100"}, "fields": {"spaces(" + spaceFields + "),nextPageToken"}}
	for range 10 {
		var result struct {
			Spaces        []Space `json:"spaces"`
			NextPageToken string  `json:"nextPageToken"`
		}
		if err := c.get(ctx, "/spaces", query, &result); err != nil {
			return nil, err
		}
		spaces = append(spaces, result.Spaces...)
		if len(spaces) > 1000 {
			return nil, errors.New("conversation list exceeded the 1000 conversation limit")
		}
		if result.NextPageToken == "" {
			if c.identities {
				self := c.Self()
				for i, space := range spaces {
					if !space.SingleUserBotDM && (space.SpaceType == "DIRECT_MESSAGE" || space.SpaceType == "GROUP_CHAT") {
						if users, ok := c.cachedMembers(space.Name); ok && self != "" {
							spaces[i] = spaceWithIdentity(space, users, self)
						}
					}
				}
			}
			slices.SortStableFunc(spaces, func(a, b Space) int {
				return strings.Compare(strings.ToLower(a.Title()), strings.ToLower(b.Title()))
			})
			return spaces, nil
		}
		query.Set("pageToken", result.NextPageToken)
	}
	return nil, errors.New("conversation list exceeded the 10 page limit")
}

func (c *Client) Messages(ctx context.Context, space string) ([]Message, error) {
	page, err := c.History(ctx, space, "", "")
	return page.Messages, err
}
