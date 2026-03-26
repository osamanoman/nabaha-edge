// Package sip provides a SIP server using sipgo that accepts calls from PBX.
// Handles INVITE/BYE/ACK and manages RTP audio streams.
package sip

import (
	"context"
	"fmt"
	"log"
	"net"
	"sync"

	"github.com/emiago/sipgo"
	"github.com/emiago/sipgo/sip"
	"github.com/pion/sdp/v3"
)

// Call represents an active SIP call with RTP audio.
type Call struct {
	ID           string
	CallerNumber string
	RTPConn      *net.UDPConn // Local UDP socket for RTP
	RTPPort      int          // Local RTP port
	RemoteAddr   *net.UDPAddr // PBX's RTP address
	IncomingRTP  chan []byte  // RTP packets from PBX → bridge
	OutgoingRTP  chan []byte  // RTP packets from bridge → PBX
	done         chan struct{}
	once         sync.Once
}

func (c *Call) Close() {
	c.once.Do(func() {
		close(c.done)
		if c.RTPConn != nil {
			c.RTPConn.Close()
		}
	})
}

func (c *Call) Done() <-chan struct{} { return c.done }

// OnCallFunc is called when a new SIP call arrives.
type OnCallFunc func(call *Call)

// Server listens for SIP calls on the local network.
type Server struct {
	port    int
	localIP string
	onCall  OnCallFunc
	ua      *sipgo.UserAgent
	srv     *sipgo.Server
	calls   sync.Map // callID → *Call
}

// NewServer creates a SIP server.
func NewServer(port int, onCall OnCallFunc) *Server {
	return &Server{
		port:    port,
		localIP: detectLocalIP(),
		onCall:  onCall,
	}
}

// Start begins listening for SIP on UDP.
func (s *Server) Start(ctx context.Context) error {
	ua, err := sipgo.NewUA(sipgo.WithUserAgent("NabahaEdge/1.0"))
	if err != nil {
		return fmt.Errorf("create UA: %w", err)
	}
	s.ua = ua

	srv, err := sipgo.NewServer(ua)
	if err != nil {
		return fmt.Errorf("create server: %w", err)
	}
	s.srv = srv

	srv.OnInvite(s.handleInvite)
	srv.OnBye(s.handleBye)
	srv.OnAck(func(req *sip.Request, tx sip.ServerTransaction) {})
	srv.OnCancel(s.handleCancel)
	srv.OnOptions(func(req *sip.Request, tx sip.ServerTransaction) {
		res := sip.NewResponseFromRequest(req, 200, "OK", nil)
		tx.Respond(res)
	})

	addr := fmt.Sprintf("0.0.0.0:%d", s.port)
	log.Printf("[sip] listening on %s (UDP), local IP: %s", addr, s.localIP)

	return srv.ListenAndServe(ctx, "udp", addr)
}

func (s *Server) handleInvite(req *sip.Request, tx sip.ServerTransaction) {
	callID := req.CallID().Value()
	log.Printf("[sip] INVITE from %s (call=%s)", req.From().Address.User, callID)

	// Send 100 Trying
	tx.Respond(sip.NewResponseFromRequest(req, 100, "Trying", nil))

	// Parse SDP from INVITE
	offerSDP := &sdp.SessionDescription{}
	if err := offerSDP.Unmarshal(req.Body()); err != nil {
		log.Printf("[sip] bad SDP: %v", err)
		tx.Respond(sip.NewResponseFromRequest(req, 400, "Bad SDP", nil))
		return
	}

	// Extract remote RTP endpoint
	var remoteIP string
	var remotePort int
	if offerSDP.ConnectionInformation != nil {
		remoteIP = offerSDP.ConnectionInformation.Address.Address
	}
	if len(offerSDP.MediaDescriptions) > 0 {
		remotePort = offerSDP.MediaDescriptions[0].MediaName.Port.Value
	}

	// Allocate local RTP port
	rtpConn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4zero, Port: 0})
	if err != nil {
		log.Printf("[sip] RTP alloc failed: %v", err)
		tx.Respond(sip.NewResponseFromRequest(req, 500, "RTP Error", nil))
		return
	}
	localRTPPort := rtpConn.LocalAddr().(*net.UDPAddr).Port

	// Create call
	call := &Call{
		ID:           callID,
		CallerNumber: req.From().Address.User,
		RTPConn:      rtpConn,
		RTPPort:      localRTPPort,
		RemoteAddr:   &net.UDPAddr{IP: net.ParseIP(remoteIP), Port: remotePort},
		IncomingRTP:  make(chan []byte, 200),
		OutgoingRTP:  make(chan []byte, 200),
		done:         make(chan struct{}),
	}
	s.calls.Store(callID, call)

	// Build SDP answer
	answerSDP := s.buildSDP(localRTPPort)

	// Send 200 OK with SDP
	res := sip.NewResponseFromRequest(req, 200, "OK", answerSDP)
	res.AppendHeader(sip.NewHeader("Content-Type", "application/sdp"))
	res.AppendHeader(&sip.ContactHeader{
		Address: sip.Uri{Host: s.localIP, Port: s.port},
	})
	if err := tx.Respond(res); err != nil {
		log.Printf("[sip] 200 OK failed: %v", err)
		call.Close()
		return
	}

	// Start RTP I/O goroutines
	go s.rtpReceiveLoop(call)
	go s.rtpSendLoop(call)

	// Notify bridge
	if s.onCall != nil {
		go s.onCall(call)
	}
}

