package web

import (
	"context"
	"errors"
	"sync"

	"github.com/google/uuid"

	"github.com/dukerupert/hiri/internal/platform/payments"
)

// fakePaymentProvider is a payments.Provider for the handlers that create a
// PaymentIntent, and a record of what they asked it to charge.
//
// payments.Provider has twelve methods and these handlers call three of them,
// so the interface is embedded rather than implemented: the embedded field is
// nil, and a call to anything not overridden below panics. That is the point —
// a change that starts charging through a fourth method should say so loudly
// here rather than take a zero value and leave the suite green. The same
// reason stubPaymentMethods in internal/app is written this way.
//
// The handlers under test create the intent inside the request and never
// concurrently, but a test may read the record while nothing is running; the
// mutex costs nothing and keeps -race honest about it.
type fakePaymentProvider struct {
	payments.Provider

	mu sync.Mutex

	// createdIntents holds every CreatePaymentIntent request in order. The
	// amount on the last one is the number the card is charged.
	createdIntents []payments.CreatePaymentIntentRequest
	// createdCustomers holds every Stripe customer the handler minted.
	createdCustomers []payments.CreateCustomerRequest
	// canceled holds every PaymentIntent id the handler asked to cancel,
	// whether abandoning a previous intent or cleaning up an orphaned one.
	canceled []string

	// intentErr, when set, fails CreatePaymentIntent. For the phase-3 rollback
	// path there is nothing to set: the handler's own transaction is what
	// fails there.
	intentErr error

	// onCreate, when set, runs inside CreatePaymentIntent. Creating the intent
	// is the only moment that sits between the handler's two pricings — phase 1
	// prices the lines and quotes the provider from the answer, phase 3 prices
	// them again inside PlaceOrder — so this is the seam a test needs to
	// reproduce a price moving mid-checkout rather than simulating one.
	//
	// It is called with the mutex released. A hook does arbitrary work — the one
	// that exists writes to the database — and sync.Mutex is not reentrant, so a
	// hook that read back through canceledIDs or provoked a second intent would
	// deadlock here with no diagnostic until go test's ten-minute panic.
	onCreate func()
}

func (f *fakePaymentProvider) CreatePaymentIntent(_ context.Context, req payments.CreatePaymentIntentRequest) (*payments.PaymentIntent, error) {
	f.mu.Lock()
	hook, failWith := f.onCreate, f.intentErr
	f.mu.Unlock()

	if failWith != nil {
		return nil, failWith
	}
	if hook != nil {
		hook()
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	f.createdIntents = append(f.createdIntents, req)
	// A fresh id per intent, not a counter: these tests commit, the id lands
	// on an order row under a unique index, and a per-test counter collides
	// with the row the previous test left behind. Same reason the fixtures
	// key on fresh UUIDs — see setup_test.go.
	id := "pi_fake_" + uuid.NewString()
	return &payments.PaymentIntent{
		ID:           id,
		ClientSecret: id + "_secret",
		Status:       payments.PaymentIntentStatusRequiresPaymentMethod,
		AmountCents:  req.AmountCents,
		Currency:     req.Currency,
		Metadata:     req.Metadata,
	}, nil
}

func (f *fakePaymentProvider) CreateCustomer(_ context.Context, req payments.CreateCustomerRequest) (*payments.Customer, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.createdCustomers = append(f.createdCustomers, req)
	// Unique for the same reason, and more sharply: customers.stripe_customer_id
	// carries a unique index, so a repeated id fails the handler's phase 3.
	return &payments.Customer{
		ID:    "cus_fake_" + uuid.NewString(),
		Email: req.Email,
		Name:  req.Name,
	}, nil
}

func (f *fakePaymentProvider) CancelPaymentIntent(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.canceled = append(f.canceled, id)
	return nil
}

// lastIntent is the PaymentIntent request the handler most recently made, and
// fails the caller's assertion by reporting none rather than panicking.
func (f *fakePaymentProvider) lastIntent() (payments.CreatePaymentIntentRequest, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.createdIntents) == 0 {
		return payments.CreatePaymentIntentRequest{}, false
	}
	return f.createdIntents[len(f.createdIntents)-1], true
}

func (f *fakePaymentProvider) intentCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.createdIntents)
}

func (f *fakePaymentProvider) canceledIDs() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.canceled...)
}

// errPaymentProviderDown is the shape of a Stripe outage: the call fails and
// nothing downstream of it runs.
var errPaymentProviderDown = errors.New("payment provider unavailable")
