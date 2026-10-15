# OpenBao for Taskiem's envelope encryption (spec 14.1), on one VM.
# File storage on the openbao_data volume. Written by taskiem-vps; backed up
# nightly without the unseal key, which lives only in openbao-unseal.env and
# in the offline recovery sheet (docs/vm.md). Losing the storage, or the
# unseal key, loses every secret Taskiem holds.
ui = false

storage "file" {
  path = "/openbao/file"
}

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
