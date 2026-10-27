package chat

import (
	"context"
	"encoding/json"
	"os/exec"

	"google-chat-max/internal/settings"
)

// AuthStatus contains setup metadata only, never credentials or raw CLI output.
type AuthStatus struct {
	GWSInstalled, GCloudInstalled bool
	ClientConfigured, LoginFound  bool
	SuggestedProject              string
	Error                         string
}

func CheckAuth(ctx context.Context) AuthStatus {
	var status AuthStatus
	_, err := exec.LookPath("gcloud")
	status.GCloudInstalled = err == nil
	path, err := exec.LookPath("gws")
	if err != nil {
		return status
	}
	status.GWSInstalled = true
	command := exec.CommandContext(ctx, path, "auth", "status")
	output := &credentialBuffer{}
	command.Stdout = output
	if err := command.Run(); err != nil {
		status.Error = "Could not check the saved gws login. Run gws auth status in a terminal, then check again."
		return status
	}
	var saved struct {
		AuthMethod         string `json:"auth_method"`
		ClientConfigExists bool   `json:"client_config_exists"`
		ClientID           string `json:"client_id"`
		HasRefreshToken    bool   `json:"has_refresh_token"`
		ProjectID          string `json:"project_id"`
	}
	if json.Unmarshal(output.Bytes(), &saved) != nil {
		status.Error = "Could not read the gws authentication status. Check that the Google Workspace CLI is up to date."
		return status
	}
	status.ClientConfigured = saved.ClientConfigExists || saved.ClientID != ""
	status.LoginFound = saved.AuthMethod == "oauth2" && saved.HasRefreshToken && status.ClientConfigured
	if settings.ValidateProjectID(saved.ProjectID) == nil {
		status.SuggestedProject = saved.ProjectID
	}
	return status
}
