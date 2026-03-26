// Nabaha Edge — Local SIP-to-WebRTC bridge for PBX integration.
//
// Single binary, works on Windows/Linux/Mac. No external dependencies.
// Accepts SIP from PBX on LAN, tunnels audio to Nabaha Cloud via WebRTC.
//
// Usage:
//
//	nabaha-edge --token nt_xxxxx
//	nabaha-edge setup --token nt_xxxxx
//	NABAHA_EDGE_TOKEN=nt_xxxxx nabaha-edge
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/osamanoman/nabaha-edge/internal/bridge"
	"github.com/osamanoman/nabaha-edge/internal/config"
	"github.com/osamanoman/nabaha-edge/internal/heartbeat"
)

func main() {
	log.SetFlags(log.Ldate | log.Ltime | log.Lmsgprefix)
	log.SetPrefix("[nabaha-edge] ")

	token := os.Getenv("NABAHA_EDGE_TOKEN")
	apiBase := os.Getenv("NABAHA_API_BASE")
	isSetup := false

	for i, arg := range os.Args[1:] {
		switch arg {
		case "--token":
			if i+2 < len(os.Args) {
				token = os.Args[i+2]
			}
		case "--api-base":
			if i+2 < len(os.Args) {
				apiBase = os.Args[i+2]
			}
		case "setup":
			isSetup = true
		}
	}

	// Try loading from saved config
	if token == "" {
		if cfg, err := config.Load(); err == nil {
			token = cfg.Token
			if apiBase == "" {
				apiBase = cfg.APIBase
			}
		}
	}
	if apiBase == "" {
		apiBase = config.DefaultAPIBase
	}

	if token == "" {
		fmt.Println("Nabaha Edge — Local SIP Bridge")
		fmt.Println()
		fmt.Println("Usage:")
		fmt.Println("  nabaha-edge --token nt_xxxxx")
		fmt.Println("  nabaha-edge setup --token nt_xxxxx")
		fmt.Println("  NABAHA_EDGE_TOKEN=nt_xxxxx nabaha-edge")
		fmt.Println()
		fmt.Println("Get your token from:")
		fmt.Println("  https://nabaha.otekit.com/dashboard/integrations")
		os.Exit(1)
	}

	// Setup mode: save config and exit
	if isSetup {
		cfg := &config.EdgeConfig{Token: token, APIBase: apiBase}
		if err := config.Save(cfg); err != nil {
			log.Fatalf("Failed to save config: %v", err)
		}
		fmt.Printf("Config saved to %s\n", config.ConfigDir())
		fmt.Println("Run 'nabaha-edge' to start.")
		return
	}

	// Save config for future runs
	config.Save(&config.EdgeConfig{Token: token, APIBase: apiBase})

	// --- Startup ---
	log.Println("starting...")
	log.Printf("token: %s****%s", token[:7], token[len(token)-4:])
	log.Printf("api: %s", apiBase)

	// Fetch remote config
	log.Println("fetching config from Nabaha Cloud...")
	remoteCfg, err := config.FetchRemoteConfig(token, apiBase)
	if err != nil {
		log.Fatalf("Failed to fetch config: %v", err)
	}
	log.Printf("LiveKit: %s", remoteCfg.LiveKitURL)
	log.Printf("SIP port: %d", remoteCfg.SIPPort)
	log.Printf("calls allowed: %v", remoteCfg.CallsAllowed)

	// Start heartbeat in background
	stopHeartbeat := make(chan struct{})
	go heartbeat.Start(token, apiBase, stopHeartbeat)

	// Start SIP→WebRTC bridge
	ctx, cancel := context.WithCancel(context.Background())
	b := bridge.New(remoteCfg, token, apiBase)

	go func() {
		if err := b.Start(ctx); err != nil {
			log.Printf("bridge error: %v", err)
		}
	}()

	log.Println("ready — accepting SIP calls on port 5060")
	log.Println("press Ctrl+C to stop")

	// Wait for shutdown
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	log.Println("shutting down...")
	cancel()
	close(stopHeartbeat)
}
