#!/usr/bin/env bash
# Собирает офлайн-бандл для TARGET_ARCH (по умолчанию архитектура сборочного хоста).
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
OUT="${1:-$ROOT/dist/offline}"
VER="${VER:-$(cat "$ROOT/VERSION" 2>/dev/null || echo dev)}"
TARGET_ARCH="${TARGET_ARCH:-$(uname -m)}"
case "$TARGET_ARCH" in
  aarch64|arm64) TARGET_ARCH=arm64 ;;
  armv7*|armv6*|arm) TARGET_ARCH=arm ;;
  x86_64|amd64) TARGET_ARCH=x86_64 ;;
  i386|i686|x86) TARGET_ARCH=x86 ;;
  mipsel|mipsle) TARGET_ARCH=mipsel ;;
  lexra|rlx) TARGET_ARCH=mipsel ;;
  mips) TARGET_ARCH=mips ;;
  riscv64) TARGET_ARCH=riscv64 ;;
  *) echo "unknown TARGET_ARCH: $TARGET_ARCH" >&2; exit 1 ;;
esac
TARGET_BINARY="$ROOT/dist/zator-linux-$TARGET_ARCH"
if [[ ! -s "$TARGET_BINARY" ]]; then
  OUT="$ROOT/dist" bash "$ROOT/scripts/crossbuild.sh"
fi
[[ -s "$TARGET_BINARY" ]] || { echo "missing target binary: $TARGET_BINARY" >&2; exit 1; }
mkdir -p "$OUT/vendor"

case "$TARGET_ARCH" in
  arm) SNIFFER_ARCH=arm; SNIFFER_TARGET=arm-linux-musleabihf ;;
  arm64) SNIFFER_ARCH=arm64; SNIFFER_TARGET=aarch64-linux-musl ;;
  mips) SNIFFER_ARCH=mips; SNIFFER_TARGET=mips-linux-musl ;;
  mipsel) SNIFFER_ARCH=mipsle; SNIFFER_TARGET=mipsel-linux-musl ;;
  x86) SNIFFER_ARCH=386; SNIFFER_TARGET=x86-linux-musl ;;
  x86_64) SNIFFER_ARCH=amd64; SNIFFER_TARGET=x86_64-linux-musl ;;
  riscv64) SNIFFER_ARCH=riscv64; SNIFFER_TARGET=riscv64-linux-musl ;;
esac
SNIFFER_DIR="$(mktemp -d)"
trap 'rm -rf "$SNIFFER_DIR"' EXIT
Z2R_GV_SNIFFER_TARGET="$SNIFFER_TARGET" \
  bash "$ROOT/scripts/build-googlevideo-dns-sniffer.sh" "$SNIFFER_DIR/googlevideo-dns-sniffer-$SNIFFER_ARCH"

COMMIT="${COMMIT:-$(git -C "$ROOT" rev-parse HEAD)}"
VER="$VER" COMMIT="$COMMIT" BINARY_PATH="$TARGET_BINARY" SNIFFER_DIR="$SNIFFER_DIR" \
  bash "$ROOT/scripts/pack-release.sh" "$OUT"
cp "$ROOT/scripts/zator-launcher.sh" "$OUT/zator-launcher.sh"
chmod 755 "$OUT/zator-launcher.sh"
for f in "$ROOT"/dist/zator-linux-*; do
  [[ -f "$f" && "$f" != *.sha256 ]] || continue
  (cd "$ROOT/dist" && sha256sum "$(basename "$f")") >"$f.sha256"
done
cp "$ROOT/dist"/zator-linux-* "$OUT/vendor/" 2>/dev/null || true

# zapret2 в vendor/ — установка без GitHub (ZATOR_VENDOR / LocalArchive).
ZAPRET_VER="${ZAPRET2_VERSION:-1.0.7-reasm-fix}"
ZAPRET_ASSET="zapret2-v${ZAPRET_VER}.tar.gz"
ZAPRET_URL="${ZAPRET2_ARCHIVE_URL:-https://github.com/MarkinAlexander/zapret2/releases/download/v${ZAPRET_VER}/${ZAPRET_ASSET}}"
if [[ -f "$ROOT/vendor/$ZAPRET_ASSET" && ! -f "$OUT/vendor/$ZAPRET_ASSET" ]]; then
  cp "$ROOT/vendor/$ZAPRET_ASSET" "$OUT/vendor/$ZAPRET_ASSET"
fi
if [[ ! -f "$OUT/vendor/$ZAPRET_ASSET" ]]; then
  echo "скачивание $ZAPRET_URL → vendor/"
  curl -fsSL -o "$OUT/vendor/$ZAPRET_ASSET" "$ZAPRET_URL" || {
    echo "warn: не удалось скачать zapret2; положите $ZAPRET_ASSET в vendor/ вручную" >&2
  }
fi
# Копия рядом с репозиторием для офлайн-разработки.
mkdir -p "$ROOT/vendor"
if [[ -f "$OUT/vendor/$ZAPRET_ASSET" && ! -f "$ROOT/vendor/$ZAPRET_ASSET" ]]; then
  cp "$OUT/vendor/$ZAPRET_ASSET" "$ROOT/vendor/$ZAPRET_ASSET"
fi

echo "offline bundle → $OUT"
ls -lh "$OUT" "$OUT/vendor"
