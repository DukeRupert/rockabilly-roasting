package quickbooks

import (
	"context"

	"github.com/dukerupert/hiri/internal/domain"
)

// Provider implements Client by resolving the configured app on each call and
// delegating. Callers that only make API calls therefore never hold a client
// that has gone stale, and never hold a nil one: an unconfigured deployment
// returns ErrNotConfigured from each of these, which IsRetryable treats as
// permanent so a job fails once with a message naming the setting instead of
// retrying against a configuration only a human can supply.
var _ Client = (*Provider)(nil)

func (p *Provider) FindCustomer(ctx context.Context, displayName, email string) (*QBCustomer, error) {
	c, err := p.resolve(ctx)
	if err != nil {
		return nil, err
	}
	return c.FindCustomer(ctx, displayName, email)
}

func (p *Provider) CreateCustomer(ctx context.Context, cust *domain.Customer) (string, error) {
	c, err := p.resolve(ctx)
	if err != nil {
		return "", err
	}
	return c.CreateCustomer(ctx, cust)
}

func (p *Provider) UpdateCustomer(ctx context.Context, qbID string, cust *domain.Customer) error {
	c, err := p.resolve(ctx)
	if err != nil {
		return err
	}
	return c.UpdateCustomer(ctx, qbID, cust)
}

func (p *Provider) CreateInvoice(ctx context.Context, params InvoiceParams) (*Invoice, error) {
	c, err := p.resolve(ctx)
	if err != nil {
		return nil, err
	}
	return c.CreateInvoice(ctx, params)
}

func (p *Provider) GetInvoice(ctx context.Context, qbInvoiceID string) (*Invoice, error) {
	c, err := p.resolve(ctx)
	if err != nil {
		return nil, err
	}
	return c.GetInvoice(ctx, qbInvoiceID)
}

func (p *Provider) FindInvoiceByDocNumber(ctx context.Context, docNumber string) (*Invoice, error) {
	c, err := p.resolve(ctx)
	if err != nil {
		return nil, err
	}
	return c.FindInvoiceByDocNumber(ctx, docNumber)
}

func (p *Provider) SendInvoice(ctx context.Context, qbInvoiceID string) error {
	c, err := p.resolve(ctx)
	if err != nil {
		return err
	}
	return c.SendInvoice(ctx, qbInvoiceID)
}

func (p *Provider) FindOrCreateTerm(ctx context.Context, dueDays int) (string, error) {
	c, err := p.resolve(ctx)
	if err != nil {
		return "", err
	}
	return c.FindOrCreateTerm(ctx, dueDays)
}

func (p *Provider) FindTerm(ctx context.Context, dueDays int) (string, error) {
	c, err := p.resolve(ctx)
	if err != nil {
		return "", err
	}
	return c.FindTerm(ctx, dueDays)
}

func (p *Provider) ListItems(ctx context.Context) ([]Item, error) {
	c, err := p.resolve(ctx)
	if err != nil {
		return nil, err
	}
	return c.ListItems(ctx)
}

func (p *Provider) CreatePayment(ctx context.Context, params PaymentParams) (*Payment, error) {
	c, err := p.resolve(ctx)
	if err != nil {
		return nil, err
	}
	return c.CreatePayment(ctx, params)
}
