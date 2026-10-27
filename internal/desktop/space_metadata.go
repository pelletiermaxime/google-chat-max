package desktop

import (
	"context"
	"time"

	"google-chat-max/internal/chat"
)

type spaceMetadataBackend interface {
	Space(context.Context, string) (chat.Space, error)
}

type spaceMetadataJob struct {
	space      chat.Space
	backend    spaceMetadataBackend
	generation uint64
}

func needsSpaceMetadata(space chat.Space) bool {
	return space.SpaceType == "" || (space.DisplayName == "" && !space.SingleUserBotDM && (space.SpaceType == "DIRECT_MESSAGE" || space.SpaceType == "GROUP_CHAT"))
}

// Event payloads can identify a conversation without its name or type. Resolve
// those fields independently of message delivery, with one pending job per
// conversation and the same four-request concurrency limit as startup identities.
func (a *App) queueSpaceMetadata(space chat.Space) {
	b, ok := a.backend.(spaceMetadataBackend)
	if !ok || a.closed || a.closing || !needsSpaceMetadata(space) {
		return
	}
	if job := a.spaceMetadataPending[space.Name]; job != nil && job.generation == a.spaceIdentityGeneration {
		return
	}
	// Match the conversation-list bound; a burst cannot grow the work queue
	// beyond the number of conversations the app can retain.
	if len(a.spaceMetadataQueue) >= 1000 {
		return
	}
	if a.spaceMetadataPending == nil {
		a.spaceMetadataPending = make(map[string]*spaceMetadataJob)
	}
	job := &spaceMetadataJob{space: space, backend: b, generation: a.spaceIdentityGeneration}
	a.spaceMetadataPending[space.Name] = job
	a.spaceMetadataQueue = append(a.spaceMetadataQueue, job)
	a.startSpaceMetadata()
}

func (a *App) startSpaceMetadata() {
	for !a.closed && !a.closing && a.spaceMetadataActive < spaceIdentityWorkers && len(a.spaceMetadataQueue) > 0 {
		job := a.spaceMetadataQueue[0]
		a.spaceMetadataQueue[0] = nil
		a.spaceMetadataQueue = a.spaceMetadataQueue[1:]
		if len(a.spaceMetadataQueue) == 0 {
			a.spaceMetadataQueue = nil
		}
		if job.generation != a.spaceIdentityGeneration {
			if a.spaceMetadataPending[job.space.Name] == job {
				delete(a.spaceMetadataPending, job.space.Name)
			}
			continue
		}
		a.spaceMetadataActive++
		ctx, cancel := context.WithTimeout(a.ctx, 10*time.Second)
		go func() {
			defer cancel()
			resolved, err := job.space, error(nil)
			if resolved.SpaceType == "" {
				resolved, err = job.backend.Space(ctx, job.space.Name)
			}
			var self string
			if b, ok := job.backend.(spaceIdentityBackend); err == nil && ok && needsSpaceMetadata(resolved) {
				// Private conversations without explicit titles need member names.
				// Keep the fetched type even if identity enrichment is unavailable.
				if identity, profileErr := b.ResolveIdentity(ctx); profileErr == nil {
					self = identity
					if full, lookupErr := b.ResolveSpaceIdentity(ctx, resolved); lookupErr == nil {
						resolved = full
					}
				}
			}
			a.dispatch(func() {
				a.spaceMetadataActive--
				if a.spaceMetadataPending[job.space.Name] == job {
					delete(a.spaceMetadataPending, job.space.Name)
				}
				if !a.closed && job.generation == a.spaceIdentityGeneration && err == nil && resolved.Name == job.space.Name {
					if self != "" {
						a.selfUser = self
					}
					for i := range a.spaces {
						space := &a.spaces[i]
						if space.Name != resolved.Name {
							continue
						}
						space.SpaceType, space.SpaceThreadingState, space.SingleUserBotDM = resolved.SpaceType, resolved.SpaceThreadingState, resolved.SingleUserBotDM
						if resolved.DisplayName != "" {
							space.DisplayName = resolved.DisplayName
						}
						if resolved.AvatarURL != "" {
							space.AvatarURL = resolved.AvatarURL
						}
						if len(resolved.Participants) > 0 {
							space.Participants = resolved.Participants
						}
						if resolved.LastActiveTime.After(space.LastActiveTime) {
							space.LastActiveTime = resolved.LastActiveTime
						}
						a.rememberUsers(*space)
						break
					}
					a.invalidate()
				}
				a.startSpaceMetadata()
			})
		}()
	}
}
