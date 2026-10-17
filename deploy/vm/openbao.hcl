# OpenBao for Taskiem's envelope encryption (spec 14.1), on one VM.
# Raft storage on the openbao_data volume. Written by taskiem-vps; backed up
# nightly without the unseal key, which lives only in openbao-unseal.env and
# in the offline recovery sheet (docs/vm.md). Losing the storage, or the
# unseal key, loses every secret Taskiem holds.
ui = false

# Integrated (Raft) storage, one node: OpenBao 2 has no plain file backend.
# The nightly backup stops OpenBao for a few seconds to copy it consistently.
storage "raft" {
  path    = "/openbao/file"
  node_id = "taskiem"
}

cluster_addr = "http://openbao:8201"

# Recommended with integrated storage (its database is memory-mapped).
disable_mlock = true

# Auto-unseal with a static key (OpenBao 2.4+), hex in an environment
# variable read from the root-only openbao-unseal.env.
seal "static" {
  current_key_id = "1"
  current_key    = "env://BAO_STATIC_UNSEAL_KEY"
}

# Reachable only on the Compose network (the port is not published), so
# plain HTTP between Taskiem and OpenBao on the same host.
listener "tcp" {
  address     = "0.0.0.0:8200"
  tls_disable = true
}

api_addr = "http://openbao:8200"
