package storefront

import "github.com/google/uuid"

// PaymentKey is paymentKey, for the tests outside the package.
func PaymentKey(id uuid.UUID) string { return paymentKey(id) }

// CountedFrom is countedFrom, for the tests outside the package.
func CountedFrom(from Source, catalogue *uuid.UUID) bool { return countedFrom(from, catalogue) }
