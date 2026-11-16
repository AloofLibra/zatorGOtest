#!/usr/bin/env bash
# Build one Linux Zator binary; optional Garble/UPX packing is shared by CI and crossbuild.
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
OUTPUT="${1:?usage: build-zator-binary.sh OUTPUT [VERSION]}"
VER="${2:-${VER:-dev}}"
LDFLAGS="-s -w -X github.com/AloofLibra/zator.versionOverride=${VER}"

mkdir -p "$(dirname "$OUTPUT")"
if [[ "${ZATOR_GARBLE:-0}" == "1" ]]; then
  command -v garble >/dev/null || { echo "garble is required when ZATOR_GARBLE=1" >&2; exit 1; }
  garble "-seed=${ZATOR_GARBLE_SEED:-Z2FyYmxlMDE}" -tiny build -mod=mod \
    -ldflags "$LDFLAGS" -o "$OUTPUT" ./cmd/zator
else
  go build -mod=mod -trimpath -ldflags "$LDFLAGS" -o "$OUTPUT" ./cmd/zator
fi

if [[ "${ZATOR_UPX:-0}" != "1" ]]; then
  exit 0
fi
command -v upx >/dev/null || { echo "upx is required when ZATOR_UPX=1" >&2; exit 1; }
if [[ "${GOOS:-linux}" != "linux" ]]; then
  echo "UPX packing skipped: only Linux release binaries are configured" >&2
  exit 0
fi
case "${GOARCH:-}" in
  mips64|mips64le)
    echo "UPX packing skipped: UPX 5.3.0 does not support linux/${GOARCH}; keeping the Garble binary" >&2
    exit 0
    ;;
esac

PACKED="$(mktemp "${OUTPUT}.upx.XXXXXX")"
trap 'rm -f "$PACKED"' EXIT
cp "$OUTPUT" "$PACKED"
upx --best --lzma "$PACKED"
upx -t "$PACKED"
ORIGINAL_SIZE="$(wc -c <"$OUTPUT" | tr -d ' ')"
PACKED_SIZE="$(wc -c <"$PACKED" | tr -d ' ')"
if (( PACKED_SIZE < ORIGINAL_SIZE )); then
  mv "$PACKED" "$OUTPUT"
else
  echo "UPX output is not smaller; keeping original binary" >&2
fi
