package chat

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"
)

type HistoryPage struct {
	Messages []Message
	Next     string
}
type ThreadHistory struct {
	Root *Message
	Page HistoryPage
}

const messageFields = "name,createTime,lastUpdateTime,text,fallbackText,cards,cardsV2,sender,emojiReactionSummaries,quotedMessageMetadata(name,lastUpdateTime,quotedMessageSnapshot(sender,text)),thread(name),threadReply,attachment(name,contentName,contentType,source,attachmentDataRef(resourceName))"

const InitialHistoryLimit = 20

func ValidThread(space, thread string) bool {
	return validSpace(space) && strings.HasPrefix(thread, space+"/threads/") && strings.Count(thread, "/") == 3 && len(thread) > len(space+"/threads/") && !strings.ContainsAny(thread, "?#%")
}

func (c *Client) History(ctx context.Context, space, thread, token string) (HistoryPage, error) {
	if !validSpace(space) || (thread != "" && !ValidThread(space, thread)) || len(token) > 8192 {
		return HistoryPage{}, errors.New("invalid history request")
	}
	limit := HistoryLimit
	if token == "" {
		limit = InitialHistoryLimit
	}
	query := url.Values{"pageSize": {fmt.Sprint(limit)}, "orderBy": {"createTime DESC"}, "fields": {"messages(" + messageFields + "),nextPageToken"}}
	if thread != "" {
		query.Set("filter", "thread.name = "+thread)
	}
	if token != "" {
		query.Set("pageToken", token)
	}
	var result struct {
		Messages []Message `json:"messages"`
		Next     string    `json:"nextPageToken"`
	}
	if err := c.get(ctx, "/"+space+"/messages", query, &result); err != nil {
		return HistoryPage{}, err
	}
	if len(result.Messages) > limit {
		return HistoryPage{}, errors.New("Google Chat exceeded the message history limit")
	}
	slices.Reverse(result.Messages)
	if c.identities {
		// Membership requests must not delay the first visible messages.
		// The desktop loads missing identities independently after selection.
		users, _ := c.cachedMembers(space)
		for i := range result.Messages {
			for _, u := range users {
				if result.Messages[i].Sender.Name == u.Name {
					result.Messages[i].Sender = result.Messages[i].Sender.WithFallback(u)
					break
				}
			}
		}
	}
	return HistoryPage{Messages: result.Messages, Next: result.Next}, nil
}

func (c *Client) Thread(ctx context.Context, space, thread string) (ThreadHistory, error) {
	page, err := c.History(ctx, space, thread, "")
	if err != nil {
		return ThreadHistory{}, err
	}
	result := ThreadHistory{Page: page}
	for _, m := range page.Messages {
		if !m.ThreadReply {
			root := m
			result.Root = &root
			break
		}
	}
	if result.Root == nil {
		query := url.Values{"pageSize": {"1"}, "orderBy": {"createTime ASC"}, "filter": {"thread.name = " + thread}, "fields": {"messages(" + messageFields + ")"}}
		var first struct {
			Messages []Message `json:"messages"`
		}
		if err := c.get(ctx, "/"+space+"/messages", query, &first); err != nil {
			return ThreadHistory{}, err
		}
		if len(first.Messages) > 0 && !first.Messages[0].ThreadReply {
			root := first.Messages[0]
			result.Root = &root
		}
	}
	if c.identities && result.Root != nil {
		users, _ := c.cachedMembers(space)
		for _, u := range users {
			if u.Name == result.Root.Sender.Name {
				result.Root.Sender = result.Root.Sender.WithFallback(u)
				break
			}
		}
	}
	return result, nil
}
