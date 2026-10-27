package chat

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"google-chat-max/internal/settings"
)

const allSpacesTarget = "//chat.googleapis.com/spaces/-"

type liveResources struct {
	Project   string    `json:"project"`
	ID        string    `json:"id"`
	Operation string    `json:"operation,omitempty"`
	Expires   time.Time `json:"expires,omitempty"`
}

func newResources(project string) (*liveResources, error) {
	if err := settings.ValidateProjectID(project); err != nil {
		return nil, errors.New("a valid Google Cloud project must be configured before connecting")
	}
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return nil, err
	}
	return &liveResources{Project: project, ID: hex.EncodeToString(random[:])}, nil
}

func (r *liveResources) topic() string {
	return "projects/" + r.Project + "/topics/chat-max-" + r.ID
}
func (r *liveResources) queue() string {
	return "projects/" + r.Project + "/subscriptions/chat-max-" + r.ID
}
func (r *liveResources) labels() map[string]string {
	return map[string]string{"app": "google-chat-max", "session": r.ID}
}

func streamJournalPath() (string, error) {
	dir := os.Getenv("XDG_STATE_HOME")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", errors.New("could not locate the live-session state directory")
		}
		dir = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(dir, "google-chat-max", "live-resources.json"), nil
}

// The journal contains only resource ownership IDs, never OAuth credentials or
// message data. Save before cloud setup so ambiguous responses remain recoverable.
func saveResources(path string, r *liveResources) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return errors.New("could not create the live-session state directory")
	}
	data, err := json.Marshal(r)
	if err != nil {
		return err
	}
	if err := os.WriteFile(path+".tmp", data, 0600); err != nil {
		return errors.New("could not save live-session resource IDs")
	}
	if err := os.Rename(path+".tmp", path); err != nil {
		return errors.New("could not update the live-session journal")
	}
	return nil
}

func loadResources(path string) (*liveResources, error) {
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, errors.New("could not read the previous live-session journal")
	}
	defer f.Close()
	var r liveResources
	if json.NewDecoder(io.LimitReader(f, 4096)).Decode(&r) != nil || len(r.ID) != 32 {
		return nil, errors.New("invalid live-session journal; no resources were changed")
	}
	if _, err := hex.DecodeString(r.ID); err != nil {
		return nil, errors.New("invalid live-session ownership ID; no resources were changed")
	}
	if r.Project == "" {
		return nil, errors.New("live-session journal has no project; restore the original Cloud project ID in the journal's project field before retrying; no resources were changed")
	}
	if settings.ValidateProjectID(r.Project) != nil {
		return nil, errors.New("invalid project in live-session journal; no resources were changed")
	}
	return &r, nil
}

func removeJournal(path string) error {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return errors.New("cloud cleanup finished, but the live-session journal could not be removed")
	}
	return nil
}

type streamAPI struct {
	client                  *Client
	pubsubURL, workspaceURL string
}

type cloudError struct{ status int }

func (e *cloudError) Error() string {
	if e.status == http.StatusConflict {
		return "Google reports an existing live subscription; other listeners were left unchanged"
	}
	return fmt.Sprintf("live subscription request failed (HTTP %d)", e.status)
}

func (a *streamAPI) request(ctx context.Context, method, endpoint string, body, out any) error {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, reader)
	if err != nil {
		return errors.New("invalid live-subscription endpoint")
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := a.client.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return errors.New("could not reach the live-subscription service")
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return &cloudError{status: res.StatusCode}
	}
	if out == nil || res.StatusCode == http.StatusNoContent {
		return nil
	}
	data, err := io.ReadAll(io.LimitReader(res.Body, maxResponseBytes+1))
	if err != nil || len(data) > maxResponseBytes || json.Unmarshal(data, out) != nil {
		return errors.New("invalid or oversized live-subscription response")
	}
	return nil
}

type workspaceSubscription struct {
	Name                 string    `json:"name"`
	ExpireTime           time.Time `json:"expireTime"`
	State                string    `json:"state"`
	NotificationEndpoint struct {
		PubsubTopic string `json:"pubsubTopic"`
	} `json:"notificationEndpoint"`
}

type workspaceOperation struct {
	Name     string                `json:"name"`
	Done     bool                  `json:"done"`
	Response workspaceSubscription `json:"response"`
	Error    *struct{ Code int }   `json:"error"`
}

func (a *streamAPI) waitOperation(ctx context.Context, op workspaceOperation) (workspaceSubscription, error) {
	for !op.Done {
		if !strings.HasPrefix(op.Name, "operations/") || strings.ContainsAny(op.Name, "?#%") {
			return workspaceSubscription{}, errors.New("invalid subscription operation name")
		}
		timer := time.NewTimer(time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return workspaceSubscription{}, ctx.Err()
		case <-timer.C:
		}
		if err := a.request(ctx, "GET", a.workspaceURL+"/"+op.Name, nil, &op); err != nil {
			return workspaceSubscription{}, err
		}
	}
	if op.Error != nil {
		return workspaceSubscription{}, fmt.Errorf("subscription operation failed (code %d)", op.Error.Code)
	}
	return op.Response, nil
}

