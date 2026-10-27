package desktop

import (
	"context"
	"slices"
	"strings"
	"sync"
	"time"

	"google-chat-max/internal/chat"
)

const spaceIdentityLimit = 32
const spaceIdentityWorkers = 4

type spaceIdentityBackend interface {
	ResolveIdentity(context.Context) (string, error)
	ResolveSpaceIdentity(context.Context, chat.Space) (chat.Space, error)
}

func (a *App) resolveSpaceIdentities() {
	b, ok := a.backend.(spaceIdentityBackend)
	if !ok {
		return
	}
	var spaces []chat.Space
	for _, space := range a.spaces {
		if !space.SingleUserBotDM && (space.SpaceType == "DIRECT_MESSAGE" || space.SpaceType == "GROUP_CHAT") {
			spaces = append(spaces, space)
		}
	}
	slices.SortFunc(spaces, func(a, b chat.Space) int {
		if n := b.LastActiveTime.Compare(a.LastActiveTime); n != 0 {
			return n
		}
		return strings.Compare(a.Name, b.Name)
	})
	spaces = spaces[:min(len(spaces), spaceIdentityLimit)]
	ctx, cancel := context.WithTimeout(a.ctx, 45*time.Second)
	a.spaceIdentityCancel = cancel
	generation := a.spaceIdentityGeneration
	go func() {
		defer cancel()
		profileCtx, profileCancel := context.WithTimeout(ctx, 10*time.Second)
		self, err := b.ResolveIdentity(profileCtx)
		profileCancel()
		if err != nil {
			return
		}
		a.dispatch(func() {
			if a.closed || a.spaceIdentityGeneration != generation {
				return
			}
			a.selfUser = self
			a.invalidate()
		})
		jobs := make(chan chat.Space)
		var workers sync.WaitGroup
		for range min(spaceIdentityWorkers, len(spaces)) {
			workers.Add(1)
			go func() {
				defer workers.Done()
				for space := range jobs {
					if ctx.Err() != nil {
						return
					}
					lookupCtx, lookupCancel := context.WithTimeout(ctx, 10*time.Second)
					resolved, err := b.ResolveSpaceIdentity(lookupCtx, space)
					lookupCancel()
					if err != nil {
						continue
					}
					a.dispatch(func() {
						if a.closed || a.spaceIdentityGeneration != generation {
							return
						}
						// Patch only identity fields: a live event may have moved
						// this space forward while its membership was loading.
						for i := range a.spaces {
							if a.spaces[i].Name == resolved.Name {
								a.spaces[i].DisplayName = resolved.DisplayName
								a.spaces[i].AvatarURL = resolved.AvatarURL
								a.spaces[i].Participants = resolved.Participants
								a.rememberUsers(a.spaces[i])
								break
							}
						}
						a.invalidate()
					})
				}
			}()
		}
	feed:
		for _, space := range spaces {
			select {
			case jobs <- space:
			case <-ctx.Done():
				break feed
			}
		}
		close(jobs)
		workers.Wait()
	}()
}
