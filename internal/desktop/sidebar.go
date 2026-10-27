package desktop

import (
	"slices"
	"strings"

	"google-chat-max/internal/chat"
)

const recentPrivateLimit = 10

type sidebarGroup struct {
	label string
	items []chat.Space
	total int
}

// Google represents continuous meeting chats as unthreaded SPACE resources.
// This threading state also covers legacy group conversations: both belong
// beside DMs/group chats, rather than alongside regular collaboration spaces.
func privateConversation(s chat.Space) bool {
	return !s.SingleUserBotDM && (s.SpaceType != "SPACE" || s.SpaceThreadingState == "UNTHREADED_MESSAGES")
}

func (a *App) sidebarGroups() []sidebarGroup {
	groups := []sidebarGroup{{label: "Private messages"}, {label: "Spaces"}, {label: "Apps"}}
	for _, s := range a.spaces {
		if !a.matches(s) {
			continue
		}
		group := 1
		if s.SingleUserBotDM {
			group = 2
		} else if privateConversation(s) {
			group = 0
		}
		groups[group].items = append(groups[group].items, s)
	}
	for i := range groups {
		group := &groups[i]
		slices.SortStableFunc(group.items, func(a, b chat.Space) int {
			if i == 0 || i == 1 {
				if order := b.LastActiveTime.Compare(a.LastActiveTime); order != 0 {
					return order
				}
			}
			if order := strings.Compare(strings.ToLower(a.Title()), strings.ToLower(b.Title())); order != 0 {
				return order
			}
			return strings.Compare(a.Name, b.Name)
		})
		group.total = len(group.items)
	}
	if !a.showAllPrivate && strings.TrimSpace(a.query) == "" && len(groups[0].items) > recentPrivateLimit {
		groups[0].items = groups[0].items[:recentPrivateLimit]
	}
	return groups
}
