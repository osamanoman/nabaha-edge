// Package webrtc provides a LiveKit room client that publishes SIP audio
// and receives agent audio using the LiveKit Go SDK.
package webrtc

import (
	"fmt"
	"log"
	"sync"

	lksdk "github.com/livekit/server-sdk-go/v2"
	"github.com/livekit/protocol/livekit"
	"github.com/pion/rtp"
	pwebrtc "github.com/pion/webrtc/v4"
)

// RoomClient manages one LiveKit room connection for one SIP call.
type RoomClient struct {
	room       *lksdk.Room
	audioTrack *lksdk.LocalSampleTrack

	// AgentAudio receives raw RTP packets from the voice agent's audio track.
	// The bridge reads from this channel and sends to the PBX via SIP RTP.
	AgentAudio chan []byte

	mu     sync.Mutex
	closed bool
}

// JoinRoom connects to a LiveKit room, publishes a PCMU audio track,
// and subscribes to the voice agent's audio.
func JoinRoom(url, apiKey, apiSecret, roomName, identity, metadata string) (*RoomClient, error) {
	rc := &RoomClient{
		AgentAudio: make(chan []byte, 200),
	}

	room, err := lksdk.ConnectToRoom(url, lksdk.ConnectInfo{
		APIKey:              apiKey,
		APISecret:           apiSecret,
		RoomName:            roomName,
		ParticipantIdentity: identity,
		ParticipantMetadata: metadata,
	}, &lksdk.RoomCallback{
		ParticipantCallback: lksdk.ParticipantCallback{
			OnTrackSubscribed: func(track *pwebrtc.TrackRemote, pub *lksdk.RemoteTrackPublication, p *lksdk.RemoteParticipant) {
				if track.Kind() == pwebrtc.RTPCodecTypeAudio {
					log.Printf("[webrtc] subscribed to audio from %s (codec=%s)", p.Identity(), track.Codec().MimeType)
					go rc.readAgentTrack(track)
				}
			},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("connect to room: %w", err)
	}
	rc.room = room

	// Create a PCMU local track — same codec as SIP, zero transcoding
	track, err := lksdk.NewLocalSampleTrack(pwebrtc.RTPCodecCapability{
		MimeType:  pwebrtc.MimeTypePCMU,
		ClockRate: 8000,
		Channels:  1,
	})
	if err != nil {
		room.Disconnect()
		return nil, fmt.Errorf("create track: %w", err)
	}

	if _, err := room.LocalParticipant.PublishTrack(track, &lksdk.TrackPublicationOptions{
		Name:   "sip-audio",
		Source: livekit.TrackSource_MICROPHONE,
	}); err != nil {
		room.Disconnect()
		return nil, fmt.Errorf("publish track: %w", err)
	}

	rc.audioTrack = track
	log.Printf("[webrtc] joined room %s as %s", roomName, identity)
	return rc, nil
}

// WriteRTP sends a raw RTP packet (from SIP) into the LiveKit room.
// The packet is written directly to the published PCMU track — zero transcoding.
func (rc *RoomClient) WriteRTP(raw []byte) error {
	rc.mu.Lock()
	if rc.closed {
		rc.mu.Unlock()
		return fmt.Errorf("closed")
	}
	rc.mu.Unlock()

	pkt := &rtp.Packet{}
	if err := pkt.Unmarshal(raw); err != nil {
		return nil // skip malformed
	}
	return rc.audioTrack.WriteRTP(pkt, nil)
}

// readAgentTrack reads RTP from the agent's audio track and pushes to AgentAudio.
func (rc *RoomClient) readAgentTrack(track *pwebrtc.TrackRemote) {
	buf := make([]byte, 1500)
	for {
		n, _, err := track.Read(buf)
		if err != nil {
			rc.mu.Lock()
			closed := rc.closed
			rc.mu.Unlock()
			if !closed {
				log.Printf("[webrtc] track read error: %v", err)
			}
			return
		}
		pkt := make([]byte, n)
		copy(pkt, buf[:n])
		select {
		case rc.AgentAudio <- pkt:
		default: // drop if full
		}
	}
}

// Close disconnects from the LiveKit room.
func (rc *RoomClient) Close() {
	rc.mu.Lock()
	rc.closed = true
	rc.mu.Unlock()

	if rc.room != nil {
		rc.room.Disconnect()
	}
	log.Println("[webrtc] disconnected")
}
