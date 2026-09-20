#!/bin/sh
# Install a verified husage release without sudo or changes to shell startup files.
set -eu

fail() {
    printf 'husage installer: %s\n' "$*" >&2
    exit 1
}

usage() {
    cat <<'HELP'
Usage: install.sh [--version VERSION] [--bin-dir DIRECTORY]

Downloads the latest stable husage release for Linux or macOS by default.
  --version VERSION    Install a specific release, e.g. v0.1.0 or 0.1.0.
  --bin-dir DIRECTORY  Install into this directory (default: ~/.local/bin).
  -h, --help           Show this help.
HELP
}

cleanup() {
    if [ -n "$husage_staged" ]; then rm -f "$husage_staged"; fi
    if [ -n "$husage_tmpdir" ]; then rm -rf "$husage_tmpdir"; fi
}

fetch() {
    curl --fail --silent --show-error --location \
        --proto '=https' --proto-redir '=https' \
        --retry 3 --connect-timeout 15 --max-time 120 "$@"
}

main() {
    husage_version=''
    husage_bindir=''
    while [ "$#" -gt 0 ]; do
        case "$1" in
            --version|--bin-dir)
                [ "$#" -ge 2 ] && [ -n "$2" ] || fail "$1 requires a value"
                case "$1" in
                    --version) husage_version=$2 ;;
                    --bin-dir) husage_bindir=$2 ;;
                esac
                shift 2
                ;;
            -h|--help) usage; exit 0 ;;
            *) fail "unknown option: $1 (use --help)" ;;
        esac
    done
    if [ -z "$husage_bindir" ]; then
        [ -n "${HOME:-}" ] || fail 'HOME is unset; use --bin-dir'
        husage_bindir=$HOME/.local/bin
    fi
    # An absolute destination also prevents options being interpreted as paths.
    case "$husage_bindir" in
        /*) ;;
        *) husage_bindir=$PWD/$husage_bindir ;;
    esac
    case "$(uname -s)" in
        Linux) husage_os=linux ;;
        Darwin) husage_os=darwin ;;
        *) fail 'supported systems are Linux and macOS; use the release ZIP on Windows' ;;
    esac
    case "$(uname -m)" in
        x86_64|amd64) husage_arch=amd64 ;;
        arm64|aarch64) husage_arch=arm64 ;;
        *) fail 'supported architectures are amd64 and arm64' ;;
    esac
    for husage_tool in curl tar awk grep mktemp; do
        command -v "$husage_tool" >/dev/null 2>&1 || fail "required command not found: $husage_tool"
    done
    if command -v sha256sum >/dev/null 2>&1; then
        husage_hash=sha256sum
    elif command -v shasum >/dev/null 2>&1; then
        husage_hash=shasum
    else
        fail 'SHA-256 verification requires sha256sum or shasum'
    fi
    husage_base=https://github.com/franciscocpg/husage
    if [ -z "$husage_version" ]; then
        husage_latest=$(fetch --output /dev/null --write-out '%{url_effective}' "$husage_base/releases/latest") || fail 'cannot resolve the latest release'
        case "$husage_latest" in
            "$husage_base"/releases/tag/v*) husage_version=${husage_latest##*/} ;;
            *) fail 'latest release did not resolve to a version tag' ;;
        esac
    fi
    case "$husage_version" in v*) ;; *) husage_version=v$husage_version ;; esac
    printf '%s\n' "$husage_version" | grep -Eq '^v[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?(\+[0-9A-Za-z.-]+)?$' || fail 'version must be a semantic version such as v0.1.0'
    husage_asset=husage_${husage_version#v}_${husage_os}_${husage_arch}.tar.gz
    husage_url=$husage_base/releases/download/$husage_version
    husage_tmpdir=''
    husage_staged=''
    trap cleanup 0
    trap 'exit 1' HUP INT TERM
    husage_tmpdir=$(mktemp -d "${TMPDIR:-/tmp}/husage-install.XXXXXX") || fail 'cannot create temporary directory'
    printf 'Downloading husage %s for %s/%s…\n' "$husage_version" "$husage_os" "$husage_arch"
    fetch --output "$husage_tmpdir/$husage_asset" "$husage_url/$husage_asset" || fail 'release archive download failed'
    fetch --output "$husage_tmpdir/checksums.txt" "$husage_url/checksums.txt" || fail 'checksum download failed'
    husage_expected=$(awk -v name="$husage_asset" '$2 == name { print $1; found++ } END { if (found != 1) exit 1 }' "$husage_tmpdir/checksums.txt") || fail 'archive must have exactly one checksum entry'
    printf '%s\n' "$husage_expected" | grep -Eq '^[0-9a-f]{64}$' || fail 'invalid release checksum'
    if [ "$husage_hash" = sha256sum ]; then
        husage_digest=$(sha256sum "$husage_tmpdir/$husage_asset") || fail 'cannot calculate checksum'
    else
        husage_digest=$(shasum -a 256 "$husage_tmpdir/$husage_asset") || fail 'cannot calculate checksum'
    fi
    husage_actual=${husage_digest%% *}
    [ "$husage_actual" = "$husage_expected" ] || fail 'checksum mismatch; existing installation was left unchanged'
    tar -xzf "$husage_tmpdir/$husage_asset" -C "$husage_tmpdir" husage || fail 'cannot extract release binary'
    [ -f "$husage_tmpdir/husage" ] && [ ! -L "$husage_tmpdir/husage" ] || fail 'release binary is not a regular file'
    mkdir -p "$husage_bindir" || fail 'cannot create install directory; choose a writable --bin-dir'
    [ ! -d "$husage_bindir/husage" ] && [ ! -L "$husage_bindir/husage" ] || fail 'install destination is a directory or symlink; it was left unchanged'
    husage_staged=$(mktemp "$husage_bindir/.husage.XXXXXX") || fail 'install directory is not writable; choose another --bin-dir'
    cp "$husage_tmpdir/husage" "$husage_staged"
    chmod 755 "$husage_staged"
    if [ "$husage_os" = darwin ]; then
        /usr/bin/xattr -dr com.apple.quarantine "$husage_staged"
    fi
    mv -f "$husage_staged" "$husage_bindir/husage"
    husage_staged=''
    printf 'Installed husage %s to %s/husage\n' "$husage_version" "$husage_bindir"
    case ":$PATH:" in
        *":$husage_bindir:"*) ;;
        *) printf 'Add %s to your PATH, or run %s/husage directly.\n' "$husage_bindir" "$husage_bindir" ;;
    esac
}

# Keep execution at the end so a truncated download cannot run a partial main.
main "$@"
