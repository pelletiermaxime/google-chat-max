# Account setup

Google Chat Max currently needs your own Google Cloud project and OAuth login.
Use the same project for the OAuth client and the app. Your Workspace
administrator may need to allow the APIs and OAuth scopes below.

## Linux packages

The app needs a graphical desktop with GTK 3. On Ubuntu 22.04:

```sh
sudo apt-get update
sudo apt-get install libgtk-3-0 libfontconfig1 libepoxy0 fonts-noto-color-emoji
```

On Arch Linux:

```sh
sudo pacman -S --needed gtk3 fontconfig libepoxy noto-fonts-emoji
```

Release binaries target Linux x86-64 with glibc 2.35 or newer.

## 1. Install the Google tools

Install the [Google Cloud CLI](https://cloud.google.com/sdk/docs/install)
(`gcloud`) and [Google Workspace CLI](https://github.com/googleworkspace/cli)
(`gws`). With Node.js 18+ and npm, you can install `gws` with:

```sh
npm install -g @googleworkspace/cli
```

Both tools must be on your `PATH` for setup. Afterward, the app only needs `gws`.

## 2. Configure your Cloud project

Create or select a project in [Google Cloud Console](https://console.cloud.google.com/)
and enable billing. Replace `your-project-id` below with its project ID:

```sh
gcloud auth login
gcloud services enable chat.googleapis.com workspaceevents.googleapis.com pubsub.googleapis.com \
  --project=your-project-id
gws auth setup --project your-project-id
```

Follow the setup prompts to configure a **Desktop app** OAuth client. For an
External app in Testing, add your Google account as a test user. In the Chat API
configuration, fill in the app name, avatar URL, and description.

If the wizard can't finish, follow the
[manual OAuth setup](https://github.com/googleworkspace/cli#manual-oauth-setup-google-cloud-console).
Keep the downloaded client JSON in `~/.config/gws/client_secret.json`, or your
`GOOGLE_WORKSPACE_CLI_CONFIG_DIR`, outside this repository.

The app creates its own delivery resources. You don't need to create topics or
subscriptions manually. Google's [subscription setup guide](https://developers.google.com/workspace/events/guides/create-subscription)
has more detail on the project requirements.

## 3. Check Cloud permissions

The account you'll use in `gws` needs permission to create, receive from, and
delete Pub/Sub resources. **Pub/Sub Admin** (`roles/pubsub.admin`) covers this,
but grants access to all Pub/Sub resources in the project. For narrower access,
ask an administrator for a custom role with:

```text
pubsub.topics.create
pubsub.topics.get
pubsub.topics.delete
pubsub.topics.setIamPolicy
pubsub.topics.attachSubscription
pubsub.subscriptions.create
pubsub.subscriptions.get
pubsub.subscriptions.delete
pubsub.subscriptions.consume
```

Enabling APIs and configuring OAuth require additional setup permissions.
See the [Pub/Sub IAM reference](https://cloud.google.com/pubsub/docs/access-control).

## 4. Sign in to Google Chat

Run this command and approve the requested permissions:

```sh
gws auth login --scopes 'openid,https://www.googleapis.com/auth/userinfo.profile,https://www.googleapis.com/auth/chat.spaces.readonly,https://www.googleapis.com/auth/chat.memberships.readonly,https://www.googleapis.com/auth/chat.messages,https://www.googleapis.com/auth/chat.customemojis.readonly,https://www.googleapis.com/auth/pubsub'
gws auth status
```

This grants access to your profile, conversations, messages, reactions, custom
emoji, and Pub/Sub delivery resources. Add the same scopes in the OAuth client's
Data Access settings if prompted. Custom emoji also need to be enabled by your
Workspace administrator.

The app needs a saved user OAuth login with a refresh token. A `gcloud` login,
service account, or access token alone isn't enough. Keep credentials private;
don't paste the output of `gws auth export --unmasked` into issues or files.

## 5. Launch the app

```sh
./google-chat-max
```

For a source build, use `./bin/google-chat-max`. Enter your project ID and click
**Save and open Chat**. Incoming messages start automatically.

Settings are stored in `~/.config/google-chat-max/config.json`, or under
`$XDG_CONFIG_HOME`. Use **Settings** in the sidebar to change the project.

## Common problems

- **Missing login:** run `gws auth status` in the environment that launches the
  app, then repeat the scoped login above.
- **OAuth access blocked:** check your OAuth audience, test users, and your
  organization's app-access policy.
- **HTTP 401/403:** check the enabled APIs, granted scopes, and the `gws` account's
  IAM permissions in the selected project.
- **Incoming messages stopped:** fix the access or network problem, then click
  **Retry incoming messages**. Temporary network failures reconnect automatically.
- **Existing subscription:** another listener may already be active. The app
  leaves it alone; stop it only if you own it and intend to replace it.
- **Missing names or custom emoji:** check membership/emoji scopes and
  organization settings. Names can fall back to resource IDs.

## Cleanup

Close the window normally to let the app delete its cloud resources. If the app
crashes or cleanup fails, reopen it to retry. It records resource ownership in
`~/.local/state/google-chat-max/live-resources.json`, or under `$XDG_STATE_HOME`.
Keep that file until cleanup succeeds. Topics don't expire automatically and
can remain billable; the receiving subscription expires after a day of inactivity.

If the journal reports **no project**, close the app and back up the file. Find
the original project in your Cloud Console or setup records, then add its ID as
the top-level `"project"` field without changing the other fields. Reopen the app.
Don't guess or substitute a newly selected project.

For manual cleanup, match the journal's exact project and session ID to the
`chat-max-<id>` resources. Verify the labels `app=google-chat-max` and
`session=<id>`. Delete the Workspace Events subscription whose endpoint matches
that exact topic, then the receiving Pub/Sub subscription, then the topic.
Use the original OAuth account and client for Workspace subscription deletion;
Cloud administrator IAM alone doesn't replace that authorization. See
[Google's deletion guide](https://developers.google.com/workspace/events/guides/delete-subscription).
Remove the journal only after confirming deletion, and preserve other listeners.
