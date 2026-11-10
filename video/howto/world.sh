#!/usr/bin/env bash
# A fresh demo world for the how-to videos (video/howto/README.md): the
# demo server (web/e2e/demo/serve-demo.sh) with WhatsApp and the model
# switched on against the stand-ins (fakes.mjs), seeded. Then run the
# videos in order: each starts where the one before ends.
#
#   bash video/howto/world.sh
set -euo pipefail
root=$(cd "$(dirname "$0")/../.." && pwd)
cd "$root"
mkdir -p video/out/howto
pkill -f "bin/taskiem-e2e" || true
pkill -f "bin/demo-fakexenibase" || true
pkill -f "video/howto/fakes.mjs" || true
sleep 1
setsid nohup node video/howto/fakes.mjs >video/out/howto/fakes.out 2>&1 </dev/null &
eval "$(node video/howto/whatsapp.mjs env)"
export DEMO_PASS=127.0.0.1:12626,127.0.0.1:12930
setsid nohup bash web/e2e/demo/serve-demo.sh >video/out/howto/server.log 2>&1 </dev/null &
for _ in $(seq 150); do
  curl -sf localhost:18090/readyz >/dev/null && break
  sleep 2
done
curl -sf localhost:18090/readyz >/dev/null || { echo "the demo server did not start: video/out/howto/server.log" >&2; exit 1; }
node web/e2e/demo/seed.ts >video/out/howto/seed.log
echo "demo world ready at http://localhost:18090"
