#!/usr/bin/env bash
# The parity test's emoji (web/e2e/parity.spec.ts): the pictures it keeps
# were drawn with Ubuntu 24.04's Noto Color Emoji, 2.047-0ubuntu0.24.04.1
# (as Playwright's own image has it). A machine with another release draws
# other emoji: this puts that one in its place, checked by its hash. Run as
# root (CI's web-e2e job does).
set -euo pipefail
version=2.047-0ubuntu0.24.04.1
deb_sha=b102b62ce6a7313315223cff5e052bdf8fc0ad162c9e961ce0dc2202bc139ce2
ttf_sha=93cdc4ee9aa40e2afceecc63da0ca05ec7aab4bec991ece51a6b52389f48a477
target=/usr/share/fonts/truetype/noto/NotoColorEmoji.ttf
if [ -f "$target" ] && echo "$ttf_sha  $target" | sha256sum -c --quiet 2>/dev/null; then
  echo "Noto Color Emoji $version already here"
  exit 0
fi
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
curl -sSfL -o "$work/f.deb" "http://archive.ubuntu.com/ubuntu/pool/main/f/fonts-noto-color-emoji/fonts-noto-color-emoji_${version}_all.deb"
echo "$deb_sha  $work/f.deb" | sha256sum -c --quiet
dpkg-deb -x "$work/f.deb" "$work/x"
echo "$ttf_sha  $work/x$target" | sha256sum -c --quiet
install -D -m 0644 "$work/x$target" "$target"
fc-cache -f >/dev/null
echo "Noto Color Emoji $version put in place"
