#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<EOF
Usage: $(basename "$0") [options] <user@host>

Deploy muxcored to a remote host via SSH.

Options:
  -r, --release <version>  Download a specific release from GitHub (e.g. v1.2.3).
                           If omitted, the locally-built binary is used.
  -c, --config <file>      Path to muxcore.json to deploy.
  -t, --tag <tag>          Spool tag passed as --tag to muxcored (default: default).
  -b, --binary <path>      Path to local muxcored binary (default: ./muxcored).
  -d, --deploy-dir <dir>   Remote directory for binary (default: /usr/local/bin).
  -s, --service <name>     Systemd service name on remote (default: muxcored).
  -u, --user <name>        Remote user for sudo (default: root unless user@host has one).
  -k, --keep-config        Don't overwrite remote config file if it exists.
  -n, --dry-run            Print what would be done without doing it.
  -q, --quiet              Suppress progress output.
  -h, --help               Show this help.

Examples:
  $(basename "$0") deploy@myserver
  $(basename "$0") -r v1.2.3 -c muxcore.json deploy@myserver
  $(basename "$0") --tag mytag -b ./muxcored deploy@myserver
EOF
  exit 0
}

# Defaults
RELEASE=""
CONFIG=""
TAG="default"
BINARY="./muxcored"
DEPLOY_DIR="/usr/local/bin"
SERVICE="muxcored"
REMOTE_USER=""
KEEP_CONFIG=false
DRY_RUN=false
QUIET=false

# Parse args
while [[ $# -gt 0 ]]; do
  case "$1" in
    -r|--release)     RELEASE="$2"; shift 2 ;;
    -c|--config)      CONFIG="$2"; shift 2 ;;
    -t|--tag)         TAG="$2"; shift 2 ;;
    -b|--binary)      BINARY="$2"; shift 2 ;;
    -d|--deploy-dir)  DEPLOY_DIR="$2"; shift 2 ;;
    -s|--service)     SERVICE="$2"; shift 2 ;;
    -u|--user)        REMOTE_USER="$2"; shift 2 ;;
    -k|--keep-config) KEEP_CONFIG=true; shift ;;
    -n|--dry-run)     DRY_RUN=true; shift ;;
    -q|--quiet)       QUIET=true; shift ;;
    -h|--help)        usage ;;
    --*)              echo "Unknown option: $1"; usage ;;
    *)                TARGET="$1"; shift ;;
  esac
done

# shellcheck disable=SC2317
log() { if ! $QUIET; then echo "$@"; fi; }

# Validate target
if [[ -z "${TARGET:-}" ]]; then
  echo "Error: target host is required."
  usage
fi

# Extract remote user from target if not explicitly set
if [[ -z "$REMOTE_USER" ]]; then
  if [[ "$TARGET" == *@* ]]; then
    REMOTE_USER="${TARGET%%@*}"
  else
    REMOTE_USER="root"
  fi
fi

SSH_TARGET="${REMOTE_USER}@${TARGET#*@}"

# Build or fetch the binary
if [[ -n "$RELEASE" ]]; then
  log "==> Downloading muxcored release $RELEASE..."
  ARCH="$(uname -m)"
  case "$ARCH" in
    x86_64|amd64) GOARCH="x86_64" ;;
    aarch64|arm64) GOARCH="arm64" ;;
    *) echo "Unsupported architecture: $ARCH"; exit 1 ;;
  esac
  URL="https://github.com/Muxcore-Media/core/releases/download/${RELEASE}/muxcored_${RELEASE#v}_Linux_${GOARCH}.tar.gz"
  TMP_DIR="$(mktemp -d)"
  # shellcheck disable=SC2317
  cleanup() { rm -rf "$TMP_DIR"; }
  trap cleanup EXIT
  curl -fsSL "$URL" | tar -xzf - -C "$TMP_DIR"
  BINARY="$TMP_DIR/muxcored"
  log "       Downloaded to $BINARY"
elif ! $DRY_RUN; then
  if [[ ! -f "$BINARY" ]]; then
    echo "Error: binary not found at $BINARY. Build it first (make build) or use --release."
    exit 1
  fi
  log "==> Using local binary: $BINARY"
fi

# Ensure binary is executable
if ! $DRY_RUN; then
  chmod +x "$BINARY"
fi

# Determine remote config path
REMOTE_CONFIG_PATH=""
if [[ -n "$CONFIG" ]]; then
  if [[ ! -f "$CONFIG" ]]; then
    echo "Error: config file not found at $CONFIG."
    exit 1
  fi
  REMOTE_CONFIG_PATH="/etc/muxcore/muxcore.json"
fi

