package payments

import "fmt"

// DeclineError is a card decline reported by the payment provider, carrying
// enough of the issuer's reason for the caller to decide whether retrying the
// same credentials could ever work.
//
// This exists for one reason: dunning. A subscription renewal that fails
// because the customer's balance was low should be retried in a few days; one
// that fails because the card was reported stolen must never be retried at all.
// The card networks are explicit about the difference — Visa and Mastercard
// both fine per excess retry against a hard decline, and repeated attempts on a
// blocked number make the issuer more likely to decline the customer's *next*
// legitimate charge too.
//
// Only card declines produce this type. Network failures, API errors, and
// anything else stay ordinary wrapped errors: those say nothing about the card,
// and the caller should treat them as transient.
type DeclineError struct {
	// Code is the provider's error code, e.g. "card_declined".
	Code string
	// DeclineCode is the issuer's reason, e.g. "lost_card". Empty when the
	// provider gave a decline without one.
	DeclineCode string
	// Message is the provider's customer-safe explanation. Safe to show a
	// customer; not safe to rely on for control flow (wording changes).
	Message string
}

func (e *DeclineError) Error() string {
	if e.DeclineCode != "" {
		return fmt.Sprintf("card declined (%s/%s): %s", e.Code, e.DeclineCode, e.Message)
	}
	return fmt.Sprintf("card declined (%s): %s", e.Code, e.Message)
}

// hardDeclineCodes are the issuer reasons that mean the credentials themselves
// are dead. Retrying any of these on the same card cannot succeed — only a
// different payment method will.
//
// Everything not listed here is treated as soft on purpose. The costly mistake
// is misreading a recoverable decline as permanent and killing a subscription
// that one retry would have saved, so the default is to retry.
//
// Two judgement calls worth naming:
//   - expired_card is soft. Issuers usually reissue on the same number with a
//     new expiry, and Stripe's Account Updater can repair the stored card
//     without the customer lifting a finger, so a later attempt often clears.
//   - do_not_honor and generic_decline are soft despite being the most common
//     decline reasons overall. They are the issuer declining to explain itself,
//     not a statement that the card is dead, and they do frequently clear.
var hardDeclineCodes = map[string]bool{
	"lost_card":                        true,
	"stolen_card":                      true,
	"pickup_card":                      true,
	"restricted_card":                  true,
	"revocation_of_authorization":      true,
	"revocation_of_all_authorizations": true,
	"do_not_try_again":                 true,
	"invalid_account":                  true,
	"no_account":                       true,
	"card_not_supported":               true,
	"transaction_not_allowed":          true,
	"stop_payment_order":               true,
}

// Permanent reports whether this decline means the card is dead and no retry on
// it can succeed. Callers should stop charging when it is true — but note that
// stopping the *charges* is not the same as giving up on the customer, who can
// still fix the problem by putting a different card on file.
func (e *DeclineError) Permanent() bool {
	return hardDeclineCodes[e.DeclineCode]
}
