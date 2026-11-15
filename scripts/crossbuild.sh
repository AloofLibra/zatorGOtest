#!/usr/bin/env bash
# Кросс-сборка zator под целевые архитектуры роутеров и VPS.
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
OUT="${OUT:-$ROOT/dist}"
VER="${VER:-$(cat "$ROOT/VERSION" 2>/dev/null || echo dev)}"
mkdir -p "$OUT"

targets=(
  "arm:arm:7:"
  "arm64:arm64::"
  "mips:mips::softfloat"
  "mips64:mips64::softfloat"
  "mipsel:mipsle::softfloat"
  "mipsel64:mips64le::softfloat"
  "ppc:ppc64::"
  "x86:386::"
  "x86_64:amd64::"
  "riscv64:riscv64::"
)

export CGO_ENABLED=0
for spec in "${targets[@]}"; do
  IFS=':' read -r name goarch goarm gomips <<<"$spec"
  echo "==> linux/$name ($goarch)"
  env GOOS=linux GOARCH="$goarch" GOARM="${goarm:-}" GOMIPS="${gomips:-}" \
    go build -trimpath -ldflags "-s -w -X github.com/AloofLibra/zator.versionOverride=${VER}" \
    -o "$OUT/zator-linux-${name}" "$ROOT/cmd/zator"
done

cp "$ROOT/scripts/zator-launcher.sh" "$OUT/zator-launcher.sh"
chmod +x "$OUT/zator-launcher.sh"
for f in "$OUT"/zator-linux-*; do
  [[ "$f" == *.sha256 ]] && continue
  chmod +x "$f"
  (cd "$OUT" && sha256sum "$(basename "$f")") >"$f.sha256"
done
(
  cd "$OUT"
  for f in zator-linux-*; do
    [[ "$f" == *.sha256 ]] && continue
    sha256sum "$f"
  done
  sha256sum zator-launcher.sh
) >"$OUT/SHA256SUMS"
ls -lh "$OUT"
