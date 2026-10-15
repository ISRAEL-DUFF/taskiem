# Taskiem's token may encrypt and decrypt with its one transit key, and
# nothing else (TASKIEM_KMS_KEY, default "taskiem"). It cannot read,
# export, rotate or delete the key.
path "transit/encrypt/taskiem" {
  capabilities = ["update"]
}

path "transit/decrypt/taskiem" {
  capabilities = ["update"]
}

# Let the openbao-renew service keep the periodic token alive.
path "auth/token/renew-self" {
  capabilities = ["update"]
}
