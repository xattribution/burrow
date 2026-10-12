#!/bin/bash
# Starts a Burrow server for the device tests, reachable from the Android
# emulator at 10.0.2.2, with a small library and an admin signed in (cookies).
set -euo pipefail
W="${RUNNER_TEMP:-/tmp}/burrow-ci"
rm -rf "$W" && mkdir -p "$W/lib/DOCS" "$W/lib/PHOTOS" "$W/state" "$W/drop"
echo "hello from the NAS" > "$W/lib/DOCS/hello.txt"
go build -o "$W/burrow" .
BURROW_DATA="$W/lib" BURROW_STATE="$W/state" BURROW_DROP="$W/drop" BURROW_ADDR=":47823" BURROW_PUBLIC_ADDR=":47824" \
  nohup "$W/burrow" > "$W/server.log" 2>&1 &
for _ in $(seq 1 30); do curl -skf https://127.0.0.1:47823/healthz > /dev/null && break; sleep 1; done
C=(curl -skf -c "$W/cookies" -b "$W/cookies" -H "X-Burrow: 1" -H "Content-Type: application/json")
"${C[@]}" -X POST https://127.0.0.1:47823/api/setup -d '{"user":"admin","password":"longenough1","roots":["DOCS","PHOTOS"]}' > /dev/null
"${C[@]}" -X PUT https://127.0.0.1:47823/api/settings -d '{"lan_url":"https://10.0.2.2:47823","public_url":"https://10.0.2.2:47824"}' > /dev/null
echo "BURROW_CI=$W" >> "${GITHUB_ENV:-/dev/null}"
echo "Burrow is up in $W"
