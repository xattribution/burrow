#!/usr/bin/env bash
# Burrow installer for any Docker host.
#   curl -fsSL https://raw.githubusercontent.com/xattribution/burrow/main/install.sh | sudo bash -s -- install /path/to/library
# Commands: install <library-path> | update | uninstall | backup | restore <file> | status
set -euo pipefail

REPO_RAW="https://raw.githubusercontent.com/xattribution/burrow/main"
OWNER="${SUDO_USER:-$(id -un)}"
HOME_DIR="$(getent passwd "$OWNER" | cut -d: -f6)"
DIR="${BURROW_DIR:-$HOME_DIR/burrow}"
cmd="${1:-help}"

need_root() { [ "$(id -u)" -eq 0 ] || { echo "Run with sudo."; exit 1; }; }
compose() { (cd "$DIR" && docker compose "$@"); }

case "$cmd" in
  install)
    need_root
    LIB="${2:-}"
    [ -n "$LIB" ] && [ -d "$LIB" ] || { echo "Usage: install /path/to/library   (the folder Burrow may index, mounted read-only)"; exit 1; }
    command -v docker >/dev/null || { echo "Docker is not installed."; exit 1; }
    mkdir -p "$DIR/state" "$DIR/drop" "$DIR/backups"
    curl -fsSL "$REPO_RAW/compose.yaml" -o "$DIR/compose.yaml"
    if [ ! -f "$DIR/.env" ]; then
      cat > "$DIR/.env" <<EOF
LIBRARY=$(realpath "$LIB")
DROP=$DIR/drop
PORT=47823
PUBLIC_PORT=47824
PUID=$(id -u "$OWNER")
PGID=$(id -g "$OWNER")
TZ=$(cat /etc/timezone 2>/dev/null || echo UTC)
EOF
    fi
    chown -R "$OWNER": "$DIR"
    compose pull && compose up -d
    ip=$(hostname -I 2>/dev/null | awk '{print $1}')
    echo "Burrow is running. Open https://${ip:-localhost}:$(grep ^PORT= "$DIR/.env" | cut -d= -f2) to finish setup (accept the self-signed certificate)."
    echo "The setup key is in the log: docker logs burrow"
    ;;
  update)
    need_root
    curl -fsSL "$REPO_RAW/compose.yaml" -o "$DIR/compose.yaml"
    compose pull && compose up -d && docker image prune -f >/dev/null
    echo "Updated."
    ;;
  uninstall)
    need_root
    compose down || true
    echo "Container removed. Your data was never touched. Settings, links and backups are still in $DIR; delete that folder to remove them."
    ;;
  backup)
    f="burrow-$(date +%Y%m%d-%H%M%S).db"
    docker exec burrow burrow backup "/state/$f"
    mkdir -p "$DIR/backups" && mv "$DIR/state/$f" "$DIR/backups/$f"
    echo "Backup saved: $DIR/backups/$f"
    ;;
  restore)
    need_root
    f="${2:-}"; [ -f "$f" ] || { echo "Usage: restore /path/to/burrow-backup.db"; exit 1; }
    compose stop
    cp "$DIR/state/burrow.db" "$DIR/state/burrow.db.before-restore" 2>/dev/null || true
    rm -f "$DIR/state/burrow.db-wal" "$DIR/state/burrow.db-shm"
    cp "$f" "$DIR/state/burrow.db" && chown "$OWNER": "$DIR/state/burrow.db"
    compose start
    echo "Restored from $f. The previous database is at $DIR/state/burrow.db.before-restore."
    ;;
  status)
    compose ps
    ;;
  *)
    sed -n '2,5p' "$0" 2>/dev/null || echo "Commands: install <library> | update | uninstall | backup | restore <file> | status"
    ;;
esac
