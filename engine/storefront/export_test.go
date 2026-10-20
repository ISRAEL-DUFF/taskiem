package storefront

import "github.com/google/uuid"

// PaymentKey is paymentKey, for the tests outside the package.
func PaymentKey(id uuid.UUID) string { return paymentKey(id) }
