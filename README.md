# Nabaha Edge — Local SIP Bridge

Runs on your local network. Accepts SIP calls from your PBX and tunnels them to Nabaha's AI voice agent via secure WebSocket.

## Quick Start (Docker)

```bash
docker run -d --name nabaha-edge \
  --restart=always \
  --network=host \
  -e NABAHA_EDGE_TOKEN=nt_xxxxx \
  ghcr.io/osamanoman/nabaha-edge:latest
```

## Quick Start (Native)

```bash
# Linux
sudo dpkg -i nabaha-edge_1.0_amd64.deb
sudo nabaha-edge setup --token nt_xxxxx
sudo systemctl start nabaha-edge

# Windows
# Run nabaha-edge-setup.exe, enter your token
```

## PBX Setup

Point your PBX SIP trunk to this machine's local IP on port 5060:
- **SIP Server**: `192.168.x.x` (this machine)
- **Port**: 5060
- **Transport**: UDP or TCP
- **No credentials needed** (LAN trust)

## How It Works

1. PBX sends SIP call to Edge (on LAN — no NAT/firewall issues)
2. Edge tunnels to Nabaha Cloud via WSS (port 443 — never blocked)
3. Nabaha's AI agent handles the call (STT → LLM → TTS)
4. Audio flows back through the same path

## Get Your Token

Visit: https://nabaha.otekit.com/dashboard/integrations → "Nabaha Edge" section