func (s *Server) handleBye(req *sip.Request, tx sip.ServerTransaction) {
	callID := req.CallID().Value()
	log.Printf("[sip] BYE (call=%s)", callID)
	tx.Respond(sip.NewResponseFromRequest(req, 200, "OK", nil))

	if v, ok := s.calls.LoadAndDelete(callID); ok {
		v.(*Call).Close()
	}
}

func (s *Server) handleCancel(req *sip.Request, tx sip.ServerTransaction) {
	callID := req.CallID().Value()
	log.Printf("[sip] CANCEL (call=%s)", callID)
	tx.Respond(sip.NewResponseFromRequest(req, 200, "OK", nil))

	if v, ok := s.calls.LoadAndDelete(callID); ok {
		v.(*Call).Close()
	}
}

// rtpReceiveLoop reads RTP from PBX and pushes to IncomingRTP channel.
func (s *Server) rtpReceiveLoop(call *Call) {
	buf := make([]byte, 1500)
	for {
		select {
		case <-call.Done():
			return
		default:
		}
		n, _, err := call.RTPConn.ReadFromUDP(buf)
		if err != nil {
			select {
			case <-call.Done():
			default:
				log.Printf("[sip] RTP read error: %v", err)
			}
			return
		}
		pkt := make([]byte, n)
		copy(pkt, buf[:n])
		select {
		case call.IncomingRTP <- pkt:
		default: // drop if full
		}
	}
}

// rtpSendLoop reads from OutgoingRTP and sends to PBX.
func (s *Server) rtpSendLoop(call *Call) {
	for {
		select {
		case <-call.Done():
			return
		case pkt := <-call.OutgoingRTP:
			if call.RemoteAddr != nil {
				call.RTPConn.WriteToUDP(pkt, call.RemoteAddr)
			}
		}
	}
}

func (s *Server) buildSDP(rtpPort int) []byte {
	answer := sdp.SessionDescription{
		Version: 0,
		Origin: sdp.Origin{
			Username: "-", SessionID: 1, SessionVersion: 1,
			NetworkType: "IN", AddressType: "IP4", UnicastAddress: s.localIP,
		},
		SessionName: "NabahaEdge",
		ConnectionInformation: &sdp.ConnectionInformation{
			NetworkType: "IN", AddressType: "IP4",
			Address: &sdp.Address{Address: s.localIP},
		},
		TimeDescriptions: []sdp.TimeDescription{
			{Timing: sdp.Timing{StartTime: 0, StopTime: 0}},
		},
		MediaDescriptions: []*sdp.MediaDescription{
			{
				MediaName: sdp.MediaName{
					Media: "audio", Port: sdp.RangedPort{Value: rtpPort},
					Protos: []string{"RTP", "AVP"}, Formats: []string{"0", "8"},
				},
				Attributes: []sdp.Attribute{
					{Key: "rtpmap", Value: "0 PCMU/8000"},
					{Key: "rtpmap", Value: "8 PCMA/8000"},
					{Key: "ptime", Value: "20"},
					{Key: "sendrecv"},
				},
			},
		},
	}
	raw, _ := answer.Marshal()
	return raw
}

func detectLocalIP() string {
	conn, err := net.Dial("udp", "8.8.8.8:80")
	if err != nil {
		return "127.0.0.1"
	}
	defer conn.Close()
	return conn.LocalAddr().(*net.UDPAddr).IP.String()
}
