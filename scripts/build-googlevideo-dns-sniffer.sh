#!/bin/sh
set -eu

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
ARCH=${Z2R_GV_SNIFFER_ARCH:-${GOARCH:-$(uname -m)}}
case "$ARCH" in
  x86_64) ARCH=amd64 ;;
  aarch64) ARCH=arm64 ;;
  armv6l|armv7l|armv8l) ARCH=arm ;;
  mipsel*) ARCH=mipsle ;;
  mips64el*) ARCH=mips64le ;;
esac
OUT=${1:-"$ROOT/assets/tools/bin/googlevideo-dns-sniffer-$ARCH"}
CC=${CC:-cc}
STRIP=${STRIP:-strip}
ZIG=${ZIG:-zig}
TARGET=${Z2R_GV_SNIFFER_TARGET:-}
MAX_BYTES=${Z2R_GV_SNIFFER_MAX_BYTES:-143360}
TMP="${OUT}.tmp.$$"
trap 'rm -f "$TMP"' EXIT HUP INT TERM
mkdir -p "$(dirname "$OUT")"
if [ -n "$TARGET" ]; then
  "$ZIG" cc -target "$TARGET" -std=c99 -Os -Wall -Wextra -Werror -static -fno-ident \
    -ffunction-sections -fdata-sections -Wl,--gc-sections -Wl,--build-id=none -s \
    ${Z2R_GV_SNIFFER_CFLAGS:-} ${Z2R_GV_SNIFFER_LDFLAGS:-} \
    -o "$TMP" "$ROOT/tools/googlevideo-dns-sniffer.c"
else
  "$CC" -std=c99 -Os -Wall -Wextra -Werror -static -fno-ident \
    -ffunction-sections -fdata-sections -Wl,--gc-sections -Wl,--build-id=none \
    ${Z2R_GV_SNIFFER_CFLAGS:-} ${Z2R_GV_SNIFFER_LDFLAGS:-} \
    -o "$TMP" "$ROOT/tools/googlevideo-dns-sniffer.c"
  "$STRIP" "$TMP"
fi
size=$(wc -c < "$TMP" | tr -d ' ')
[ "$size" -le "$MAX_BYTES" ] || { echo "sniffer is ${size} bytes (limit ${MAX_BYTES})" >&2; exit 1; }
mv -f "$TMP" "$OUT"
echo "$OUT: $size bytes"
