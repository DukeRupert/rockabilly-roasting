package payments

import (
	"context"
	"fmt"

	"github.com/stripe/stripe-go/v82"
	"github.com/stripe/stripe-go/v82/client"
	"github.com/stripe/stripe-go/v82/webhook"
)

// StripeProvider implements Provider using the Stripe API.
type StripeProvider struct {
	client        *client.API
	webhookSecret string
}

// NewStripeProvider creates a new StripeProvider with the given API key and webhook secret.
func NewStripeProvider(apiKey, webhookSecret string) *StripeProvider {
	c := &client.API{}
	c.Init(apiKey, nil)
	return &StripeProvider{
		client:        c,
		webhookSecret: webhookSecret,
	}
}

func (p *StripeProvider) CreatePaymentIntent(_ context.Context, req CreatePaymentIntentRequest) (*PaymentIntent, error) {
	params := &stripe.PaymentIntentParams{
		Amount:   stripe.Int64(req.AmountCents),
		Currency: stripe.String(req.Currency),
	}

	if req.CustomerID != "" {
		params.Customer = stripe.String(req.CustomerID)
	}

	if req.PaymentMethodID != "" {
		params.PaymentMethod = stripe.String(req.PaymentMethodID)
	}

	if req.OffSession {
		params.OffSession = stripe.Bool(true)
		params.Confirm = stripe.Bool(true)
	}

	if req.ShippingAddress != nil {
		params.Shipping = &stripe.ShippingDetailsParams{
			Name: stripe.String(req.ShippingAddress.Name),
			Address: &stripe.AddressParams{
				Line1:      stripe.String(req.ShippingAddress.Line1),
				Line2:      stripe.String(req.ShippingAddress.Line2),
				City:       stripe.String(req.ShippingAddress.City),
				State:      stripe.String(req.ShippingAddress.State),
				PostalCode: stripe.String(req.ShippingAddress.PostalCode),
				Country:    stripe.String(req.ShippingAddress.Country),
			},
		}
	}

	for k, v := range req.Metadata {
		params.AddMetadata(k, v)
	}

	pi, err := p.client.PaymentIntents.New(params)
	if err != nil {
		return nil, fmt.Errorf("create payment intent: %w", err)
	}

	return paymentIntentFromStripe(pi), nil
}

func (p *StripeProvider) GetPaymentIntent(_ context.Context, paymentIntentID string) (*PaymentIntent, error) {
	pi, err := p.client.PaymentIntents.Get(paymentIntentID, nil)
	if err != nil {
		return nil, fmt.Errorf("get payment intent: %w", err)
	}
	return paymentIntentFromStripe(pi), nil
}

func (p *StripeProvider) CancelPaymentIntent(_ context.Context, paymentIntentID string) error {
	_, err := p.client.PaymentIntents.Cancel(paymentIntentID, nil)
	if err != nil {
		return fmt.Errorf("cancel payment intent: %w", err)
	}
	return nil
}

func (p *StripeProvider) Refund(_ context.Context, req RefundRequest) (*RefundResult, error) {
	params := &stripe.RefundParams{
		PaymentIntent: stripe.String(req.PaymentIntentID),
	}

	if req.AmountCents > 0 {
		params.Amount = stripe.Int64(req.AmountCents)
	}

	if req.Reason != "" {
		params.Reason = stripe.String(req.Reason)
	}

	r, err := p.client.Refunds.New(params)
	if err != nil {
		return nil, fmt.Errorf("create refund: %w", err)
	}

	return &RefundResult{
		ID:          r.ID,
		Status:      RefundStatus(r.Status),
		AmountCents: r.Amount,
	}, nil
}

func (p *StripeProvider) CreateCustomer(_ context.Context, req CreateCustomerRequest) (*Customer, error) {
	params := &stripe.CustomerParams{
		Email: stripe.String(req.Email),
		Name:  stripe.String(req.Name),
	}

	for k, v := range req.Metadata {
		params.AddMetadata(k, v)
	}

	c, err := p.client.Customers.New(params)
	if err != nil {
		return nil, fmt.Errorf("create customer: %w", err)
	}

	return customerFromStripe(c), nil
}

func (p *StripeProvider) GetCustomer(_ context.Context, customerID string) (*Customer, error) {
	c, err := p.client.Customers.Get(customerID, nil)
	if err != nil {
		return nil, fmt.Errorf("get customer: %w", err)
	}
	return customerFromStripe(c), nil
}

func (p *StripeProvider) AttachPaymentMethod(_ context.Context, paymentMethodID string, customerID string) error {
	_, err := p.client.PaymentMethods.Attach(paymentMethodID, &stripe.PaymentMethodAttachParams{
		Customer: stripe.String(customerID),
	})
	if err != nil {
		return fmt.Errorf("attach payment method: %w", err)
	}
	return nil
}

func (p *StripeProvider) DetachPaymentMethod(_ context.Context, paymentMethodID string) error {
	_, err := p.client.PaymentMethods.Detach(paymentMethodID, nil)
	if err != nil {
		return fmt.Errorf("detach payment method: %w", err)
	}
	return nil
}

func (p *StripeProvider) ListPaymentMethods(_ context.Context, customerID string) ([]PaymentMethod, error) {
	params := &stripe.PaymentMethodListParams{
		Customer: stripe.String(customerID),
		Type:     stripe.String(string(stripe.PaymentMethodTypeCard)),
	}

	iter := p.client.PaymentMethods.List(params)
	var methods []PaymentMethod
	for iter.Next() {
		pm := iter.PaymentMethod()
		method := PaymentMethod{
			ID:   pm.ID,
			Type: string(pm.Type),
		}
		if pm.Card != nil {
			method.Card = &CardDetails{
				Last4:    pm.Card.Last4,
				Brand:    string(pm.Card.Brand),
				ExpMonth: pm.Card.ExpMonth,
				ExpYear:  pm.Card.ExpYear,
			}
		}
		methods = append(methods, method)
	}
	if err := iter.Err(); err != nil {
		return nil, fmt.Errorf("list payment methods: %w", err)
	}
	return methods, nil
}

func (p *StripeProvider) ConstructWebhookEvent(payload []byte, signature string) (*WebhookEvent, error) {
	event, err := webhook.ConstructEventWithOptions(payload, signature, p.webhookSecret, webhook.ConstructEventOptions{
		IgnoreAPIVersionMismatch: true,
	})
	if err != nil {
		return nil, fmt.Errorf("verify webhook signature: %w", err)
	}
	return &WebhookEvent{
		ID:   event.ID,
		Type: string(event.Type),
		Data: event.Data.Raw,
	}, nil
}

// --- Stripe-to-domain converters ---

func paymentIntentFromStripe(pi *stripe.PaymentIntent) *PaymentIntent {
	result := &PaymentIntent{
		ID:           pi.ID,
		ClientSecret: pi.ClientSecret,
		Status:       PaymentIntentStatus(pi.Status),
		AmountCents:  pi.Amount,
		Currency:     string(pi.Currency),
		Metadata:     pi.Metadata,
	}
	if pi.PaymentMethod != nil {
		result.PaymentMethodID = pi.PaymentMethod.ID
	}
	return result
}

func customerFromStripe(c *stripe.Customer) *Customer {
	result := &Customer{
		ID:    c.ID,
		Email: c.Email,
		Name:  c.Name,
	}
	if c.InvoiceSettings != nil && c.InvoiceSettings.DefaultPaymentMethod != nil {
		result.DefaultPaymentMethodID = c.InvoiceSettings.DefaultPaymentMethod.ID
	}
	return result
}
