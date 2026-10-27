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
	"time"
)

var ErrReadStatePermission = errors.New("Google Chat denied read-status syncing. Update your gws login with the chat.users.readstate permission, then restart the app")

// MarkRead advances the calling user's top-level conversation read position.
// Check Google's position first so stale cached history does not rewind a
// position already advanced by another client. Thread replies are unaffected.
func (c *Client) MarkRead(ctx context.Context, space string, through time.Time) (time.Time, error) {
	if !validSpace(space) || through.IsZero() || through.Year() < 1 || through.Year() >= 9999 {
		return time.Time{}, errors.New("invalid conversation read position")
	}
	current, err := c.spaceReadState(ctx, space, time.Time{})
	if err != nil || !current.Before(through) {
		return current, err
	}
	if err := ctx.Err(); err != nil {
		return time.Time{}, err
	}
	// Google asks for a value later than the latest read message and coerces
	// it to the message timestamp. Do not use now: newer unseen messages may
	// already exist on the server while their events are still in flight.
	confirmed, err := c.spaceReadState(ctx, space, through.Add(time.Nanosecond))
	if err == nil && confirmed.Before(through) {
		err = errors.New("Google Chat did not confirm the read position. Retry read sync")
	}
	return confirmed, err
}

func (c *Client) spaceReadState(ctx context.Context, space string, through time.Time) (time.Time, error) {
	name := "users/me/" + space + "/spaceReadState"
	endpoint := c.baseURL + "/" + name
	method := http.MethodGet
	var body []byte
	if !through.IsZero() {
		method = http.MethodPatch
		endpoint += "?" + url.Values{"updateMask": {"lastReadTime"}}.Encode()
		body, _ = json.Marshal(map[string]string{"lastReadTime": through.UTC().Format(time.RFC3339Nano)})
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(body))
	if err != nil {
		return time.Time{}, errors.New("could not prepare read-status syncing")
	}
	if method == http.MethodPatch {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := c.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return time.Time{}, ctx.Err()
		}
		return time.Time{}, errors.New("could not reach Google Chat to sync read status. Retry read sync")
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		if res.StatusCode == http.StatusUnauthorized || res.StatusCode == http.StatusForbidden {
			return time.Time{}, ErrReadStatePermission
		}
		return time.Time{}, fmt.Errorf("Google Chat could not sync read status (HTTP %d). Retry read sync", res.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(res.Body, maxResponseBytes+1))
	var state struct {
		Name         string    `json:"name"`
		LastReadTime time.Time `json:"lastReadTime"`
	}
	if err != nil || len(data) > maxResponseBytes || json.Unmarshal(data, &state) != nil {
		return time.Time{}, errors.New("Google Chat returned invalid read-status information")
	}
	// The response can replace the me alias with the user's canonical ID.
	parts := strings.Split(state.Name, "/")
	if len(parts) != 5 || parts[0] != "users" || parts[1] == "" || strings.Join(parts[2:], "/") != space+"/spaceReadState" || (method == http.MethodPatch && state.LastReadTime.IsZero()) {
		return time.Time{}, errors.New("Google Chat returned incomplete read-status information")
	}
	return state.LastReadTime, nil
}
