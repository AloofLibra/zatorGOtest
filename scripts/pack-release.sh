#!/usr/bin/env bash
# Build separate app and config resource archives plus latest.json.
set -euo pipefail
ROOT="${ROOT:-$(cd "$(dirname "$0")/.." && pwd)}"
OUT="${1:?usage: pack-release.sh OUT_DIR}"
VER="${VER:-${GITHUB_REF_NAME:-$(cat "$ROOT/VERSION" 2>/dev/null || echo dev)}}"
COMMIT="${COMMIT:-${GITHUB_SHA:-$(git -C "$ROOT" rev-parse HEAD)}}"
DATE="$(date -u +%Y-%m-%d)"
BUILDDATE="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
mkdir -p "$OUT"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

make_archive() {
  local name="$1" root="$2" kind="$3"
  local archive="$OUT/zator-$name.tar.gz"
  local manifest="$root/extra_strats/cache/deploy/manifest.tsv"
  mkdir -p "$(dirname "$manifest")"
  : >"$manifest"
  while IFS= read -r -d '' f; do
    local rel dest sum size exec class
    rel=${f#"$root/"}
    [[ "$rel" == extra_strats/cache/deploy/manifest.tsv || "$rel" == extra_strats/cache/deploy/version.env ]] && continue
    sum=$(sha256sum "$f" | awk '{print $1}')
    size=$(wc -c <"$f" | tr -d ' ')
    exec=0
    case "$rel" in *.sh|init/*|tools/bin/googlevideo-dns-sniffer-*) exec=1 ;; esac
    dest="/opt/zator/$rel"; class=auto
    case "$rel" in
      extra_strats/cache/orchestra/*) class=auto ;;
      extra_strats/sni_zator.txt) class=auto ;;
      extra_strats/sni_user.txt) class=keep-if-exists ;;
      lists/*|extra_strats/*) class=update-if-unmodified ;;
      files/fake/custom_tls.bin) class=keep-if-exists ;;
      blockcheck2/*) dest="/opt/zapret2/blockcheck2.d/${rel#blockcheck2/}" ;;
    esac
    printf '%s|%s|%s|%s|%s|%s\n' "$rel" "$dest" "$class" "$sum" "$size" "$exec" >>"$manifest"
  done < <(find "$root" -type f -print0 | sort -z)
  tar -C "$root" -czf "$archive" .
  local size unpacked sha
  size=$(wc -c <"$archive" | tr -d ' ')
  unpacked=$(du -sb "$root" | awk '{print $1}')
  sha=$(sha256sum "$archive" | awk '{print $1}')
  printf '    "%s": {"name":"zator-%s.tar.gz","size":%s,"sha256":"%s","unpackedSize":%s}' \
    "$kind" "$name" "$size" "$sha" "$unpacked"
}

APP="$TMP/app"; CFG="$TMP/config"
mkdir -p "$APP" "$CFG"
cp -a "$ROOT/assets/webui" "$APP/"
cp -a "$ROOT/assets/init" "$APP/"
mkdir -p "$APP/tools/bin"
cp -a "$ROOT/assets/tools/." "$APP/tools/" 2>/dev/null || true
SNIFFER_DIR="${SNIFFER_DIR:-$ROOT/release-sniffers}"
if [[ -d "$SNIFFER_DIR" ]]; then cp -a "$SNIFFER_DIR/." "$APP/tools/bin/"; fi

mkdir -p "$CFG/lua" "$CFG/lists" "$CFG/data" "$CFG/files/fake" \
  "$CFG/extra_strats/cache/orchestra" "$CFG/config" "$CFG/blockcheck2"
cp -a "$ROOT/assets/lua/." "$CFG/lua/"
cp -a "$ROOT/assets/lists/." "$CFG/lists/"
cp -a "$ROOT/assets/data/." "$CFG/data/"
cp -a "$ROOT/assets/files/fake/." "$CFG/files/fake/"
cp -a "$ROOT/assets/extra_strats/." "$CFG/extra_strats/"
cp -a "$ROOT/assets/orchestra/." "$CFG/extra_strats/cache/orchestra/"
cp -a "$ROOT/assets/blockcheck2/." "$CFG/blockcheck2/"
cp "$ROOT/assets/config.default" "$CFG/config/config.default"

normalize_lf_tree() {
  local base="$1" f tmp
  while IFS= read -r -d '' f; do
    tmp="$f.lf.tmp"
    tr -d '\r' <"$f" >"$tmp"
    cat "$tmp" >"$f"
    rm -f "$tmp"
  done < <(find "$base" -type f \( \
    -path "$base/init/*" -o \
    -path "$base/blockcheck2/*" -o \
    -name '*.sh' -o \
    -name '*.lua' -o \
    -name '*.txt' -o \
    -name '*.tsv' -o \
    -path "$base/config/config.default" \
  \) -print0)
}
normalize_lf_tree "$APP"
normalize_lf_tree "$CFG"

WEBUI_SHA=$(find "$APP/webui" -type f -print0 | sort -z | xargs -0 sha256sum | sha256sum | awk '{print $1}')
CONFIG_SHA=$(find "$CFG" -type f -print0 | sort -z | xargs -0 sha256sum | sha256sum | awk '{print $1}')
mkdir -p "$APP/extra_strats/cache/deploy" "$CFG/extra_strats/cache/deploy"
cat >"$APP/extra_strats/cache/deploy/version.env" <<EOF
ZATOR_VERSION="$VER"
ZATOR_DATE="$DATE"
ZATOR_COMMIT="$COMMIT"
ZATOR_SHA="$COMMIT"
WEBUI_VERSION="$VER"
WEBUI_DATE="$DATE"
WEBUI_SHA="$WEBUI_SHA"
CONFIG_VERSION="$VER"
CONFIG_SHA="$CONFIG_SHA"
TRACKING="latest"
EOF
cp "$APP/extra_strats/cache/deploy/version.env" "$CFG/extra_strats/cache/deploy/version.env"
# Metadata is kept only in app archive; config version is carried in latest.json.
make_archive app "$APP" app >"$TMP/app-asset.json"
make_archive config "$CFG" config >"$TMP/config-asset.json"
cat >"$OUT/latest.json" <<EOF
{
  "schemaVersion": 1,
  "release": "$VER",
  "buildDate": "$BUILDDATE",
  "commit": "$COMMIT",
  "zatorSha": "$COMMIT",
  "webuiSha": "$WEBUI_SHA",
  "configSha": "$CONFIG_SHA",
  "zatorDate": "$DATE",
  "webuiDate": "$DATE",
  "assets": {
$(cat "$TMP/app-asset.json"),
$(cat "$TMP/config-asset.json")
  }
}
EOF
