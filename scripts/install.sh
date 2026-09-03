#!/usr/bin/env bash
# Installs worksome-cli — https://github.com/worksome/worksome-cli
#
#   curl -fsSL https://raw.githubusercontent.com/worksome/worksome-cli/main/scripts/install.sh | bash
#
# What it does, in order:
#   1. If `worksome` is already on PATH, says where and exits 0.
#      Set WORKSOME_FORCE=1 to install anyway.
#   2. Downloads the release tarball for this OS and architecture from GitHub
#      Releases, verifies it against the release's checksums.txt, and installs
#      the binary into the first writable directory of:
#        $WORKSOME_INSTALL_DIR, /usr/local/bin, ~/.local/bin, ~/bin
#   3. If no release asset fits this machine, or curl is missing, falls back to
#      `go install` when a Go toolchain is present.
#
# Environment:
#   WORKSOME_VERSION      release tag to install, e.g. v0.6.2 (default: latest)
#   WORKSOME_INSTALL_DIR  directory to install into (created if needed)
#   WORKSOME_FORCE=1      install even if worksome is already on PATH
#   WORKSOME_RELEASE_BASE base URL holding the release assets and checksums.txt,
#                         for mirrors and air-gapped installs (default: GitHub)
#
# On success prints one line to stdout:
#   installed worksome <version> at <path>      or
#   already installed at <path>
# and exits 0. Anything else exits non-zero with the reason on stderr.
#
# The script is idempotent and safe to re-run. Read it before you pipe it.

set -uo pipefail

REPO="worksome/worksome-cli"
BIN="worksome"

log() { printf 'install.sh: %s\n' "$*" >&2; }

# --- 1. already installed? -------------------------------------------------
if [ "${WORKSOME_FORCE:-0}" != "1" ] && command -v "$BIN" >/dev/null 2>&1; then
  printf 'already installed at %s\n' "$(command -v "$BIN")"
  exit 0
fi

# --- pick an install dir on PATH -------------------------------------------
pick_dir() {
  local d
  for d in "${WORKSOME_INSTALL_DIR:-}" /usr/local/bin "$HOME/.local/bin" "$HOME/bin"; do
    [ -n "$d" ] || continue
    if [ -d "$d" ] && [ -w "$d" ]; then printf '%s' "$d"; return 0; fi
    if [ ! -e "$d" ] && mkdir -p "$d" 2>/dev/null; then printf '%s' "$d"; return 0; fi
  done
  return 1
}

DEST="$(pick_dir)" || {
  log "no writable install directory among ${WORKSOME_INSTALL_DIR:+\$WORKSOME_INSTALL_DIR, }/usr/local/bin, ~/.local/bin, ~/bin"
  exit 1
}

# --- detect platform --------------------------------------------------------
case "$(uname -s)" in
  Linux)  OS=linux ;;
  Darwin) OS=darwin ;;
  MINGW*|MSYS*|CYGWIN*)
    log "Windows detected. Download worksome_windows_<arch>.zip from https://github.com/$REPO/releases and put worksome.exe on your PATH."
    exit 1 ;;
  *) OS="" ;;
esac
case "$(uname -m)" in
  x86_64|amd64)  ARCH=amd64 ;;
  aarch64|arm64) ARCH=arm64 ;;
  *)             ARCH="" ;;
esac

# sha256 of a file, with whichever tool this machine has. Empty when neither.
sha256_of() {
  if command -v sha256sum >/dev/null 2>&1; then sha256sum "$1" | awk '{print $1}'
  elif command -v shasum >/dev/null 2>&1; then shasum -a 256 "$1" | awk '{print $1}'
  fi
}

# --- 2. prebuilt release binary --------------------------------------------
if [ -n "$OS" ] && [ -n "$ARCH" ] && command -v curl >/dev/null 2>&1; then
  ASSET="${BIN}_${OS}_${ARCH}.tar.gz"
  if [ -n "${WORKSOME_RELEASE_BASE:-}" ]; then
    BASE="${WORKSOME_RELEASE_BASE%/}"
  elif [ -n "${WORKSOME_VERSION:-}" ]; then
    BASE="https://github.com/${REPO}/releases/download/${WORKSOME_VERSION}"
  else
    BASE="https://github.com/${REPO}/releases/latest/download"
  fi
  TMP="$(mktemp -d)"
  trap 'rm -rf "$TMP"' EXIT

  if curl -fsSL -o "$TMP/$ASSET" "$BASE/$ASSET"; then
    # Verify against the release's checksums.txt. A mismatch is fatal. A missing
    # checksum file or tool is reported, since neither should happen on a
    # normal machine, but does not block the install.
    if curl -fsSL -o "$TMP/checksums.txt" "$BASE/checksums.txt"; then
      want="$(awk -v a="$ASSET" '$2 == a || $2 == "*"a {print $1}' "$TMP/checksums.txt" | head -1)"
      got="$(sha256_of "$TMP/$ASSET")"
      if [ -z "$got" ]; then
        log "warning: neither sha256sum nor shasum found, skipping checksum verification"
      elif [ -z "$want" ]; then
        log "warning: $ASSET is not listed in checksums.txt, skipping checksum verification"
      elif [ "$want" != "$got" ]; then
        log "checksum mismatch for $ASSET (expected $want, got $got). Refusing to install."
        exit 1
      fi
    else
      log "warning: could not download checksums.txt, skipping checksum verification"
    fi

    if tar -xzf "$TMP/$ASSET" -C "$TMP" "$BIN" 2>/dev/null && install -m 0755 "$TMP/$BIN" "$DEST/$BIN" 2>/dev/null; then
      version="$("$DEST/$BIN" version 2>/dev/null | awk 'NR==1 {print $2}')"
      printf 'installed worksome %s at %s\n' "${version:-?}" "$DEST/$BIN"
      case ":$PATH:" in
        *":$DEST:"*) ;;
        *) log "note: $DEST is not on your PATH. Add it, e.g.  export PATH=\"$DEST:\$PATH\"" ;;
      esac
      exit 0
    fi
    log "downloaded $ASSET but could not unpack or install it; falling back to go install"
  else
    log "no release asset at $BASE/$ASSET; falling back to go install"
  fi
fi

# --- 3. go install ----------------------------------------------------------
if command -v go >/dev/null 2>&1; then
  # Go fetches a matching toolchain if the module needs a newer one; this can
  # take a couple of minutes on a cold machine.
  if go install "github.com/${REPO}/cmd/${BIN}@${WORKSOME_VERSION:-latest}" >&2; then
    GOBIN="$(go env GOBIN)"; [ -n "$GOBIN" ] || GOBIN="$(go env GOPATH)/bin"
    if [ -x "$GOBIN/$BIN" ]; then
      case ":$PATH:" in
        *":$GOBIN:"*) ;;
        *) install -m 0755 "$GOBIN/$BIN" "$DEST/$BIN" 2>/dev/null && GOBIN="$DEST" ;;
      esac
      version="$("$GOBIN/$BIN" version 2>/dev/null | awk 'NR==1 {print $2}')"
      printf 'installed worksome %s at %s\n' "${version:-?}" "$GOBIN/$BIN"
      exit 0
    fi
  fi
fi

log "could not install $BIN: no release asset for $(uname -s)/$(uname -m) and no Go toolchain."
log "Prebuilt binaries: https://github.com/$REPO/releases"
exit 1
