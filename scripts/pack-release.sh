#!/usr/bin/env bash
# Собирает versioned full resource archive и latest.json для GitHub Releases.
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
OUT="${1:?usage: pack-release.sh OUT_DIR}"
VER="${VER:-${GITHUB_REF_NAME:-$(cat "$ROOT/VERSION" 2>/dev/null || echo dev)}}"
COMMIT="${COMMIT:-${GITHUB_SHA:-$(git -C "$ROOT" rev-parse HEAD)}}"
DATE="$(date -u +%Y-%m-%d)"
BUILDDATE="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
mkdir -p "$OUT"
STAGE="$(mktemp -d)"
trap 'rm -rf "$STAGE"' EXIT
mkdir -p "$STAGE/webui" "$STAGE/lua" "$STAGE/lists" "$STAGE/data" \
  "$STAGE/files/fake" "$STAGE/extra_strats" "$STAGE/extra_strats/cache/orchestra" \
  "$STAGE/config" "$STAGE/extra_strats/cache/deploy"
cp -a "$ROOT/assets/webui/." "$STAGE/webui/"
cp -a "$ROOT/assets/lua/." "$STAGE/lua/"
cp -a "$ROOT/assets/lists/." "$STAGE/lists/"
cp -a "$ROOT/assets/data/." "$STAGE/data/"
cp -a "$ROOT/assets/files/fake/." "$STAGE/files/fake/"
cp -a "$ROOT/assets/extra_strats/." "$STAGE/extra_strats/"
cp -a "$ROOT/assets/orchestra/." "$STAGE/extra_strats/cache/orchestra/"
cp "$ROOT/assets/config.default" "$STAGE/config/config.default"
if [[ -n "${BINARY_PATH:-}" ]]; then
  [[ -s "$BINARY_PATH" ]] || { echo "binary not found: $BINARY_PATH" >&2; exit 1; }
  mkdir -p "$STAGE/bin"
  install -m 0755 "$BINARY_PATH" "$STAGE/bin/zator"
fi

MAN="$STAGE/extra_strats/cache/deploy/manifest.tsv"
: > "$MAN"
while IFS= read -r -d '' f; do
  rel=${f#"$STAGE/"}
  [[ "$rel" == extra_strats/cache/deploy/* ]] && continue
  sum=$(sha256sum "$f" | awk '{print $1}')
  size=$(wc -c <"$f" | tr -d ' ')
  exec=0
  [[ -x "$f" ]] && exec=1
  class=auto
  case "$rel" in
    extra_strats/cache/orchestra/*) class=auto ;;
    files/fake/custom_tls.bin) class=keep-if-exists ;;
    lists/*|extra_strats/*) class=keep-if-exists ;;
  esac
  printf '%s|/opt/zator/%s|%s|%s|%s|%s\n' "$rel" "$rel" "$class" "$sum" "$size" "$exec" >>"$MAN"
done < <(find "$STAGE" -type f -print0)

WEBUI_SHA="$(find "$ROOT/assets/webui" -type f -print0 | sort -z | xargs -0 sha256sum | sha256sum | awk '{print $1}')"
cat >"$STAGE/extra_strats/cache/deploy/version.env" <<EOF
ZATOR_VERSION="$VER"
ZATOR_DATE="$DATE"
ZATOR_COMMIT="$COMMIT"
ZATOR_SHA="$COMMIT"
WEBUI_VERSION="$VER"
WEBUI_DATE="$DATE"
WEBUI_SHA="$WEBUI_SHA"
TRACKING="latest"
EOF

ARCHIVE="$OUT/zator-full.tar.gz"
tar -C "$STAGE" -czf "$ARCHIVE" .
SIZE=$(wc -c <"$ARCHIVE" | tr -d ' ')
UNPACKED=$(du -sb "$STAGE" | awk '{print $1}')
SHA=$(sha256sum "$ARCHIVE" | awk '{print $1}')
cat >"$OUT/latest.json" <<EOF
{
  "schemaVersion": 1,
  "release": "$VER",
  "buildDate": "$BUILDDATE",
  "commit": "$COMMIT",
  "zatorSha": "$COMMIT",
  "webuiSha": "$WEBUI_SHA",
  "zatorDate": "$DATE",
  "webuiDate": "$DATE",
  "assets": {
    "full": {
      "name": "zator-full.tar.gz",
      "size": $SIZE,
      "sha256": "$SHA",
      "unpackedSize": $UNPACKED
    }
  }
}
EOF
