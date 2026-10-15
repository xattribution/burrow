#!/usr/bin/env bash
# Builds tethersys for the app: the Android library (arm64-v8a and x86_64 by
# default, ABIS to change), a host copy for the JVM tests, and the Kotlin
# bindings. It uses the tethersys revision the server's bridge pins in
# tether/Cargo.toml, so both ends always speak the same protocol.
#
# Needs Rust, cargo-ndk and the Android NDK (ANDROID_NDK_HOME). Without the
# library the app still builds and runs; it just can't reach Burrow over tethersys.
set -euo pipefail
here=$(cd "$(dirname "$0")" && pwd)
repo=$(cd "$here/../.." && pwd)
rev=$(sed -n 's/^tethersys = .*rev = "\([0-9a-f]*\)".*/\1/p' "$repo/tether/Cargo.toml")
[ -n "$rev" ] || { echo "no tethersys rev in tether/Cargo.toml" >&2; exit 1; }
src=$(realpath -m "${TETHERSYS_SRC:-$here/build/tethersys}")

if [ ! -d "$src/.git" ]; then
  git clone -q https://github.com/xattribution/tethersys "$src"
fi
git -C "$src" cat-file -e "$rev^{commit}" 2>/dev/null || git -C "$src" fetch -q origin "$rev" || git -C "$src" fetch -q origin
git -C "$src" -c advice.detachedHead=false checkout -q "$rev"

# small over fast: the library ships in the APK
export CARGO_PROFILE_RELEASE_OPT_LEVEL=s CARGO_PROFILE_RELEASE_LTO=fat CARGO_PROFILE_RELEASE_CODEGEN_UNITS=1
abis=${ABIS:-arm64-v8a x86_64}
for abi in $abis; do
  case $abi in
    arm64-v8a) triple=aarch64-linux-android ;;
    x86_64) triple=x86_64-linux-android ;;
    *) echo "unsupported ABI $abi" >&2; exit 1 ;;
  esac
  (cd "$src" && cargo ndk -t "$abi" --platform 29 build --release -p tethersys-ffi)
  # only the library itself: cargo also leaves some dependencies' own .so files there
  mkdir -p "$here/../app/src/main/jniLibs/$abi"
  cp "$src/target/$triple/release/libtethersys_ffi.so" "$here/../app/src/main/jniLibs/$abi/"
done

if [ "${HOST:-1}" = 1 ]; then
  (cd "$src" && cargo build --release -p tethersys-ffi)
  mkdir -p "$here/build/host"
  cp "$src/target/release/libtethersys_ffi.so" "$here/build/host/"
fi

dest="$here/../app/src/main/kotlin/uniffi/tethersys_ffi"
mkdir -p "$dest"
cp "$src/bindings/kotlin/uniffi/tethersys_ffi/tethersys_ffi.kt" "$dest/"
du -h "$here"/../app/src/main/jniLibs/*/libtethersys_ffi.so
