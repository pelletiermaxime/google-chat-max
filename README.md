# Google Chat Max

A native Google Chat app for Linux, built with Go and [MyGo](https://github.com/egoist/mygo).

**Experimental.** Linux x86-64 only. Not affiliated with Google.

![Google Chat Max](docs/images/google-chat-max.png)

## What it does

- Browse direct messages, group chats, and spaces.
- Reply in threads or quote a message.
- Add reactions with standard and custom emoji.
- View images and animated GIFs inline.
- Receive new messages as they arrive.

## Install

Download the Linux archive and its `.sha256` file from
[Releases](https://github.com/pelletiermaxime/google-chat-max/releases).
For version 0.1.0:

```sh
sha256sum -c google-chat-max-v0.1.0-linux-amd64.tar.gz.sha256
tar -xzf google-chat-max-v0.1.0-linux-amd64.tar.gz
cd google-chat-max-v0.1.0-linux-amd64
```

The app requires GTK 3 and glibc 2.35+;
see [Linux packages](docs/setup.md#linux-packages) if anything is missing.

## Connect your account

You'll need your own Google Cloud project and a
[Google Workspace CLI](https://github.com/googleworkspace/cli) (`gws`) login.
Follow the [setup guide](docs/setup.md), then launch the app:

```sh
./google-chat-max
```

Enter your project ID on first launch. You can change it later in **Settings**.
New messages appear automatically.

**Cloud usage may incur charges.** Close the app normally so it can remove its
cloud resources. If cleanup fails, follow the [recovery steps](docs/setup.md#cleanup).

## Using the app

- **Ctrl+Enter** sends a message. **Enter** adds a new line.
- Hover over a message to reply, quote, or react.
- Scroll up for older messages; use **Latest** to return to recent messages.
- Search the sidebar to find a conversation. **Show more** reveals older chats.

## Build from source

Requires Go 1.27.3+, a C compiler, and the [Linux packages](docs/setup.md#linux-packages).
From the repository root:

```sh
GOMAXPROCS=6 go build -mod=readonly -tags=netcgo -p 6 -o bin/google-chat-max ./cmd/google-chat-max
./bin/google-chat-max
```

See [development](docs/testing.md) for tests and release builds, or
[architecture](docs/architecture.md) for implementation details.

Bundled Unicode emoji data uses the [Unicode license](internal/emojidata/LICENSE).
