// Package sip manages the local LiveKit SIP server process.
// It generates the config file and starts/stops the livekit-sip binary.
package sip

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"text/template"

	"github.com/osamanoman/nabaha-edge/internal/config"
)

const sipConfigTemplate = `api_key: {{.APIKey}}
api_secret: {{.APISecret}}
ws_url: {{.WSURL}}
redis:
  address: localhost:6379
sip_port: {{.SIPPort}}
rtp_port: 10000-20000
use_external_ip: false
logging:
  level: info
  json: true
`

type sipConfigData struct {
	APIKey    string
	APISecret string
	WSURL     string
	SIPPort   int
}

// WriteSIPConfig generates the livekit-sip config file from remote config.
func WriteSIPConfig(remoteCfg *config.RemoteConfig) (string, error) {
	dir := config.ConfigDir()
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", err
	}

	path := filepath.Join(dir, "sip.yaml")
	f, err := os.Create(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	tmpl, err := template.New("sip").Parse(sipConfigTemplate)
	if err != nil {
		return "", err
	}

	data := sipConfigData{
		APIKey:    remoteCfg.LiveKitAPIKey,
		APISecret: remoteCfg.LiveKitAPISecret,
		WSURL:     remoteCfg.LiveKitURL,
		SIPPort:   remoteCfg.SIPPort,
	}

	if err := tmpl.Execute(f, data); err != nil {
		return "", err
	}

	log.Printf("[sip] config written to %s", path)
	return path, nil
}

// FindSIPBinary locates the livekit-sip binary.
func FindSIPBinary() (string, error) {
	// Check common locations
	candidates := []string{
		"livekit-sip",                                    // In PATH
		"/usr/local/bin/livekit-sip",                     // Linux standard
		"/usr/bin/livekit-sip",                           // Linux standard
		filepath.Join(config.ConfigDir(), "livekit-sip"), // Same dir as config
	}

	if runtime.GOOS == "windows" {
		candidates = append(candidates,
			`C:\Program Files\NabahaEdge\livekit-sip.exe`,
			filepath.Join(config.ConfigDir(), "livekit-sip.exe"),
		)
	}

	// Check next to the current executable
	exe, err := os.Executable()
	if err == nil {
		candidates = append([]string{filepath.Join(filepath.Dir(exe), "livekit-sip")}, candidates...)
	}

	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c, nil
		}
		// Try with PATH lookup
		if p, err := exec.LookPath(c); err == nil {
			return p, nil
		}
	}

	return "", fmt.Errorf("livekit-sip binary not found — install it alongside nabaha-edge")
}

// StartSIPServer launches the livekit-sip process. Returns the process.
func StartSIPServer(binaryPath, configPath string) (*exec.Cmd, error) {
	cmd := exec.Command(binaryPath, "--config", configPath)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("failed to start livekit-sip: %w", err)
	}

	log.Printf("[sip] livekit-sip started (pid=%d)", cmd.Process.Pid)
	return cmd, nil
}
