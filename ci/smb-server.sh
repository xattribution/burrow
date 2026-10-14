#!/bin/bash
# Starts a throwaway Samba server on loopback for the connected-NAS tests, then
# prints the BURROW_TEST_SMB value that turns those tests on. Needs root (sudo).
#   eval "$(sudo ci/smb-server.sh)" && go test -run Remote ./...
set -euo pipefail
D="${BURROW_SMB_DIR:-/tmp/burrow-smb}"
PORT="${BURROW_SMB_PORT:-44545}"
USER_NAME=burrowtest
PASS=smb-test-pass-1
command -v smbd > /dev/null || { apt-get install -y -q samba > /dev/null; }
id "$USER_NAME" > /dev/null 2>&1 || useradd -M -s /usr/sbin/nologin "$USER_NAME"
pkill -f "smbd.*$D/smb.conf" 2> /dev/null || true
rm -rf "$D" && mkdir -p "$D"/{share,private,lock,state,cache,pid,ncalrpc}
chmod 777 "$D/share"
cat > "$D/smb.conf" <<CONF
[global]
  server role = standalone server
  smb ports = $PORT
  interfaces = lo
  bind interfaces only = yes
  disable netbios = yes
  server min protocol = SMB2_10
  map to guest = never
  private dir = $D/private
  lock directory = $D/lock
  state directory = $D/state
  cache directory = $D/cache
  pid directory = $D/pid
  ncalrpc dir = $D/ncalrpc
  log file = $D/log.%m
  passdb backend = tdbsam:$D/private/passdb.tdb
[Media]
  path = $D/share
  read only = no
  valid users = $USER_NAME
  force user = root
CONF
printf '%s\n%s\n' "$PASS" "$PASS" | smbpasswd -c "$D/smb.conf" -s -a "$USER_NAME" > /dev/null
smbd -D -s "$D/smb.conf"
for _ in $(seq 1 30); do (echo > /dev/tcp/127.0.0.1/$PORT) 2> /dev/null && break; sleep 0.3; done
echo "export BURROW_TEST_SMB=127.0.0.1:$PORT:Media:$USER_NAME:$PASS:$D/share"
