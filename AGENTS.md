# Google Chat Max

- Never git commit or git push without asking the user first.
- Build a native Go interface with `github.com/egoist/mygo/ui`. No webview,
  Electron, HTML frontend, or embedded Google Chat website.
- Keep this a standalone project.
- The connection-test CLI was disposable and has been replaced by the native app.
- Keep OAuth credentials in the existing host credential store and process
  memory. Never print, copy into the repo, or commit tokens or client secrets.
- Use user-authenticated Google Chat calls. New messages arrive through Pub/Sub
  StreamingPull; periodic message polling is not the chosen architecture.
- Use the Google Cloud project selected in app settings. Keep resources isolated
  and bounded; delete only resources created by this app. Preserve existing Chat
  listeners.
- Never send Chat test messages automatically. Message posting must follow an
  explicit user action, and live test messages require explicit authorization.
- Keep network work off the GUI thread; apply UI state changes on MyGo's main
  thread. Bound message history, event queues, and media caches.
- For builds, use `GOMAXPROCS=6` and `go build -p 6`. Inspect effective cgroup
  memory limits before substantial validation; host free RAM is not always the
  available process budget. Stop on an OOM kill.
- Keep runtime evidence separate from compilation and from UI verification.
