package main

import (
	"flag"
	"log"
	"os"
	"runtime"
	"strings"

	"google-chat-max/internal/desktop"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

func main() {
	// This host's resolved NSS backend works through libc; direct Go DNS
	// requests to its local stub time out. Respect an explicit user override.
	if runtime.GOOS == "linux" && !strings.Contains(os.Getenv("GODEBUG"), "netdns=") {
		debug := strings.Trim(os.Getenv("GODEBUG")+",netdns=cgo", ",")
		if err := os.Setenv("GODEBUG", debug); err != nil {
			log.Fatal("could not configure the system DNS resolver")
		}
	}
	demo := flag.Bool("demo", false, "show offline sample conversations without accessing Google")
	// Accept older desktop launchers; ordinary startup now always connects.
	flag.Bool("connect", true, "deprecated: incoming messages connect automatically")
	flag.Parse()
	mygo.App.SetName("Google Chat Max")
	if !mygo.App.RequestSingleInstanceLock() {
		return
	}
	mygo.App.WhenReady(func() {
		mygo.Theme.SetSource(mygo.ThemeDark)
		var window *mygo.Window
		app := desktop.NewLauncher(desktop.LauncherOptions{Demo: *demo}, mygo.RunOnMain, func() { window.Invalidate() })
		window = mygo.NewWindow(mygo.WindowOptions{
			Title: "Google Chat Max", Width: 1180, Height: 780,
			MinWidth: 760, MinHeight: 520, Content: ui.View(app.View),
		})
		mygo.App.OnSecondInstance(func([]string, string) { window.Show(); window.Focus() })
		window.OnFocus(func() { app.SetFocused(true) })
		window.OnBlur(func() { app.SetFocused(false) })
		window.OnHide(func() { app.SetFocused(false) })
		app.SetFocused(window.IsFocused())
		window.OnClose(func(event *mygo.CloseEvent) {
			event.PreventDefault()
			app.Shutdown(func(err error) {
				if err != nil {
					log.Print(err)
				}
				window.Destroy()
				mygo.App.Quit()
			})
		})
		app.Start()
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}
