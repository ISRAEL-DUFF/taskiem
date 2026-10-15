# OpenBao for Taskiem's envelope encryption (spec 14.1), on one VM.
# File storage on the openbao_data volume: back it up with the unseal keys
# kept apart (docs/vm.md). Losing both loses every secret Taskiem holds.
ui = false

storage "file" {
  path = "/openbao/file"
}

# Reachable only on the Compose network (the port is not published), so
# plain HTTP between Taskiem and OpenBao on the same host.
listener "tcp" {
  address     = "0.0.0.0:8200"
  tls_disable = true
}

api_addr = "http://openbao:8200"
