#!/usr/bin/env bash
# Собирает офлайн-бандл: vendor/ (zapret2 архивы) + zator-full.tar.gz + launcher.
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
OUT="${1:-$ROOT/dist/offline}"
VER="${VER:-$(cat "$ROOT/VERSION" 2>/dev/null || echo dev)}"
mkdir -p "$OUT/vendor"

if [[ ! -d "$ROOT/dist" ]] || ! ls "$ROOT/dist"/zator-linux-* >/dev/null 2>&1; then
  OUT="$ROOT/dist" bash "$ROOT/scripts/crossbuild.sh"
fi

# Минимальный full-архив из assets + бинарник хост-arch (для применения deploy.Apply).
STAGE=$(mktemp -d)
trap 'rm -rf "$STAGE"' EXIT
mkdir -p "$STAGE/extra_strats/cache/deploy" "$STAGE/webui" "$STAGE/lua" "$STAGE/lists" "$STAGE/bin"
cp -a "$ROOT/assets/webui/." "$STAGE/webui/"
cp -a "$ROOT/assets/lua/." "$STAGE/lua/" 2>/dev/null || true
cp -a "$ROOT/assets/lists/." "$STAGE/lists/" 2>/dev/null || true
cp "$ROOT/assets/config.default" "$STAGE/config.default"
cp "$ROOT/dist/zator-linux-x86_64" "$STAGE/bin/zator" 2>/dev/null || true

# Манифест: path|dest|class|sha256|size|exec
MAN="$STAGE/extra_strats/cache/deploy/manifest.tsv"
: > "$MAN"
while IFS= read -r -d '' f; do
  rel=${f#"$STAGE/"}
  [[ "$rel" == extra_strats/cache/deploy/* ]] && continue
  sum=$(sha256sum "$f" | awk '{print $1}')
  size=$(wc -c <"$f" | tr -d ' ')
  exec=0
  [[ -x "$f" ]] && exec=1
  printf '%s|/opt/zator/%s|auto|%s|%s|%s\n' "$rel" "$rel" "$sum" "$size" "$exec" >>"$MAN"
done < <(find "$STAGE" -type f -print0)

cat >"$STAGE/extra_strats/cache/deploy/version.env" <<EOF
ZATOR_VERSION="$VER"
ZATOR_DATE="$(date -u +%Y-%m-%d)"
ZATOR_COMMIT=""
ZATOR_SHA="$(sha256sum "$MAN" | awk '{print $1}')"
WEBUI_VERSION="$VER"
WEBUI_DATE="$(date -u +%Y-%m-%d)"
WEBUI_SHA=""
TRACKING="offline"
EOF

tar -C "$STAGE" -czf "$OUT/zator-full.tar.gz" .
cp "$ROOT/scripts/zator-launcher.sh" "$OUT/zator-launcher.sh"
cp "$ROOT/dist"/zator-linux-* "$OUT/vendor/" 2>/dev/null || true
echo "offline bundle → $OUT"
ls -lh "$OUT"
