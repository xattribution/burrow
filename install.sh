#!/usr/bin/env bash
# Burrow installer for any Docker host.
#   curl -fsSL https://raw.githubusercontent.com/xattribution/burrow/main/install.sh | sudo bash -s -- install /path/to/library
# Commands: install <library-path> | update [version] | uninstall | backup | restore <file> | status
set -euo pipefail

REPO_RAW="${BURROW_REPO_RAW:-https://raw.githubusercontent.com/xattribution/burrow/main}"
OWNER="${SUDO_USER:-$(id -un)}"
HOME_DIR="$(getent passwd "$OWNER" | cut -d: -f6)"
DIR="${BURROW_DIR:-$HOME_DIR/burrow}"
cmd="${1:-help}"

need_root() { [ "$(id -u)" -eq 0 ] || { echo "Run with sudo."; exit 1; }; }
compose() { (cd "$DIR" && docker compose "$@"); }
# Every compose.yaml this script handed out before it started keeping .compose.upstream.yaml,
# so an untouched copy from an older install still counts as untouched.
SHIPPED_COMPOSE="8d3e321d9bd3f889e8d3fc1de61469b0cdf0faf465b39faf9f214b0ee8fd4f5c 2c3f8197fe1cd0891d535b920f89ac47a08160fc21a920a911b698f93316fe95 60cb89a466d2d262d15c735bdf452a54eb9b4d2bca33a1bb5432d998e7f32612"
untouched() {
  [ -f "$DIR/.compose.upstream.yaml" ] && cmp -s "$1" "$DIR/.compose.upstream.yaml" && return 0
  case " $SHIPPED_COMPOSE " in *" $(sha256sum "$1" | cut -d' ' -f1) "*) return 0 ;; esac
  return 1
}
running() { [ -n "$(docker ps -q -f name='^burrow$')" ]; }
backup_to() { docker exec burrow burrow backup "/state/$1" && mkdir -p "$DIR/backups" && mv "$DIR/state/$1" "$DIR/backups/$1"; }
# set_env KEY VALUE writes KEY=VALUE into .env, or removes KEY when VALUE is empty
set_env() {
  grep -v "^$1=" "$DIR/.env" > "$DIR/.env.tmp" || true
  [ -n "$2" ] && echo "$1=$2" >> "$DIR/.env.tmp"
  mv "$DIR/.env.tmp" "$DIR/.env"
}
# image_tag turns a running version (main-<commit> or v1.2.3-<commit>) into the image tag that holds it
image_tag() { c=$(printf '%s' "$1" | grep -oE '[0-9a-f]{40}$' || true); [ -n "$c" ] && echo "sha-${c:0:7}" || echo ""; }

case "$cmd" in
  install)
    need_root
    LIB="${2:-}"
    [ -n "$LIB" ] && [ -d "$LIB" ] || { echo "Usage: install /path/to/library   (the folder Burrow may index, mounted read-only)"; exit 1; }
    command -v docker >/dev/null || { echo "Docker is not installed."; exit 1; }
    mkdir -p "$DIR/state" "$DIR/drop" "$DIR/backups"
    curl -fsSL "$REPO_RAW/compose.yaml" -o "$DIR/compose.yaml"
    cp "$DIR/compose.yaml" "$DIR/.compose.upstream.yaml"
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
    # In place: the container is replaced, everything in state/ (database, certificate,
    # thumbnails) and your files stay. The database upgrades itself on start.
    need_root
    [ -f "$DIR/.env" ] || { echo "No Burrow install in $DIR. Set BURROW_DIR if it lives elsewhere."; exit 1; }
    want="${2:-}"
    before=""
    if running; then
      before=$(docker exec burrow burrow version 2>&1 || true)
      f="burrow-$(date +%Y%m%d-%H%M%S)-before-update.db"
      backup_to "$f" && echo "Backed up first: $DIR/backups/$f"
    fi
    # Take the new compose.yaml, unless you edited yours (then it's left alone).
    new=$(mktemp)
    curl -fsSL "$REPO_RAW/compose.yaml" -o "$new"
    if [ ! -f "$DIR/compose.yaml" ] || cmp -s "$DIR/compose.yaml" "$new" || untouched "$DIR/compose.yaml"; then
      cp "$new" "$DIR/compose.yaml"
      rm -f "$DIR/compose.yaml.new"
    else
      cp "$new" "$DIR/compose.yaml.new"
      echo "You've edited compose.yaml, so it was kept; the current one is compose.yaml.new."
      echo "Move your changes into compose.override.yaml (Docker Compose merges it in) and future updates take the new file by themselves."
    fi
    cp "$new" "$DIR/.compose.upstream.yaml" && rm -f "$new"
    chown "$OWNER": "$DIR/compose.yaml" "$DIR/.compose.upstream.yaml"
    # A version pins that image (going back is an update to the older version); "latest" unpins.
    case "$want" in
      "") ;;
      latest) set_env BURROW_TAG "" ;;
      *) set_env BURROW_TAG "$want" ;;
    esac
    compose pull && compose up -d
    for i in $(seq 1 30); do docker exec burrow burrow healthcheck >/dev/null 2>&1 && break; sleep 2; done
    if ! docker exec burrow burrow healthcheck >/dev/null 2>&1; then
      echo "Burrow isn't answering after the update. See: docker logs burrow"
      prev=$(image_tag "$before")
      [ -n "$prev" ] && echo "To go back to the version you had: sudo bash install.sh update $prev"
      exit 1
    fi
    docker image prune -f >/dev/null
    now=$(docker exec burrow burrow version 2>&1 || true)
    if [ -n "$before" ] && [ "$before" != "$now" ]; then
      echo "Updated: $before -> $now"
      prev=$(image_tag "$before")
      [ -n "$prev" ] && echo "To go back: sudo bash install.sh update $prev"
    else
      echo "Running $now."
    fi
    ;;
  uninstall)
    need_root
    compose down || true
    echo "Container removed. Your data was never touched. Settings, links and backups are still in $DIR; delete that folder to remove them."
    ;;
  backup)
    f="burrow-$(date +%Y%m%d-%H%M%S).db"
    backup_to "$f"
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
    sed -n '2,5p' "$0" 2>/dev/null || echo "Commands: install <library> | update [version] | uninstall | backup | restore <file> | status"
    ;;
esac
