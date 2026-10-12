#!/bin/bash
# Runs inside the emulator job: the instrumented tests, then the release build
# (shrunk by R8, like the published APK) paired and browsed through its own UI.
set -euo pipefail
W="$BURROW_CI"
APKS=android/app/build/outputs/apk
link() {
  curl -skf -c "$W/cookies" -b "$W/cookies" -H "X-Burrow: 1" -X POST https://127.0.0.1:47823/api/devices/pair |
    python3 -c 'import json,sys; print(json.load(sys.stdin)["link"])'
}

echo "== instrumented tests"
adb install -r -g "$APKS/debug/app-debug.apk"
adb install -r "$APKS/androidTest/debug/app-debug-androidTest.apk"
adb shell "am instrument -w -r -e link '$(link)' io.github.xattribution.burrow.test/androidx.test.runner.AndroidJUnitRunner" | tee "$W/instrument.txt"
if grep -q 'FAILURES!!!\|INSTRUMENTATION_FAILED\|Process crashed' "$W/instrument.txt" || ! grep -q '^OK (' "$W/instrument.txt"; then
  echo "instrumented tests failed"; tail -40 "$W/server.log"; exit 1
fi

echo "== release build"
adb uninstall io.github.xattribution.burrow > /dev/null || true
adb install -g "$APKS/release/app-release.apk"
L=$(link)
adb shell "am start -W -a android.intent.action.VIEW -d '$L' io.github.xattribution.burrow"

dump() { adb shell uiautomator dump /sdcard/ui.xml > /dev/null 2>&1 && adb pull /sdcard/ui.xml "$W/ui.xml" > /dev/null 2>&1; }
wait_for() {
  for _ in $(seq 1 45); do
    # a text view keeps a file's last newline (&#10;)
    dump && grep -qF -e "text=\"$1\"" -e "text=\"$1&#10;\"" "$W/ui.xml" && return 0
    sleep 2
  done
  echo "never saw \"$1\" on screen:"; grep -o 'text="[^"]*"' "$W/ui.xml" | sort -u | head -40; tail -30 "$W/server.log"
  adb logcat -d | grep -iE 'burrow|AndroidRuntime' | tail -60
  return 1
}
tap() {
  read -r X Y < <(python3 - "$W/ui.xml" "$1" <<'PY'
import re, sys
s = open(sys.argv[1]).read()
m = re.search(r'text="%s"[^>]*bounds="\[(\d+),(\d+)\]\[(\d+),(\d+)\]"' % re.escape(sys.argv[2]), s)
x1, y1, x2, y2 = map(int, m.groups())
print((x1 + x2) // 2, (y1 + y2) // 2)
PY
)
  adb shell input tap "$X" "$Y"
}

wait_for "Pair"
tap "Pair"
wait_for "DOCS"
tap "DOCS"
wait_for "hello.txt"
tap "hello.txt"
wait_for "hello from the NAS"
echo "release build: paired, browsed and opened a file"
