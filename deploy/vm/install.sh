#!/usr/bin/env bash
# Install Taskiem on a fresh Ubuntu or Debian VPS (decision 0032, docs/vm.md):
#
#   curl -fsSL https://raw.githubusercontent.com/ISRAEL-DUFF/taskiem/main/deploy/vm/install.sh | sudo bash
#
# It installs Docker, cosign and the firewall rules, puts the deployment kit
# in /opt/taskiem and taskiem-vps in /usr/local/bin, then runs
# `taskiem-vps setup`, which asks a few questions and does the rest.
#
# Options (environment): TASKIEM_VPS_REF (git ref of the kit, default main),
# TASKIEM_VPS_NO_FIREWALL=1, TASKIEM_VPS_RESTORE=1 (rebuild from a backup
# instead of a new install), and every TASKIEM_VPS_* answer that setup takes
# (docs/vm.md) for an unattended install.
set -Eeuo pipefail

REPO_SLUG=ISRAEL-DUFF/taskiem
REF=${TASKIEM_VPS_REF:-main}
KIT=${TASKIEM_VPS_KIT:-/opt/taskiem}
COSIGN_VERSION=v3.1.3

log() { printf '==> %s\n' "$*" >&2; }
die() { printf 'ERROR: %s\n' "$*" >&2; exit 1; }

[ "$(id -u)" = 0 ] || die "run as root: curl -fsSL … | sudo bash"
[ "$(uname -s)" = Linux ] || die "Linux only"
case $(uname -m) in
x86_64) ARCH=amd64 ;;
*) die "Taskiem's image is published for x86_64 (amd64) only; this machine is $(uname -m)" ;;
esac
command -v apt-get >/dev/null || die "this installer supports Ubuntu and Debian (apt)"

log "installing packages"
export DEBIAN_FRONTEND=noninteractive
apt-get update -qq
apt-get install -y -qq curl ca-certificates jq openssl tar coreutils util-linux >/dev/null
[ -n "${TASKIEM_VPS_NO_FIREWALL:-}" ] || apt-get install -y -qq ufw >/dev/null

if ! command -v docker >/dev/null; then
  log "installing Docker"
  curl -fsSL https://get.docker.com | sh >/dev/null
fi
systemctl enable --now docker >/dev/null 2>&1 || true
docker compose version >/dev/null || die "the Docker Compose plugin is missing"
docker buildx version >/dev/null || die "the Docker buildx plugin is missing"

if ! cosign version 2>/dev/null | grep -q "${COSIGN_VERSION#v}"; then
  log "installing cosign $COSIGN_VERSION (verifies that releases come from Taskiem's CI)"
  tmp=$(mktemp -d)
  base="https://github.com/sigstore/cosign/releases/download/$COSIGN_VERSION"
  curl -fsSL -o "$tmp/cosign-linux-$ARCH" "$base/cosign-linux-$ARCH"
  curl -fsSL -o "$tmp/cosign_checksums.txt" "$base/cosign_checksums.txt"
  (cd "$tmp" && grep " cosign-linux-$ARCH\$" cosign_checksums.txt | sha256sum -c --quiet) || die "cosign download does not match its published checksum"
  install -m 0755 "$tmp/cosign-linux-$ARCH" /usr/local/bin/cosign
  rm -rf "$tmp"
fi

if [ -z "${TASKIEM_VPS_NO_FIREWALL:-}" ]; then
  # Keep the SSH port this session came in on, whatever it is.
  ssh_port=$(awk '{print $4}' <<<"${SSH_CONNECTION:-}")
  log "firewall: SSH (${ssh_port:-22}), HTTP and HTTPS in; everything else closed"
  ufw allow "${ssh_port:-22}/tcp" >/dev/null
  ufw allow 80/tcp >/dev/null
  ufw allow 443/tcp >/dev/null
  ufw allow 443/udp >/dev/null
  ufw --force enable >/dev/null
fi

log "installing the deployment kit ($REF) into $KIT"
mkdir -p "$KIT"
src=$(mktemp -d)
if [ -n "${TASKIEM_VPS_KIT_SOURCE:-}" ]; then
  cp -R "$TASKIEM_VPS_KIT_SOURCE"/. "$src"/
else
  curl -fsSL "https://codeload.github.com/$REPO_SLUG/tar.gz/$REF" | tar -xz -C "$src" --strip-components=3 --wildcards "*/deploy/vm"
fi
install -m 0755 "$src/taskiem-vps" /usr/local/bin/taskiem-vps
taskiem-vps _install-kit "$src"
rm -rf "$src"

if [ -n "${TASKIEM_VPS_RESTORE:-}" ]; then
  log "restoring Taskiem from a backup"
  exec taskiem-vps restore
fi
log "setting up Taskiem"
exec taskiem-vps setup
