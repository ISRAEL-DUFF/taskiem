#!/usr/bin/env bash
# Install Taskiem on a fresh Ubuntu or Debian VPS (decision 0032, docs/vm.md):
#
#   curl -fsSL https://raw.githubusercontent.com/ISRAEL-DUFF/taskiem/main/deploy/vm/install.sh | sudo bash
#
# It installs Docker (from Docker's signed apt repository), cosign and the
# firewall rules, puts the deployment kit in /opt/taskiem and taskiem-vps in
# /usr/local/bin, then runs `taskiem-vps setup`, which asks a few questions
# and does the rest.
#
# The kit is taken from the release named below (KIT_REF) and must match
# its checksum (KIT_SHA256). Options (environment): TASKIEM_VPS_REF with
# TASKIEM_VPS_KIT_SHA256 (another git ref of the kit and its tarball's
# sha256), TASKIEM_VPS_NO_FIREWALL=1, TASKIEM_VPS_RESTORE=1 (rebuild from a backup
# instead of a new install), and every TASKIEM_VPS_* answer that setup takes
# (docs/vm.md) for an unattended install.
set -Eeuo pipefail

REPO_SLUG=ISRAEL-DUFF/taskiem
# The release this installer installs: a version tag, and the sha256 of
# https://codeload.github.com/ISRAEL-DUFF/taskiem/tar.gz/<tag>. Cutting a
# release fills both in (docs/vm.md#the-installers-pins). While they are
# empty no release has been pinned yet, and the installer falls back to the
# main branch, unverified, and says so.
KIT_REF=""
KIT_SHA256=""
REF=${TASKIEM_VPS_REF:-$KIT_REF}
if [ "$REF" = "$KIT_REF" ]; then SHA256=${TASKIEM_VPS_KIT_SHA256:-$KIT_SHA256}; else SHA256=${TASKIEM_VPS_KIT_SHA256:-}; fi
KIT=${TASKIEM_VPS_KIT:-/opt/taskiem}
COSIGN_VERSION=v3.1.3
# Docker's package signing key (https://docs.docker.com/engine/install/ubuntu/).
DOCKER_KEY_FPR=9DC858229FC7DD38854AE2D88D81803C0EBFCD88

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

# Docker from its own apt repository, as its documentation installs it: the
# repository's key is checked against the fingerprint above, and apt then
# checks every package against that key. (Not the get.docker.com script,
# which is unsigned and runs whatever it is served, as root.)
install_docker() {
  local id codename tmp fpr
  # shellcheck source=/dev/null
  id=$(. /etc/os-release && printf '%s' "$ID")
  # shellcheck source=/dev/null
  codename=$(. /etc/os-release && printf '%s' "${VERSION_CODENAME:-}")
  case $id in ubuntu | debian) ;; *) die "Docker is not installed and this installer adds it only on Ubuntu and Debian: install Docker Engine with the Compose and buildx plugins, then run this again" ;; esac
  [ -n "$codename" ] || die "cannot tell this system's release name (/etc/os-release): install Docker Engine by hand, then run this again"
  apt-get install -y -qq gnupg >/dev/null
  tmp=$(mktemp -d)
  curl -fsSL -o "$tmp/docker.asc" "https://download.docker.com/linux/$id/gpg"
  fpr=$(gpg --show-keys --with-colons "$tmp/docker.asc" 2>/dev/null | awk -F: '$1 == "fpr" { print $10; exit }')
  [ "$fpr" = "$DOCKER_KEY_FPR" ] || die "the key served for Docker's apt repository is not Docker's ($fpr): not installing"
  install -m 0755 -d /etc/apt/keyrings
  install -m 0644 "$tmp/docker.asc" /etc/apt/keyrings/docker.asc
  rm -rf "$tmp"
  printf 'deb [arch=%s signed-by=/etc/apt/keyrings/docker.asc] https://download.docker.com/linux/%s %s stable\n' \
    "$(dpkg --print-architecture)" "$id" "$codename" >/etc/apt/sources.list.d/docker.list
  apt-get update -qq
  apt-get install -y -qq docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin >/dev/null
}

# The kit's tarball for REF into DIR, refused unless it matches SHA256.
# Without a checksum there is nothing to check it against: that is allowed
# only for the unpinned fallback, with a warning.
fetch_kit() { # REF SHA256 DIR
  local ref=$1 sum=$2 dir=$3 tgz got
  tgz=$(mktemp)
  curl -fsSL -o "$tgz" "https://codeload.github.com/$REPO_SLUG/tar.gz/$ref" || { rm -f "$tgz"; die "could not download the deployment kit ($ref)"; }
  if [ -n "$sum" ]; then
    got=$(sha256sum "$tgz" | cut -d' ' -f1)
    [ "$got" = "$sum" ] || { rm -f "$tgz"; die "the deployment kit ($ref) does not match its checksum: expected $sum, got $got. Not installing"; }
  else
    log "WARNING: the deployment kit ($ref) has no checksum to be checked against; installing it unverified"
  fi
  tar -xzf "$tgz" -C "$dir" --strip-components=3 --wildcards "*/deploy/vm"
  rm -f "$tgz"
}

if ! command -v docker >/dev/null; then
  log "installing Docker"
  install_docker
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

mkdir -p "$KIT"
src=$(mktemp -d)
if [ -n "${TASKIEM_VPS_KIT_SOURCE:-}" ]; then
  log "installing the deployment kit from $TASKIEM_VPS_KIT_SOURCE into $KIT"
  cp -R "$TASKIEM_VPS_KIT_SOURCE"/. "$src"/
else
  if [ -z "$REF" ]; then
    log "WARNING: this installer names no release yet: taking the deployment kit from the main branch"
    REF=main
  elif [ -z "$SHA256" ]; then
    die "TASKIEM_VPS_REF=$REF needs TASKIEM_VPS_KIT_SHA256, the sha256 of that ref's tarball, so the kit can be checked"
  fi
  log "installing the deployment kit ($REF) into $KIT"
  fetch_kit "$REF" "$SHA256" "$src"
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
