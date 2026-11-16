#!/bin/sh
# zator-launcher — минимальный bootstrap: определяет arch, качает бинарник, запускает.
set -eu

ZATOR_OWNER="${ZATOR_OWNER:-Koviand}"
ZATOR_REPO="${ZATOR_REPO:-zatorGO}"
ZATOR_RELEASE="${ZATOR_RELEASE:-latest}"
ZATOR_ROOT="${ZATOR_ROOT:-/opt/zator}"
ZATOR_BIN_DIR="${ZATOR_BIN_DIR:-$ZATOR_ROOT/bin}"
ZATOR_MIRROR="${ZATOR_MIRROR:-}"
ZATOR_BUNDLE_DIR="${ZATOR_BUNDLE_DIR:-$(CDPATH= cd -- "$(dirname "$0")" && pwd)}"
ZATOR_VENDOR="${ZATOR_VENDOR:-$ZATOR_BUNDLE_DIR/vendor}"
export ZATOR_BUNDLE_DIR ZATOR_VENDOR

umask 022

die() { echo "zator-launcher: $*" >&2; exit 1; }

detect_lan_ip() {
  local iface iface_ip
  if command -v ip >/dev/null 2>&1; then
    for iface in br-lan br0; do
      iface_ip="$(ip -4 addr show dev "$iface" 2>/dev/null | awk '/inet /{print $2; exit}' | cut -d/ -f1)"
      if [ -n "$iface_ip" ]; then
        echo "$iface_ip"
        return 0
      fi
    done
  fi
  hostname -I 2>/dev/null | tr ' ' '\n' | grep -v '^127\.' | grep -E '^[0-9]+\.' | head -n 1
}

detect_arch() {
  m=$(uname -m 2>/dev/null || echo unknown)
  case "$m" in
    x86_64|amd64) echo x86_64 ;;
    i386|i686|x86) echo x86 ;;
    aarch64|arm64) echo arm64 ;;
    armv7*|armv6*|arm) echo arm ;;
    mipsel|mipsle) echo mipsel ;;
    mips) echo mips ;;
    riscv64) echo riscv64 ;;
    *)
      if grep -qiE 'lexra|rlx' /proc/cpuinfo 2>/dev/null; then
        echo lexra
      else
        die "неизвестная архитектура: $m"
      fi
      ;;
  esac
}

download() {
  url="$1"
  dest="$2"
  if command -v curl >/dev/null 2>&1; then
    curl -fsSL --connect-timeout 10 --retry 3 -o "$dest" "$url"
  elif command -v wget >/dev/null 2>&1; then
    wget -q -O "$dest" "$url"
  else
    die "нужен curl или wget"
  fi
}

# GitHub: latest → /releases/latest/download/<file>
#         tag    → /releases/download/<tag>/<file>
asset_url() {
  asset="$1"
  if [ -n "$ZATOR_MIRROR" ]; then
    printf '%s\n' "${ZATOR_MIRROR%/}/${asset}"
    return
  fi
  if [ -n "${ZATOR_BASE_URL:-}" ]; then
    printf '%s\n' "${ZATOR_BASE_URL%/}/${asset}"
    return
  fi
  base="https://github.com/${ZATOR_OWNER}/${ZATOR_REPO}"
  if [ "$ZATOR_RELEASE" = "latest" ]; then
    printf '%s\n' "${base}/releases/latest/download/${asset}"
  else
    printf '%s\n' "${base}/releases/download/${ZATOR_RELEASE}/${asset}"
  fi
}

arch=$(detect_arch)
# Lexra (RLX) — нет отдельного Go-таргета; релизный ассет = mipsel softfloat.
asset_arch=$arch
if [ "$arch" = lexra ]; then
  asset_arch=mipsel
  echo "zator-launcher: архитектура Lexra — скачиваем zator-linux-mipsel" >&2
fi
asset="zator-linux-${asset_arch}"
install_dir="$ZATOR_BIN_DIR"
mkdir -p "$install_dir"
tmp="$install_dir/.${asset}.$$"
trap 'rm -f "$tmp"' EXIT INT HUP

url=$(asset_url "$asset")

echo "zator-launcher: arch=${arch}"
local_binary="$ZATOR_VENDOR/$asset"
if [ -f "$local_binary" ]; then
  sums="$local_binary.sha256"
  [ -f "$sums" ] || die "нет checksum для offline бинарника $asset"
  expected=$(awk -v want="$asset" '{ name=$2; sub(/^\*/, "", name); sub(/^.*\//, "", name); if (name == want) { print $1; exit } }' "$sums")
  actual=$(sha256sum "$local_binary" | awk '{print $1}')
  [ -n "$expected" ] && [ "$actual" = "$expected" ] || die "checksum offline бинарника $asset не совпал"
  cp "$local_binary" "$tmp" || die "не удалось скопировать offline бинарник $asset"
  echo "zator-launcher: local vendor ${local_binary}"
else
  echo "zator-launcher: download ${url}"
  download "$url" "$tmp" || die "не удалось скачать ${url}"
fi
chmod 755 "$tmp"
mv -f "$tmp" "$install_dir/zator"
trap - EXIT INT HUP

echo "zator-launcher: установка / обновление (zapret2 latest + автозапуск)..."
"$install_dir/zator" install "$@" || die "install завершился с ошибкой"

# Службы zapret2 и Web UI поднимает сам install. Не держим webui
# в foreground, чтобы не конфликтовать с S92/systemd.
echo "zator-launcher: готово"
if [ -f "$ZATOR_ROOT/config/zator.json" ]; then
  port=$(sed -n 's/.*"port"[[:space:]]*:[[:space:]]*\([0-9][0-9]*\).*/\1/p' "$ZATOR_ROOT/config/zator.json" 2>/dev/null | head -n1)
fi
port="${port:-17682}"
lan_ip="$(detect_lan_ip || true)"
lan_ip="${lan_ip:-127.0.0.1}"
echo "zator-launcher: Web UI → http://${lan_ip}:${port}/"
exit 0