# Detect remote init system and binary presence
log "==> Checking remote host $SSH_TARGET..."
if $DRY_RUN; then
  log "       (dry-run, skipping remote checks)"
else
  HAS_SYSTEMD=$(ssh "$SSH_TARGET" "command -v systemctl &>/dev/null && echo yes || echo no")
  HAS_SERVICE=$(ssh "$SSH_TARGET" "systemctl is-enabled $SERVICE &>/dev/null 2>&1 && echo yes || echo no" 2>/dev/null || echo "no")
  IS_ACTIVE=$(ssh "$SSH_TARGET" "systemctl is-active $SERVICE &>/dev/null 2>&1 && echo yes || echo no" 2>/dev/null || echo "no")
fi

# Execute deployment
log "==> Deploying to $SSH_TARGET..."

if $DRY_RUN; then
  log "       [DRY-RUN] scp $BINARY $SSH_TARGET:$DEPLOY_DIR/muxcored"
  if [[ -n "$REMOTE_CONFIG_PATH" ]]; then
    log "       [DRY-RUN] scp $CONFIG $SSH_TARGET:$REMOTE_CONFIG_PATH"
  fi
  log "       [DRY-RUN] ssh $SSH_TARGET systemctl restart $SERVICE"
  log "       [DRY-RUN] ssh $SSH_TARGET systemctl status $SERVICE"
  log "==> Dry-run complete. No changes made."
  exit 0
fi

# Ensure remote deploy dir exists
ssh "$SSH_TARGET" "sudo mkdir -p '$DEPLOY_DIR'"

# Stop the service if it's running
if [[ "$IS_ACTIVE" == "yes" ]]; then
  log "       Stopping $SERVICE..."
  ssh "$SSH_TARGET" "sudo systemctl stop '$SERVICE'"
fi

# Copy the binary
log "       Copying binary..."
scp -q "$BINARY" "$SSH_TARGET:/tmp/muxcored"
ssh "$SSH_TARGET" "sudo mv /tmp/muxcored '$DEPLOY_DIR/muxcored' && sudo chmod 755 '$DEPLOY_DIR/muxcored'"

# Copy config if provided
if [[ -n "$REMOTE_CONFIG_PATH" ]]; then
  REMOTE_CONFIG_DIR="$(dirname "$REMOTE_CONFIG_PATH")"
  if $KEEP_CONFIG && ssh "$SSH_TARGET" "test -f '$REMOTE_CONFIG_PATH'" &>/dev/null; then
    log "       Config exists at $REMOTE_CONFIG_PATH, keeping (--keep-config)"
  else
    log "       Copying config..."
    ssh "$SSH_TARGET" "sudo mkdir -p '$REMOTE_CONFIG_DIR'"
    scp -q "$CONFIG" "$SSH_TARGET:/tmp/muxcore.json"
    ssh "$SSH_TARGET" "sudo mv /tmp/muxcore.json '$REMOTE_CONFIG_PATH' && sudo chmod 644 '$REMOTE_CONFIG_PATH'"
  fi
fi

# Restart the service
if [[ "$HAS_SERVICE" == "yes" ]]; then
  log "       Restarting $SERVICE..."
  ssh "$SSH_TARGET" "sudo systemctl daemon-reload && sudo systemctl start '$SERVICE'"
elif $HAS_SYSTEMD; then
  log "       Service $SERVICE not installed. Starting binary directly..."
  ssh "$SSH_TARGET" "nohup sudo $DEPLOY_DIR/muxcored --tag '$TAG' &>/var/log/muxcored.log &"
else
  log "       Starting binary directly..."
  ssh "$SSH_TARGET" "nohup sudo $DEPLOY_DIR/muxcored --tag '$TAG' &>/var/log/muxcored.log &"
fi

# Wait for service to start and health check
log "       Waiting for health check..."
sleep 3
HEALTH_OK=false
for i in 1 2 3 4 5; do
  STATUS=$(ssh "$SSH_TARGET" "curl -sf http://127.0.0.1:8080/health -o /dev/null -w '%{http_code}' 2>/dev/null || echo 'failed'" 2>/dev/null || echo "failed")
  if [[ "$STATUS" == "200" ]]; then
    HEALTH_OK=true
    break
  fi
  sleep 2
done

if $HEALTH_OK; then
  log "==> Deploy complete. muxcored is healthy on $SSH_TARGET."
else
  log "==> Deploy complete. Health check did not return 200. Check remote logs:"
  log "       ssh $SSH_TARGET sudo journalctl -u $SERVICE --no-pager -n 50"
  log "       ssh $SSH_TARGET cat /var/log/muxcored.log"
fi
