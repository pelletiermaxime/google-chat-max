package chat

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"google-chat-max/internal/settings"

	"golang.org/x/oauth2"
)

// Open captures credentials in memory only. It never starts a login flow.
func Open(ctx context.Context, project string) (*Client, error) {
	if err := settings.ValidateProjectID(project); err != nil {
		return nil, errors.New("configure a valid Google Cloud project in Settings before opening Chat")
	}
	gws, err := findGoogleTool("gws")
	if err != nil {
		return nil, errors.New("Google Workspace CLI (gws) was not found; install it or add it to PATH, then open Settings")
	}
	authCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	command := googleToolCommand(authCtx, gws, "auth", "export", "--unmasked")
	output := &credentialBuffer{}
	command.Stdout = output
	if err := command.Run(); err != nil {
		return nil, errors.New("could not read your gws login. Run gws auth status in a terminal, then refresh")
	}
	var saved struct {
		ClientID     string `json:"client_id"`
		ClientSecret string `json:"client_secret"`
		RefreshToken string `json:"refresh_token"`
	}
	if json.Unmarshal(output.Bytes(), &saved) != nil {
		return nil, errors.New("could not decode the existing gws login")
	}
	configDir := os.Getenv("GOOGLE_WORKSPACE_CLI_CONFIG_DIR")
	if configDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, errors.New("could not locate the gws configuration directory")
		}
		configDir = filepath.Join(home, ".config", "gws")
	}
	f, err := os.Open(filepath.Join(configDir, "client_secret.json"))
	if err == nil {
		defer f.Close()
		var client struct {
			Installed struct {
				ClientID     string `json:"client_id"`
				ClientSecret string `json:"client_secret"`
			} `json:"installed"`
		}
		if json.NewDecoder(io.LimitReader(f, 64<<10)).Decode(&client) != nil {
			return nil, errors.New("invalid gws OAuth client configuration")
		}
		if client.Installed.ClientID != "" {
			saved.ClientID, saved.ClientSecret = client.Installed.ClientID, client.Installed.ClientSecret
		}
	} else if !os.IsNotExist(err) {
		return nil, errors.New("could not read the gws OAuth client configuration")
	}
	if saved.ClientID == "" || saved.ClientSecret == "" || saved.RefreshToken == "" {
		return nil, errors.New("the existing gws OAuth login is incomplete")
	}
	config := &oauth2.Config{
		ClientID: saved.ClientID, ClientSecret: saved.ClientSecret,
		Endpoint: oauth2.Endpoint{TokenURL: "https://oauth2.googleapis.com/token", AuthStyle: oauth2.AuthStyleInParams},
	}
	// Resource cleanup must still refresh credentials after the window's
	// context is canceled. TokenSource does no background work on its own.
	tokenCtx := context.WithValue(context.WithoutCancel(ctx), oauth2.HTTPClient, &http.Client{Timeout: 15 * time.Second})
	tokens := config.TokenSource(tokenCtx, &oauth2.Token{RefreshToken: saved.RefreshToken})
	client := oauth2.NewClient(tokenCtx, tokens)
	client.Timeout = 30 * time.Second
	return &Client{http: client, tokens: tokens, project: project, baseURL: "https://chat.googleapis.com/v1", identities: true, identityURL: "https://www.googleapis.com/oauth2/v3/userinfo"}, nil
}

type credentialBuffer struct{ bytes.Buffer }

func (b *credentialBuffer) Write(p []byte) (int, error) {
	if b.Len()+len(p) > 64<<10 {
		return 0, errors.New("gws credential output exceeded its limit")
	}
	return b.Buffer.Write(p)
}
