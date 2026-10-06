// Command kumo is the Kumo anime server. It serves the API and the web UI on
// 127.0.0.1 (and optionally your LAN) and is started by the desktop app.
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/simo1337s/animetest/server/internal/api"
	"github.com/simo1337s/animetest/server/internal/app"
	"github.com/simo1337s/animetest/server/internal/config"
	"github.com/simo1337s/animetest/server/internal/webui"
)

func main() {
	var (
		dataDir = flag.String("data-dir", config.DataDir(), "where Kumo stores its database")
		port    = flag.Int("port", 0, "override the port (default from settings, 43211)")
		webUI   = flag.Bool("web-ui", false, "force-enable the browser web UI for this run")
		desktop = flag.Bool("desktop", false, "started by the desktop app")
		version = flag.Bool("version", false, "print the version")
	)
	flag.Parse()
	if *version {
		fmt.Println(config.AppName, config.AppVersion)
		return
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
	log.Printf("%s %s ready — data: %s — shell token: %s", config.AppName, config.AppVersion, *dataDir, tokenPath)
	if srv.ForceWebUI || cfg.Server.WebUI {
		log.Printf("Web UI: http://127.0.0.1:%d", cfg.Server.Port)
	}

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	<-sig
	log.Printf("shutting down…")
	srv.Shutdown()
	a.Shutdown()
	_ = os.Remove(tokenPath)
}
