#!/bin/sh
# zator-launcher — bootstrap из репозитория Koviand/zatorGOtest.
# Ставит бинарник zator, затем install (zapret2=latest + автозапуск служб).
set -eu

ZATOR_OWNER="${ZATOR_OWNER:-Koviand}"
ZATOR_REPO="${ZATOR_REPO:-zatorGOtest}"
ZATOR_RELEASE="${ZATOR_RELEASE:-latest}"
ZATOR_ROOT="${ZATOR_ROOT:-/opt/zator}"
ZATOR_BIN_DIR="${ZATOR_BIN_DIR:-$ZATOR_ROOT/bin}"
ZATOR_MIRROR="${ZATOR_MIRROR:-}"

umask 022

die() { echo "zator-launcher: $*" >&2; exit 1; }

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
    ppc|ppc32|ppc64|ppc64le) echo ppc ;;
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
asset="zator-linux-${arch}"
install_dir="$ZATOR_BIN_DIR"
mkdir -p "$install_dir"
tmp="$install_dir/.${asset}.$$"
trap 'rm -f "$tmp"' EXIT INT HUP

url=$(asset_url "$asset")
echo "zator-launcher: arch=${arch}"
echo "zator-launcher: download ${url}"
download "$url" "$tmp" || die "не удалось скачать ${url}"
chmod 755 "$tmp"
mv -f "$tmp" "$install_dir/zator"
trap - EXIT INT HUP

echo "zator-launcher: установка / обновление (zapret2=latest, автозапуск)..."
"$install_dir/zator" install -zapret=true -zapret-version latest "$@" || die "install завершился с ошибкой"

# На Entware init может не держать webui в foreground — поднимаем демоном при необходимости.
if ! pidof zator >/dev/null 2>&1; then
  nohup "$install_dir/zator" webui >/tmp/zator-webui.log 2>&1 &
fi

echo "zator-launcher: готово"
"$install_dir/zator" status || true
exit 0
