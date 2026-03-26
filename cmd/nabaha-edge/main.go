// Nabaha Edge — Local SIP bridge for PBX integration.
//
// Runs on customer's LAN, accepts SIP from PBX, tunnels to Nabaha Cloud.
//
// Usage:
//
//	nabaha-edge --token nt_xxxxx          (run with token)
//	nabaha-edge setup --token nt_xxxxx    (save token to config)
//	NABAHA_EDGE_TOKEN=nt_xxxxx nabaha-edge (via env var)
package main

import (
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/osamanoman/nabaha-edge/internal/config"
	"github.com/osamanoman/nabaha-edge/internal/heartbeat"
	"github.com/osamanoman/nabaha-edge/internal/sip"
)

func main() {
	log.SetFlags(log.Ldate | log.Ltime | log.Lmsgprefix)
	log.SetPrefix("[nabaha-edge] ")

	// Parse token from env or args
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
		fmt.Println("╔══════════════════════════════════════════════╗")
		fmt.Println("║         Nabaha Edge — SIP Bridge             ║")
		fmt.Println("╚══════════════════════════════════════════════╝")
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
	preview := token[:7] + "****" + token[len(token)-4:]
	log.Printf("token: %s", preview)
	log.Printf("api: %s", apiBase)

	// Step 1: Fetch remote config
	log.Println("fetching config from Nabaha Cloud...")
	remoteCfg, err := config.FetchRemoteConfig(token, apiBase)
	if err != nil {
		log.Fatalf("Failed to fetch config: %v", err)
	}
	log.Printf("LiveKit: %s", remoteCfg.LiveKitURL)
	log.Printf("SIP port: %d", remoteCfg.SIPPort)
	log.Printf("calls allowed: %v", remoteCfg.CallsAllowed)

	// Step 2: Write SIP config
	sipConfigPath, err := sip.WriteSIPConfig(remoteCfg)
	if err != nil {
		log.Fatalf("Failed to write SIP config: %v", err)
	}

	// Step 3: Find and start LiveKit SIP binary
	sipBinary, err := sip.FindSIPBinary()
	if err != nil {
		log.Printf("WARNING: %v", err)
		log.Println("SIP bridge will not start — install livekit-sip binary")
		log.Println("Running in heartbeat-only mode...")
	} else {
		log.Printf("SIP binary: %s", sipBinary)
		cmd, err := sip.StartSIPServer(sipBinary, sipConfigPath)
		if err != nil {
			log.Fatalf("Failed to start SIP server: %v", err)
		}
		defer func() {
			if cmd.Process != nil {
				log.Println("stopping SIP server...")
				cmd.Process.Kill()
			}
		}()
	}

	// Step 4: Start heartbeat in background
	stopCh := make(chan struct{})
	go heartbeat.Start(token, apiBase, stopCh)

	// Step 5: Wait for shutdown signal
	log.Println("ready — accepting SIP calls on port 5060")
	log.Println("press Ctrl+C to stop")

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	log.Println("shutting down...")
	close(stopCh)
}