func (a *streamAPI) create(ctx context.Context, r *liveResources, journal string) error {
	if err := a.request(ctx, "PUT", a.pubsubURL+"/"+r.topic(), map[string]any{"labels": r.labels()}, nil); err != nil {
		return err
	}
	policy := map[string]any{"policy": map[string]any{"bindings": []any{map[string]any{
		"role": "roles/pubsub.publisher", "members": []string{"serviceAccount:chat-api-push@system.gserviceaccount.com"},
	}}}}
	if err := a.request(ctx, "POST", a.pubsubURL+"/"+r.topic()+":setIamPolicy", policy, nil); err != nil {
		return err
	}
	queue := map[string]any{
		"topic": r.topic(), "labels": r.labels(), "ackDeadlineSeconds": 20,
		"messageRetentionDuration": "600s", "expirationPolicy": map[string]string{"ttl": "86400s"},
	}
	if err := a.request(ctx, "PUT", a.pubsubURL+"/"+r.queue(), queue, nil); err != nil {
		return err
	}
	body := map[string]any{
		"targetResource": allSpacesTarget, "eventTypes": []string{messageCreated, reactionCreated, reactionDeleted}, "ttl": "900s",
		"notificationEndpoint": map[string]string{"pubsubTopic": r.topic()},
		"payloadOptions":       map[string]any{"includeResource": true},
	}
	var op workspaceOperation
	if err := a.request(ctx, "POST", a.workspaceURL+"/subscriptions", body, &op); err != nil {
		return err
	}
	r.Operation = op.Name
	if err := saveResources(journal, r); err != nil {
		return err
	}
	sub, err := a.waitOperation(ctx, op)
	if err != nil {
		return err
	}
	if sub.Name == "" || sub.ExpireTime.IsZero() || sub.NotificationEndpoint.PubsubTopic != r.topic() {
		return errors.New("subscription setup returned unexpected ownership or expiration")
	}
	// Only unfinished creation operations are needed for crash recovery.
	// Completed operation records can expire independently of the subscription.
	r.Operation = ""
	r.Expires = sub.ExpireTime
	return saveResources(journal, r)
}

func (a *streamAPI) subscriptions(ctx context.Context, r *liveResources) ([]workspaceSubscription, error) {
	query := url.Values{"pageSize": {"100"}, "filter": {`event_types:"` + messageCreated + `" AND target_resource="` + allSpacesTarget + `"`}}
	var owned []workspaceSubscription
	for range 10 {
		var page struct {
			Subscriptions []workspaceSubscription `json:"subscriptions"`
			NextPageToken string                  `json:"nextPageToken"`
		}
		if err := a.request(ctx, "GET", a.workspaceURL+"/subscriptions?"+query.Encode(), nil, &page); err != nil {
			return nil, err
		}
		for _, sub := range page.Subscriptions {
			// Never adopt, modify, or delete another listener.
			if sub.NotificationEndpoint.PubsubTopic == r.topic() && validSubscriptionName(sub.Name) {
				owned = append(owned, sub)
			}
		}
		if page.NextPageToken == "" {
			return owned, nil
		}
		query.Set("pageToken", page.NextPageToken)
	}
	return nil, errors.New("subscription listing exceeded its page limit")
}

func validSubscriptionName(name string) bool {
	return strings.HasPrefix(name, "subscriptions/") && len(name) > len("subscriptions/") && strings.Count(name, "/") == 1 && !strings.ContainsAny(name, "?#%")
}

func (a *streamAPI) renew(ctx context.Context, r *liveResources, journal string) (time.Time, error) {
	subs, err := a.subscriptions(ctx, r)
	if err != nil || len(subs) != 1 {
		return time.Time{}, errors.New("could not find this app's live subscription to renew")
	}
	var op workspaceOperation
	if err := a.request(ctx, "PATCH", a.workspaceURL+"/"+subs[0].Name+"?updateMask=ttl", map[string]any{"name": subs[0].Name, "ttl": "900s"}, &op); err != nil {
		return time.Time{}, err
	}
	sub, err := a.waitOperation(ctx, op)
	if err != nil {
		return time.Time{}, err
	}
	if sub.ExpireTime.Before(time.Now().Add(5*time.Minute)) || sub.State == "SUSPENDED" {
		return time.Time{}, errors.New("live subscription renewal did not produce an active lease")
	}
	r.Expires = sub.ExpireTime
	return r.Expires, saveResources(journal, r)
}

// cleanup checks ownership on the server before deleting any cloud resource.
// The journal survives errors so an explicit reconnect can retry after a crash.
func (a *streamAPI) cleanup(ctx context.Context, r *liveResources) error {
	var failures []error
	owned := make(map[string]bool)
	for _, resource := range []string{r.queue(), r.topic()} {
		var remote struct {
			Labels map[string]string `json:"labels"`
		}
		err := a.request(ctx, "GET", a.pubsubURL+"/"+resource, nil, &remote)
		if isCloudStatus(err, 404) {
			continue
		}
		if err != nil {
			return err
		}
		if remote.Labels["app"] != "google-chat-max" || remote.Labels["session"] != r.ID {
			return errors.New("resource ownership mismatch; resources left unchanged")
		}
		owned[resource] = true
	}
	if r.Operation != "" {
		_, err := a.waitOperation(ctx, workspaceOperation{Name: r.Operation})
		if err != nil && !isCloudStatus(err, 404) {
			failures = append(failures, err)
		}
	}
	subs, err := a.subscriptions(ctx, r)
	if err != nil {
		failures = append(failures, err)
	}
	for _, sub := range subs {
		var op workspaceOperation
		if err := a.request(ctx, "DELETE", a.workspaceURL+"/"+sub.Name, nil, &op); err != nil {
			failures = append(failures, err)
		} else if _, err := a.waitOperation(ctx, op); err != nil {
			failures = append(failures, err)
		}
	}
	for _, resource := range []string{r.queue(), r.topic()} {
		if !owned[resource] {
			continue
		}
		if err := a.request(ctx, "DELETE", a.pubsubURL+"/"+resource, nil, nil); err != nil && !isCloudStatus(err, 404) {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}

func isCloudStatus(err error, code int) bool {
	var cloud *cloudError
	return errors.As(err, &cloud) && cloud.status == code
}
