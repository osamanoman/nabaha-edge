// Package heartbeat sends periodic health checks to Nabaha Cloud
// and receives config updates (subscription status, version info).
package heartbeat

import (
	"log"
	"net"
	"time"

	"github.com/osamanoman/nabaha-edge/internal/config"
)

const (
	Interval = 60 * time.Second
	Version  = "1.0.0"
)

// Start begins the heartbeat loop. Blocks forever.
func Start(token, apiBase string, stopCh <-chan struct{}) {
	localIP := detectLocalIP()
	log.Printf("[heartbeat] starting (interval=%s, ip=%s)", Interval, localIP)

	// Send first heartbeat immediately
	sendOnce(token, apiBase, localIP)

	ticker := time.NewTicker(Interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			sendOnce(token, apiBase, localIP)
		case <-stopCh:
			log.Println("[heartbeat] stopped")
			return
		}
	}
}

func sendOnce(token, apiBase, localIP string) {
	resp, err := config.SendHeartbeat(token, apiBase, localIP, Version, 0)
	if err != nil {
		log.Printf("[heartbeat] error: %v", err)
		return
	}
	if !resp.CallsAllowed {
		log.Println("[heartbeat] WARNING: subscription exhausted — new calls will be rejected")
	}
}

// detectLocalIP finds the machine's LAN IP address.
func detectLocalIP() string {
	conn, err := net.Dial("udp", "8.8.8.8:80")
	if err != nil {
		return "unknown"
	}
	defer conn.Close()
	return conn.LocalAddr().(*net.UDPAddr).IP.String()
}
