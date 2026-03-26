// Package config handles Edge device configuration — token management,
// API communication with Nabaha Cloud, and local config persistence.
package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"time"
)

const (
	DefaultAPIBase  = "https://nabaha.otekit.com"
	DefaultSIPPort  = 5060
	ConfigFileName  = "config.yaml"
	DefaultTimeout  = 10 * time.Second
)

// EdgeConfig is the local config stored on disk.
type EdgeConfig struct {
	Token   string `json:"token"`
	APIBase string `json:"api_base"`
}

// RemoteConfig is what we receive from the Nabaha API.
type RemoteConfig struct {
	LiveKitURL       string `json:"livekit_url"`
	LiveKitAPIKey    string `json:"livekit_api_key"`
	LiveKitAPISecret string `json:"livekit_api_secret"`
	SIPPort          int    `json:"sip_port"`
	RoomMetadata     string `json:"room_metadata_template"`
	AgentName        string `json:"agent_name"`
	CallsAllowed     bool   `json:"calls_allowed"`
	LatestVersion    string `json:"latest_version"`
}

// HeartbeatResponse is what the API returns on heartbeat.
type HeartbeatResponse struct {
	CallsAllowed  bool   `json:"calls_allowed"`
	LatestVersion string `json:"latest_version"`
	AgentID       string `json:"agent_id"`
}

// ConfigDir returns the platform-specific config directory.
func ConfigDir() string {
	switch runtime.GOOS {
	case "windows":
		return filepath.Join(os.Getenv("APPDATA"), "NabahaEdge")
	case "darwin":
		home, _ := os.UserHomeDir()
		return filepath.Join(home, ".nabaha-edge")
	default: // linux
		return "/etc/nabaha-edge"
	}
}

// Load reads the local config file.
func Load() (*EdgeConfig, error) {
	path := filepath.Join(ConfigDir(), ConfigFileName)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("config not found at %s: %w", path, err)
	}
	var cfg EdgeConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}
	if cfg.APIBase == "" {
		cfg.APIBase = DefaultAPIBase
	}
	return &cfg, nil
}

// Save writes the local config file.
func Save(cfg *EdgeConfig) error {
	dir := ConfigDir()
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	data, _ := json.MarshalIndent(cfg, "", "  ")
	return os.WriteFile(filepath.Join(dir, ConfigFileName), data, 0600)
}

// FetchRemoteConfig calls POST /api/v1/edge/config to get LiveKit credentials.
func FetchRemoteConfig(token, apiBase string) (*RemoteConfig, error) {
	body, _ := json.Marshal(map[string]string{"edge_token": token})
	req, err := http.NewRequest("POST", apiBase+"/api/v1/edge/config", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: DefaultTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to contact Nabaha API: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == 401 {
		return nil, fmt.Errorf("invalid edge token — check your token or regenerate from dashboard")
	}
	if resp.StatusCode != 200 {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("API error %d: %s", resp.StatusCode, string(respBody))
	}

	var cfg RemoteConfig
	if err := json.NewDecoder(resp.Body).Decode(&cfg); err != nil {
		return nil, fmt.Errorf("invalid API response: %w", err)
	}
	return &cfg, nil
}

// SendHeartbeat sends POST /api/v1/edge/heartbeat.
func SendHeartbeat(token, apiBase, localIP, version string, activeCalls int) (*HeartbeatResponse, error) {
	body, _ := json.Marshal(map[string]interface{}{
		"edge_token":   token,
		"local_ip":     localIP,
		"version":      version,
		"active_calls": activeCalls,
	})
	req, err := http.NewRequest("POST", apiBase+"/api/v1/edge/heartbeat", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: DefaultTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == 401 {
		return nil, fmt.Errorf("edge token revoked")
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("heartbeat failed: %d", resp.StatusCode)
	}

	var result HeartbeatResponse
	json.NewDecoder(resp.Body).Decode(&result)
	return &result, nil
}

// ReportCallEvent sends POST /api/v1/edge/call-event.
func ReportCallEvent(token, apiBase, event, roomName string, durationSeconds int, phoneNumber string) error {
	body, _ := json.Marshal(map[string]interface{}{
		"edge_token":       token,
		"event":            event,
		"room_name":        roomName,
		"duration_seconds": durationSeconds,
		"phone_number":     phoneNumber,
	})
	req, err := http.NewRequest("POST", apiBase+"/api/v1/edge/call-event", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: DefaultTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return fmt.Errorf("call event failed: %d", resp.StatusCode)
	}
	return nil
}
