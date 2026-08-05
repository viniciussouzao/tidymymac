#!/bin/sh
# tidymymac installer
#
# Usage:
#   curl -fsSL https://raw.githubusercontent.com/viniciussouzao/tidymymac/main/install.sh | sh
#
# Env overrides:
#   TIDYMYMAC_VERSION      pin a specific release tag, e.g. v1.1 (default: latest)
#   TIDYMYMAC_INSTALL_DIR  override install directory (default: auto-detected)
#   TIDYMYMAC_DRY_RUN      set to 1 to print resolved settings and exit without installing
#   TIDYMYMAC_BASE_URL     override the release download base URL (for local testing)
set -eu

REPO="viniciussouzao/tidymymac"
BINARY_NAME="tidymymac"
BASE_URL="${TIDYMYMAC_BASE_URL:-https://github.com/${REPO}/releases/download}"
API_URL="https://api.github.com/repos/${REPO}/releases/latest"

info() { printf '==> %s\n' "$*"; }
warn() { printf 'Warning: %s\n' "$*" >&2; }
err() {
	printf 'Error: %s\n' "$*" >&2
	exit 1
}
need_cmd() {
	command -v "$1" >/dev/null 2>&1 || err "required command '$1' not found"
}

detect_platform() {
	os=$(uname -s)
	if [ "$os" != "Darwin" ]; then
		err "tidymymac only supports macOS. Detected: $os"
	fi

	machine=$(uname -m)
	case "$machine" in
		arm64) ARCH="arm64" ;;
		x86_64) ARCH="amd64" ;;
		*) err "unsupported architecture: $machine" ;;
	esac
}

resolve_version() {
	if [ -n "${TIDYMYMAC_VERSION:-}" ]; then
		VERSION="$TIDYMYMAC_VERSION"
		return
	fi

	info "Resolving latest release..."
	latest=$(curl --proto '=https' --tlsv1.2 -fsSL "$API_URL" |
		grep '"tag_name":' | head -n1 | sed -E 's/.*"tag_name": *"([^"]+)".*/\1/')
	[ -n "$latest" ] || err "could not resolve latest release from $API_URL"
	VERSION="$latest"
}

resolve_install_dir() {
	SUDO=""

	if [ -n "${TIDYMYMAC_INSTALL_DIR:-}" ]; then
		INSTALL_DIR="$TIDYMYMAC_INSTALL_DIR"
		mkdir -p "$INSTALL_DIR" 2>/dev/null || err "cannot create $INSTALL_DIR"
		[ -w "$INSTALL_DIR" ] || err "$INSTALL_DIR is not writable"
		return
	fi

	if [ -d /usr/local/bin ] && [ -w /usr/local/bin ]; then
		INSTALL_DIR="/usr/local/bin"
		return
	fi

	candidate="$HOME/.local/bin"
	if mkdir -p "$candidate" 2>/dev/null && [ -w "$candidate" ]; then
		INSTALL_DIR="$candidate"
		warn "installing to $INSTALL_DIR — make sure it's on your PATH"
		return
	fi

	if [ -t 1 ]; then
		INSTALL_DIR="/usr/local/bin"
		SUDO="sudo"
		mkdir -p "$INSTALL_DIR" 2>/dev/null || true
		warn "installing to $INSTALL_DIR requires sudo"
	else
		err "no writable install directory found; set TIDYMYMAC_INSTALL_DIR to choose one"
	fi
}

download_and_verify() {
	TMP_DIR=$(mktemp -d)
	trap 'rm -rf "$TMP_DIR"' EXIT INT TERM

	asset="tidymymac-${VERSION}-darwin-${ARCH}.tar.gz"
	url="${BASE_URL}/${VERSION}/${asset}"
	checksums_url="${BASE_URL}/${VERSION}/checksums.txt"

	info "Downloading $asset ($VERSION)..."
	curl --proto '=https' --tlsv1.2 -fsSL -o "$TMP_DIR/$asset" "$url" ||
		err "failed to download $url (does version $VERSION exist?)"

	info "Verifying checksum..."
	curl --proto '=https' --tlsv1.2 -fsSL -o "$TMP_DIR/checksums.txt" "$checksums_url" ||
		err "failed to download checksums.txt for $VERSION"

	expected=$(grep " ${asset}\$" "$TMP_DIR/checksums.txt" | awk '{print $1}')
	[ -n "$expected" ] || err "no checksum entry for $asset in checksums.txt"

	actual=$(shasum -a 256 "$TMP_DIR/$asset" | awk '{print $1}')
	[ "$expected" = "$actual" ] || err "checksum mismatch for $asset (expected $expected, got $actual)"

	info "Extracting..."
	tar -xzf "$TMP_DIR/$asset" -C "$TMP_DIR"

	EXTRACTED_BIN="$TMP_DIR/tidymymac-darwin-${ARCH}"
	[ -f "$EXTRACTED_BIN" ] || err "expected binary not found in archive: tidymymac-darwin-${ARCH}"
}

install_binary() {
	chmod +x "$EXTRACTED_BIN"

	DEST="$INSTALL_DIR/$BINARY_NAME"
	# shellcheck disable=SC2086
	$SUDO mkdir -p "$INSTALL_DIR"
	# shellcheck disable=SC2086
	$SUDO cp "$EXTRACTED_BIN" "$DEST"
	# shellcheck disable=SC2086
	$SUDO xattr -d com.apple.quarantine "$DEST" 2>/dev/null || true
}

print_summary() {
	info "tidymymac $VERSION installed to $DEST"

	case ":$PATH:" in
		*":$INSTALL_DIR:"*) ;;
		*)
			warn "$INSTALL_DIR is not on your PATH"
			printf '    export PATH="%s:$PATH"\n' "$INSTALL_DIR" >&2
			;;
	esac

	"$DEST" version 2>/dev/null || true
}

dry_run() {
	detect_platform
	resolve_version
	resolve_install_dir
	info "os=Darwin arch=$ARCH version=$VERSION install_dir=$INSTALL_DIR"
	info "would download: ${BASE_URL}/${VERSION}/tidymymac-${VERSION}-darwin-${ARCH}.tar.gz"
	exit 0
}

main() {
	need_cmd curl
	need_cmd tar
	need_cmd shasum

	if [ "${TIDYMYMAC_DRY_RUN:-}" = "1" ]; then
		dry_run
	fi

	detect_platform
	resolve_version
	resolve_install_dir
	download_and_verify
	install_binary
	print_summary
}

main "$@"
