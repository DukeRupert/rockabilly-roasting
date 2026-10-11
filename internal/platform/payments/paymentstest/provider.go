// Package paymentstest is a payments.Provider for tests that drive a charge
// end to end: the subscription renewal and the workers that run it.
//
// It lives beside the interface, the way httptest lives beside net/http, so
// that the app and jobs test packages share one fake rather than each growing
// their own. The web package's fakePaymentProvider predates it and covers the
// checkout handlers; it is not this.
package paymentstest

import (
	"context"
	"errors"
	"reflect"
	"sync"

	"github.com/google/uuid"

	"github.com/dukerupert/hiri/internal/platform/payments"
)

// DefaultCard is the payment method a new Provider reports as the customer's
// default.
const DefaultCard = "pm_card_visa"

// Provider records what a renewal asked Stripe to do and answers from a
// script.
//
// payments.Provider is embedded and nil, so a call to anything not overridden
// here panics. A renewal that starts reaching a method nobody expected it to
// should say so loudly rather than take a zero value and leave the suite
// green.
//
// Set the exported fields before the code under test runs; the recorded calls
// are read through the methods, which lock.
type Provider struct {
	payments.Provider

	// DefaultPaymentMethod is what GetCustomer reports. Empty means the
	// customer has no default.
	DefaultPaymentMethod string
	// Attached is what ListPaymentMethods reports.
	Attached []payments.PaymentMethod
	// RefundErr, when set, fails every refund.
	RefundErr error
	// OnCreate, when set, runs inside CreatePaymentIntent before the outcome
	// is decided — the moment between a renewal's charge and its write. It is
	// called with the mutex released, so it may read back through the
	// Provider; a hook that re-entered under the lock would deadlock with no
	// diagnostic until go test's timeout.
	OnCreate func()

	mu       sync.Mutex
	script   []Outcome
	requests []payments.CreatePaymentIntentRequest
	charges  []payments.PaymentIntent
	intents  map[string]*payments.PaymentIntent
	refunds  []payments.RefundRequest
	canceled []string
	refunded map[string]bool
	// keyed is what Stripe stores under an idempotency key: the request that
	// first used it and the answer it got.
	keyed map[string]keyedResult
	// executed counts the creates that reached the card network rather than
	// being answered from a stored key.
	executed int
}

type keyedResult struct {
	req payments.CreatePaymentIntentRequest
	pi  *payments.PaymentIntent
	err error
}

// New returns a Provider whose customer has DefaultCard on file and whose
// every charge succeeds until told otherwise.
func New() *Provider {
	return &Provider{DefaultPaymentMethod: DefaultCard}
}

// Outcome is what one CreatePaymentIntent call does.
type Outcome struct {
	charge bool
	err    error
}

// Succeed charges the card.
func Succeed() Outcome { return Outcome{charge: true} }

// Decline is the issuer refusing the card, as StripeProvider reports it. A
// code in payments' hard-decline list ("stolen_card") is permanent; anything
// else ("insufficient_funds") is soft.
func Decline(declineCode string) Outcome {
	return Outcome{err: &payments.DeclineError{
		Code:        "card_declined",
		DeclineCode: declineCode,
		Message:     "Your card was declined.",
	}}
}

// Fail is a call that never reached Stripe, or that Stripe refused before
// charging: an outage, a 5xx. Nothing is charged.
func Fail(err error) Outcome { return Outcome{err: err} }

// ChargeThenFail is the case a timeout cannot tell apart from Fail: Stripe
// charged the card and the response never arrived.
func ChargeThenFail(err error) Outcome { return Outcome{charge: true, err: err} }

// ErrTimeout is the error ChargeThenFail and Fail are most often given.
var ErrTimeout = errors.New("paymentstest: request timed out")

// ErrIdempotencyMismatch is Stripe refusing a key reused with different
// parameters. Like Stripe's, it is not a card error.
var ErrIdempotencyMismatch = errors.New("paymentstest: idempotency key reused with different parameters")

// Then queues outcomes for the next CreatePaymentIntent calls, in order. Once
// the queue is empty every call succeeds.
func (p *Provider) Then(outcomes ...Outcome) *Provider {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.script = append(p.script, outcomes...)
	return p
}

func (p *Provider) GetCustomer(_ context.Context, id string) (*payments.Customer, error) {
	return &payments.Customer{ID: id, DefaultPaymentMethodID: p.DefaultPaymentMethod}, nil
}

func (p *Provider) ListPaymentMethods(context.Context, string) ([]payments.PaymentMethod, error) {
	return p.Attached, nil
}

