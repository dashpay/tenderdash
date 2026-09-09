package types

import "errors"

// ErrVerificationBudgetExhausted is returned before a signature check when the
// node-wide verification budget has no permit for that work.
var ErrVerificationBudgetExhausted = errors.New("verification budget exhausted")

// VerificationBudget grants permits denominated in signature-verification
// operations. Implementations must be safe for concurrent use.
type VerificationBudget interface {
	Allow(cost int) bool
}
