#!/usr/bin/env bash
# setup.sh — Set up DownloadOnce on a Debian Trixie system (or LXC container).
# Run as root inside the target machine. Assumes the pre-built binary is
# already present next to this script (scp it in alongside this file).
#
# Quick start (from your dev machine):
#   CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o downloadonce ./cmd/server
#   scp downloadonce setup.sh root@<host>:~
#   ssh root@<host> 'BASE_URL=https://dl.example.com ./setup.sh'
set -euo pipefail

###############################################################################
# Configuration — override via environment or edit here
###############################################################################
BASE_URL="${BASE_URL:-http://localhost:8080}"
SESSION_SECRET="${SESSION_SECRET:-$(openssl rand -hex 32)}"
WORKER_COUNT="${WORKER_COUNT:-2}"
LOG_LEVEL="${LOG_LEVEL:-info}"
DATA_DIR="${DATA_DIR:-/data}"
LISTEN_ADDR="${LISTEN_ADDR:-:8080}"

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
BINARY="$SCRIPT_DIR/downloadonce"

###############################################################################
# Helpers
###############################################################################
info() { printf '\033[1;34m==>\033[0m %s\n' "$*"; }
die()  { printf '\033[1;31m==> ERROR:\033[0m %s\n' "$*" >&2; exit 1; }

###############################################################################
# Pre-flight
###############################################################################
[[ $EUID -eq 0 ]] || die "Run this script as root"
[[ -f "$BINARY" ]] || die "Binary not found at $BINARY — build it first and place it next to this script"

###############################################################################
# 1. System packages
###############################################################################
info "Installing system packages..."
apt-get update -qq
DEBIAN_FRONTEND=noninteractive apt-get install -y -qq \
    ffmpeg \
    imagemagick \
    fonts-dejavu-core \
    ca-certificates \
    python3 \
    python3-pip \
    python3-venv \
    tesseract-ocr

###############################################################################
# 2. Python virtualenv
###############################################################################
info "Setting up Python venv at /opt/venv..."
python3 -m venv /opt/venv
/opt/venv/bin/pip install --no-cache-dir -q \
    invisible-watermark opencv-python-headless

###############################################################################
# 3. Install binary
###############################################################################
info "Installing binary..."
install -m 0755 "$BINARY" /usr/local/bin/downloadonce

###############################################################################
# 4. Data directory
###############################################################################
mkdir -p "$DATA_DIR"

###############################################################################
# 5. Systemd service
###############################################################################
info "Writing systemd unit..."
cat > /etc/systemd/system/downloadonce.service <<EOF
[Unit]
Description=DownloadOnce
After=network.target

[Service]
Type=simple
ExecStart=/usr/local/bin/downloadonce
Restart=on-failure
RestartSec=5

Environment=DATA_DIR=$DATA_DIR
Environment=LISTEN_ADDR=$LISTEN_ADDR
Environment=BASE_URL=$BASE_URL
Environment=SESSION_SECRET=$SESSION_SECRET
Environment=WORKER_COUNT=$WORKER_COUNT
Environment=FONT_PATH=/usr/share/fonts/truetype/dejavu/DejaVuSans.ttf
Environment=VENV_PATH=/opt/venv
Environment=LOG_LEVEL=$LOG_LEVEL

[Install]
WantedBy=multi-user.target
EOF

###############################################################################
# 6. Start
###############################################################################
info "Enabling and starting service..."
systemctl daemon-reload
systemctl enable --now downloadonce

sleep 2
if systemctl is-active --quiet downloadonce; then
    info "Service is running."
else
    die "Service failed to start. Check: journalctl -u downloadonce --no-pager"
fi

###############################################################################
# Summary
###############################################################################
IP=$(hostname -I | awk '{print $1}')
echo
info "Setup complete!"
echo "  URL:    http://${IP}${LISTEN_ADDR}"
echo "  Data:   $DATA_DIR"
echo "  Logs:   journalctl -u downloadonce -f"
echo
echo "  Session secret (save this): $SESSION_SECRET"
echo