func (p *Provider) CreatePaymentIntent(_ context.Context, req payments.CreatePaymentIntentRequest) (*payments.PaymentIntent, error) {
	if p.OnCreate != nil {
		p.OnCreate()
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	p.requests = append(p.requests, req)

	// Stripe answers a key it has seen from what it stored, without charging
	// and without consuming anything: the same parameters replay the first
	// answer, success or decline; different ones are refused. Only a request
	// that executed is stored — one that never reached Stripe, or failed
	// before it charged, leaves the key free.
	if req.IdempotencyKey != "" {
		if prior, ok := p.keyed[req.IdempotencyKey]; ok {
			if !reflect.DeepEqual(prior.req, req) {
				return nil, ErrIdempotencyMismatch
			}
			if prior.err != nil {
				return nil, prior.err
			}
			got := *prior.pi
			return &got, nil
		}
	}

	outcome := Succeed()
	if len(p.script) > 0 {
		outcome, p.script = p.script[0], p.script[1:]
	}

	var pi *payments.PaymentIntent
	if outcome.charge {
		// A fresh id per intent, not a counter: these tests commit, and the id
		// lands on an order row under a unique index.
		pi = &payments.PaymentIntent{
			ID:              "pi_test_" + uuid.NewString(),
			Status:          payments.PaymentIntentStatusSucceeded,
			AmountCents:     req.AmountCents,
			Currency:        req.Currency,
			PaymentMethodID: req.PaymentMethodID,
			Metadata:        req.Metadata,
		}
		p.charges = append(p.charges, *pi)
		if p.intents == nil {
			p.intents = map[string]*payments.PaymentIntent{}
		}
		p.intents[pi.ID] = pi
	}
	var declined *payments.DeclineError
	executed := outcome.charge || errors.As(outcome.err, &declined)
	if executed {
		p.executed++
		if req.IdempotencyKey != "" {
			if p.keyed == nil {
				p.keyed = map[string]keyedResult{}
			}
			// A charge whose response was lost is stored as the success it
			// was: a retry under the same key gets the intent back.
			stored := keyedResult{req: req, pi: pi}
			if !outcome.charge {
				stored.err = outcome.err
			}
			p.keyed[req.IdempotencyKey] = stored
		}
	}

	if outcome.err != nil {
		return nil, outcome.err
	}
	got := *pi
	return &got, nil
}

// GetPaymentIntent reports an intent this Provider created, as Stripe holds
// it now — a refunded intent still reads as succeeded, as it does at Stripe.
func (p *Provider) GetPaymentIntent(_ context.Context, id string) (*payments.PaymentIntent, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	pi, ok := p.intents[id]
	if !ok {
		return nil, errors.New("paymentstest: no such payment intent " + id)
	}
	got := *pi
	return &got, nil
}

func (p *Provider) Refund(_ context.Context, req payments.RefundRequest) (*payments.RefundResult, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.RefundErr != nil {
		return nil, p.RefundErr
	}
	// As Stripe: only a charged intent can be refunded, and only once.
	if pi, ok := p.intents[req.PaymentIntentID]; !ok || pi.Status != payments.PaymentIntentStatusSucceeded {
		return nil, errors.New("paymentstest: no charge to refund on " + req.PaymentIntentID)
	}
	if p.refunded[req.PaymentIntentID] {
		return nil, errors.New("paymentstest: charge already refunded on " + req.PaymentIntentID)
	}
	if p.refunded == nil {
		p.refunded = map[string]bool{}
	}
	p.refunded[req.PaymentIntentID] = true
	p.refunds = append(p.refunds, req)
	return &payments.RefundResult{
		ID:          "re_test_" + uuid.NewString(),
		Status:      payments.RefundStatusSucceeded,
		AmountCents: req.AmountCents,
	}, nil
}

func (p *Provider) CancelPaymentIntent(_ context.Context, id string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	// Stripe refuses to cancel an intent that has already taken the money;
	// that charge can only be refunded.
	if pi, ok := p.intents[id]; ok && pi.Status == payments.PaymentIntentStatusSucceeded {
		return errors.New("paymentstest: cannot cancel succeeded payment intent " + id)
	}
	p.canceled = append(p.canceled, id)
	return nil
}

// Requests is every CreatePaymentIntent call in order, charged or not.
func (p *Provider) Requests() []payments.CreatePaymentIntentRequest {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]payments.CreatePaymentIntentRequest(nil), p.requests...)
}

// Executed is how many creates reached the card network — a charge or a
// decline — rather than being answered from a stored idempotency key. A
// retry that replays the first answer does not ask the issuer again.
func (p *Provider) Executed() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.executed
}

// Charges is every intent that moved money, in order. This, not Requests, is
// the number a customer sees on their statement.
func (p *Provider) Charges() []payments.PaymentIntent {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]payments.PaymentIntent(nil), p.charges...)
}

// ChargeCount is len(Charges()).
func (p *Provider) ChargeCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.charges)
}

// Refunds is every refund made, in order.
func (p *Provider) Refunds() []payments.RefundRequest {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]payments.RefundRequest(nil), p.refunds...)
}

// Canceled is every intent id asked to be cancelled, in order.
func (p *Provider) Canceled() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.canceled...)
}
