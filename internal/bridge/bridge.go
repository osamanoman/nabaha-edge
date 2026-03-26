// Package bridge orchestrates SIP ↔ WebRTC audio bridging.
// For each SIP call, it creates a LiveKit room, joins as a participant,
// and pipes audio bidirectionally between SIP RTP and LiveKit WebRTC.
package bridge

import (
	"context"
	"fmt"
	"log"
	"sync/atomic"
	"time"

	"github.com/osamanoman/nabaha-edge/internal/config"
	sipserver "github.com/osamanoman/nabaha-edge/internal/sip"
	"github.com/osamanoman/nabaha-edge/internal/webrtc"
)

// Bridge manages the SIP server and creates WebRTC connections per call.
type Bridge struct {
	cfg       *config.RemoteConfig
	token     string
	apiBase   string
	sipServer *sipserver.Server
	callCount atomic.Int32
}

// New creates a new Bridge with the given remote config.
func New(cfg *config.RemoteConfig, token, apiBase string) *Bridge {
	b := &Bridge{
		cfg:     cfg,
		token:   token,
		apiBase: apiBase,
	}
	b.sipServer = sipserver.NewServer(cfg.SIPPort, b.onNewCall)
	return b
}

// Start begins the SIP server and blocks until ctx is cancelled.
func (b *Bridge) Start(ctx context.Context) error {
	log.Printf("[bridge] starting SIP→WebRTC bridge on port %d", b.cfg.SIPPort)
	return b.sipServer.Start(ctx)
}

// ActiveCalls returns the number of currently active calls.
func (b *Bridge) ActiveCalls() int {
	return int(b.callCount.Load())
}

// onNewCall is called by the SIP server when a new call arrives.
// It creates a LiveKit room, joins it, and bridges audio.
func (b *Bridge) onNewCall(call *sipserver.Call) {
	b.callCount.Add(1)
	defer b.callCount.Add(-1)

	callStart := time.Now()
	roomName := fmt.Sprintf("pbx-%s-%d", call.ID[:8], time.Now().UnixMilli())

	log.Printf("[bridge] new call: %s from %s → room %s", call.ID, call.CallerNumber, roomName)

	// Report call started
	config.ReportCallEvent(b.token, b.apiBase, "call_started", roomName, 0, call.CallerNumber)

	// Join LiveKit room
	rc, err := webrtc.JoinRoom(
		b.cfg.LiveKitURL,
		b.cfg.LiveKitAPIKey,
		b.cfg.LiveKitAPISecret,
		roomName,
		fmt.Sprintf("pbx-%s", call.CallerNumber),
		b.cfg.RoomMetadata,
	)
	if err != nil {
		log.Printf("[bridge] failed to join room: %v", err)
		call.Close()
		return
	}
	defer rc.Close()

	log.Printf("[bridge] call %s bridged to room %s", call.ID[:8], roomName)

	// Bridge audio bidirectionally until call ends
	done := make(chan struct{})

	// SIP → LiveKit: read RTP from PBX, write to LiveKit track
	go func() {
		defer func() { done <- struct{}{} }()
		for {
			select {
			case <-call.Done():
				return
			case pkt, ok := <-call.IncomingRTP:
				if !ok {
					return
				}
				if err := rc.WriteRTP(pkt); err != nil {
					return
				}
			}
		}
	}()

	// LiveKit → SIP: read agent audio, send as RTP to PBX
	go func() {
		defer func() { done <- struct{}{} }()
		for {
			select {
			case <-call.Done():
				return
			case pkt, ok := <-rc.AgentAudio:
				if !ok {
					return
				}
				select {
				case call.OutgoingRTP <- pkt:
				default:
				}
			}
		}
	}()

	// Wait for either direction to end
	<-done

	// Calculate duration and report
	duration := int(time.Since(callStart).Seconds())
	log.Printf("[bridge] call %s ended (duration=%ds)", call.ID[:8], duration)

	config.ReportCallEvent(b.token, b.apiBase, "call_ended", roomName, duration, call.CallerNumber)

	// Clean up
	call.Close()
}
