#!/usr/bin/env bash

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
APP_NAME="nexus-trade-bot"

cd "$ROOT_DIR"

extract_version() {
  grep -E '^var Version = "[^"]+"' main.go | sed -E 's/^var Version = "([^"]+)"$/\1/'
}

normalize_goos() {
  case "$1" in
    MacOS|macOS|macos|darwin)
      echo "darwin"
      ;;
    Windows|windows)
      echo "windows"
      ;;
    Linux|linux)
      echo "linux"
      ;;
    *)
      echo "$1"
      ;;
  esac
}

normalize_goarch() {
  case "$1" in
    x86_64)
      echo "amd64"
      ;;
    aarch64)
      echo "arm64"
      ;;
    *)
      echo "$1"
      ;;
  esac
}

VERSION="${VERSION:-$(extract_version)}"
TARGET_OS="${TARGET_OS:-${GOOS:-linux}}"
TARGET_ARCH="${TARGET_ARCH:-${GOARCH:-amd64}}"
GOOS="$(normalize_goos "$TARGET_OS")"
GOARCH="$(normalize_goarch "$TARGET_ARCH")"
DIST_DIR="$ROOT_DIR/dist"

package_os_name() {
  case "$GOOS" in
    darwin)
      echo "MacOS"
      ;;
    *)
      echo "$GOOS"
      ;;
  esac
}

PACKAGE_OS_NAME="${PACKAGE_OS_NAME:-$(package_os_name)}"
PACKAGE_BASENAME="${APP_NAME}_${VERSION}_${PACKAGE_OS_NAME}_${GOARCH}"
STAGE_DIR="$DIST_DIR/$PACKAGE_BASENAME"

archive_extension() {
  case "$GOOS" in
    windows)
      echo "zip"
      ;;
    *)
      echo "tar.gz"
      ;;
  esac
}

binary_name() {
  case "$GOOS" in
    windows)
      echo "${APP_NAME}.exe"
      ;;
    *)
      echo "$APP_NAME"
      ;;
  esac
}

sha256_file() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1"
    return
  fi

  if command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$1"
    return
  fi

  echo "缺少 sha256sum 或 shasum，无法生成校验文件" >&2
  exit 1
}

create_archive() {
  case "$ARCHIVE_EXT" in
    zip)
      (
        cd "$DIST_DIR"
        zip -qr "$ARCHIVE_PATH" "$PACKAGE_BASENAME"
      )
      ;;
    tar.gz)
      tar -C "$DIST_DIR" -czf "$ARCHIVE_PATH" "$PACKAGE_BASENAME"
      ;;
    *)
      echo "不支持的压缩格式: $ARCHIVE_EXT" >&2
      exit 1
      ;;
  esac
}

ARCHIVE_EXT="$(archive_extension)"
ARCHIVE_PATH="$DIST_DIR/${PACKAGE_BASENAME}.${ARCHIVE_EXT}"
CHECKSUM_PATH="$ARCHIVE_PATH.sha256"
BIN_PATH="$STAGE_DIR/$(binary_name)"

if [[ -z "$VERSION" ]]; then
  echo "无法从 main.go 提取版本号" >&2
  exit 1
fi

rm -rf "$STAGE_DIR" "$ARCHIVE_PATH" "$CHECKSUM_PATH"
mkdir -p "$STAGE_DIR"

echo "==> 构建 $APP_NAME $VERSION ($TARGET_OS/$TARGET_ARCH -> $GOOS/$GOARCH)"
CGO_ENABLED=0 GOOS="$GOOS" GOARCH="$GOARCH" go build -o "$BIN_PATH" .

echo "==> 准备发布包内容"
cp README.md "$STAGE_DIR/README.md"
cp config.example.yaml "$STAGE_DIR/config.example.yaml"
cp config.example.yaml "$STAGE_DIR/config.yaml"

if [[ -d docs ]]; then
  cp -R docs "$STAGE_DIR/docs"
fi

if [[ -d logo ]]; then
  cp -R logo "$STAGE_DIR/logo"
  find "$STAGE_DIR/logo" -name ".DS_Store" -delete
fi

mkdir -p "$STAGE_DIR/scripts"
cp scripts/nexus-trade-bot.sh "$STAGE_DIR/scripts/nexus-trade-bot.sh"
chmod +x "$STAGE_DIR/scripts/nexus-trade-bot.sh"

for optional_file in ARCHITECTURE.md 部署教程.pdf; do
  if [[ -f "$optional_file" ]]; then
    cp "$optional_file" "$STAGE_DIR/$optional_file"
  fi
done

if [[ -d live_server ]]; then
  cp -R live_server "$STAGE_DIR/live_server"
fi

echo "==> 打包归档"
create_archive
sha256_file "$ARCHIVE_PATH" > "$CHECKSUM_PATH"

echo "==> 发布包已生成"
echo "$ARCHIVE_PATH"
echo "$CHECKSUM_PATH"
