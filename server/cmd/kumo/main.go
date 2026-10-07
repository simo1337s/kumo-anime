// Command kumo is the Kumo anime server. It serves the API and the web UI on
// 127.0.0.1 (and optionally your LAN) and is started by the desktop app.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/simo1337s/animetest/server/internal/api"
	"github.com/simo1337s/animetest/server/internal/app"
	"github.com/simo1337s/animetest/server/internal/config"
	"github.com/simo1337s/animetest/server/internal/lifecycle"
	"github.com/simo1337s/animetest/server/internal/util"
	"github.com/simo1337s/animetest/server/internal/webui"
)

func main() {
	var (
		dataDir = flag.String("data-dir", config.DataDir(), "where Kumo stores its database")
		port    = flag.Int("port", 0, "override the port (default from settings, 43211)")
		webUI   = flag.Bool("web-ui", false, "force-enable the browser web UI for this run")
		desktop = flag.Bool("desktop", false, "started by the desktop app")
		version = flag.Bool("version", false, "print the version")
		// Windows has no signals to ask a program to stop: the desktop app
		// closes the server's input instead.
		exitWithStdin = flag.Bool("exit-with-stdin", false, "stop when standard input closes")
	)
	flag.Parse()
	if *version {
		fmt.Println(config.AppName, config.AppVersion)
		return
	}
	// Whatever Kumo starts (ffmpeg, ani-cli, mpv) ends with it, even when
	// it's killed.
	if err := util.BindChildren(); err != nil {
		log.Printf("child processes won't end with Kumo: %v", err)
	}
	if err := os.MkdirAll(*dataDir, 0o700); err != nil {
		log.Fatal(err)
	}
	a, err := app.New(*dataDir)
	if err != nil {
		log.Fatal(err)
	}
	if *port > 0 {
		cfg := a.Settings.Get()
		cfg.Server.Port = *port
		if _, err := a.Settings.Save(cfg); err != nil {
			log.Fatal(err)
		}
	}
	tokenPath := a.WriteShellToken()
	srv := api.New(a, webui.FS())
	// Without the desktop app nothing could reach the server if the web UI
	// were off, so headless runs always serve it (on 127.0.0.1 by default).
	srv.ForceWebUI = *webUI || !*desktop
	if err := srv.Start(); err != nil {
		log.Fatalf("could not start the server: %v (is Kumo already running?)", err)
	}
	a.Start()
	cfg := a.Settings.Get()
	// Lets the desktop app find an already running server.
	infoPath := filepath.Join(config.RuntimeDir(), "server.json")
	writeInfo := func() {
		raw, _ := json.Marshal(map[string]any{"pid": os.Getpid(), "port": a.Settings.Get().Server.Port, "addr": srv.Addr(), "tokenFile": tokenPath})
		_ = os.WriteFile(infoPath, raw, 0o600)
	}
	writeInfo()
	a.Settings.OnChange(func(_, _ config.Settings) { writeInfo() })
	log.Printf("%s %s ready — data: %s — shell token: %s", config.AppName, config.AppVersion, *dataDir, tokenPath)
	if srv.ForceWebUI || cfg.Server.WebUI {
		log.Printf("Web UI: http://127.0.0.1:%d", cfg.Server.Port)
	}

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	if *exitWithStdin {
		go func() {
			_, _ = io.Copy(io.Discard, os.Stdin)
			sig <- syscall.SIGTERM
		}()
	}
	// A signal, or a request from inside Kumo (the updater restarting it
	// into a new version): both shut down the same way.
	var exit lifecycle.Exit
	select {
	case <-sig:
	case exit = <-a.Exits.C():
	}
	log.Printf("shutting down…")
	srv.Shutdown()
	a.Shutdown()
	_ = os.Remove(tokenPath)
	_ = os.Remove(infoPath)
	end(exit, *desktop)
}

// end finishes the process the way exit asks, once Kumo has shut down.
func end(exit lifecycle.Exit, desktop bool) {
	restart := exit.Restart
	if exit.Before != nil {
		if err := exit.Before(); err != nil {
			log.Printf("%v; starting Kumo again", err)
			restart = true
		}
	}
	if !restart {
		if exit.Code != 0 {
			os.Exit(exit.Code)
		}
		return
	}
	// The desktop app started this server: it starts everything again, its
	// window included. Otherwise (a systemd service, a terminal) the server
	// runs itself again, in place, where the system can.
	if !desktop && canReexec {
		err := reexec()
		log.Printf("could not start Kumo again: %v", err)
	}
	os.Exit(lifecycle.CodeRestart)
}
