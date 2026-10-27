package chat

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"time"
)

type User struct {
	Name        string `json:"name"`
	DisplayName string `json:"displayName"`
	AvatarURL   string `json:"avatarUrl"`
	Email       string `json:"email"`
	Type        string `json:"type"`
}

// WithFallback enriches incomplete identities without discarding the app name
// and avatar supplied on a message in favor of a name-only membership record.
func (u User) WithFallback(other User) User {
	if u.Name == "" {
		u.Name = other.Name
	}
	if u.DisplayName == "" {
		u.DisplayName = other.DisplayName
	}
	if u.AvatarURL == "" {
		u.AvatarURL = other.AvatarURL
	}
	if u.Email == "" {
		u.Email = other.Email
	}
	if u.Type == "" {
		u.Type = other.Type
	}
	return u
}

type memberCache struct {
	users []User
	at    time.Time
}

// Self returns the authenticated user's resource name once profile resolution
// has completed. It reads cached identity only and performs no network work.
func (c *Client) Self() string {
	c.identityMu.Lock()
	defer c.identityMu.Unlock()
	return c.self
}

func (c *Client) cachedMembers(space string) ([]User, bool) {
	c.identityMu.Lock()
	defer c.identityMu.Unlock()
	entry, ok := c.members[space]
	if !ok || time.Since(entry.at) >= 10*time.Minute {
		return nil, false
	}
	return entry.users, true
}

func (c *Client) Members(ctx context.Context, space string) ([]User, error) {
	if !validSpace(space) {
		return nil, errors.New("invalid conversation resource name")
	}
	if users, ok := c.cachedMembers(space); ok {
		return users, nil
	}
	query := url.Values{"pageSize": {"100"}, "fields": {"memberships(member(name,displayName,avatarUrl,email,type)),nextPageToken"}}
	var users []User
	for range 2 {
		var page struct {
			Memberships []struct {
				Member User `json:"member"`
			} `json:"memberships"`
			Next string `json:"nextPageToken"`
		}
		if err := c.get(ctx, "/"+space+"/members", query, &page); err != nil {
			return nil, err
		}
		for _, m := range page.Memberships {
			if m.Member.Name != "" {
				users = append(users, m.Member)
			}
		}
		if page.Next == "" {
			break
		}
		query.Set("pageToken", page.Next)
	}
	c.identityMu.Lock()
	if c.members == nil {
		c.members = make(map[string]memberCache)
	}
	if len(c.members) >= 64 {
		for key := range c.members {
			delete(c.members, key)
			break
		}
	}
	c.members[space] = memberCache{users: users, at: time.Now()}
	c.identityMu.Unlock()
	return users, nil
}

func validSpace(name string) bool {
	return strings.HasPrefix(name, "spaces/") && len(name) > 7 && strings.Count(name, "/") == 1 && !strings.ContainsAny(name, "?#%")
}

// ResolveIdentity loads the account profile once, separately from listing spaces.
func (c *Client) ResolveIdentity(ctx context.Context) (string, error) {
	if self := c.Self(); self != "" {
		return self, nil
	}
	if c.identityURL == "" {
		return "", errors.New("account profile is unavailable")
	}
	var profile struct {
		Sub string `json:"sub"`
	}
	if err := c.getURL(ctx, c.identityURL, &profile); err != nil {
		return "", err
	}
	if profile.Sub == "" {
		return "", errors.New("account profile has no user identity")
	}
	c.identityMu.Lock()
	c.self = "users/" + profile.Sub
	self := c.self
	c.identityMu.Unlock()
	return self, nil
}

// ResolveSpaceIdentity enriches a space using memberships and the resolved profile.
func (c *Client) ResolveSpaceIdentity(ctx context.Context, space Space) (Space, error) {
	users, err := c.Members(ctx, space.Name)
	if err != nil {
		return space, err
	}
	return spaceWithIdentity(space, users, c.Self()), nil
}

func spaceWithIdentity(space Space, users []User, self string) Space {
	space.Participants = users
	var names []string
	for _, u := range users {
		if u.Name == self {
			continue
		}
		if u.DisplayName != "" {
			names = append(names, u.DisplayName)
		}
		if space.AvatarURL == "" {
			space.AvatarURL = u.AvatarURL
		}
	}
	if space.DisplayName == "" && len(names) > 0 {
		space.DisplayName = strings.Join(names, ", ")
	}
	return space
}
