#!/bin/sh
# zator-launcher — минимальный bootstrap: определяет arch, качает бинарник, запускает.
# Breaking change относительно старого z4r/z2r: новый URL и имя артефактов.
set -eu

ZATOR_OWNER="${ZATOR_OWNER:-AloofLibra}"
ZATOR_REPO="${ZATOR_REPO:-zator}"
ZATOR_RELEASE="${ZATOR_RELEASE:-latest}"
ZATOR_ROOT="${ZATOR_ROOT:-/opt/zator}"
ZATOR_BIN_DIR="${ZATOR_BIN_DIR:-$ZATOR_ROOT/bin}"
ZATOR_MIRROR="${ZATOR_MIRROR:-}"
ZATOR_BASE_URL="${ZATOR_BASE_URL:-https://github.com/${ZATOR_OWNER}/${ZATOR_REPO}/releases/download}"

umask 022

die() { echo "zator-launcher: $*" >&2; exit 1; }

need_cmd() {
  command -v "$1" >/dev/null 2>&1 || die "нужна утилита: $1"
}

detect_arch() {
  m=$(uname -m 2>/dev/null || echo unknown)
  case "$m" in
    x86_64|amd64) echo x86_64 ;;
    i386|i686|x86) echo x86 ;;
    aarch64|arm64) echo arm64 ;;
    armv7*|armv6*|arm) echo arm ;;
    mips64el|mips64le) echo mipsel64 ;;
    mips64) echo mips64 ;;
    mipsel|mipsle) echo mipsel ;;
    mips) echo mips ;;
    ppc|ppc32) echo ppc ;;
    ppc64|ppc64le) echo ppc ;;
    riscv64) echo riscv64 ;;
    *)
      # Lexra / особые SoC Keenetic
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

arch=$(detect_arch)
asset="zator-linux-${arch}"
install_dir="$ZATOR_BIN_DIR"
mkdir -p "$install_dir"
tmp="$install_dir/.${asset}.$$"
trap 'rm -f "$tmp"' EXIT INT HUP

if [ -n "$ZATOR_MIRROR" ]; then
  url="${ZATOR_MIRROR%/}/${asset}"
else
  url="${ZATOR_BASE_URL%/}/${ZATOR_RELEASE}/${asset}"
fi

echo "zator-launcher: arch=${arch}"
echo "zator-launcher: download ${url}"
download "$url" "$tmp" || die "не удалось скачать ${url}"
chmod 755 "$tmp"
mv -f "$tmp" "$install_dir/zator"
trap - EXIT INT HUP

# Первый запуск: установка + подсказка Web UI.
if [ ! -f "$ZATOR_ROOT/config/zator.json" ]; then
  echo "zator-launcher: первичная установка..."
  "$install_dir/zator" install "$@" || die "install завершился с ошибкой"
fi

echo "zator-launcher: запуск Web UI"
exec "$install_dir/zator" webui "$@"
