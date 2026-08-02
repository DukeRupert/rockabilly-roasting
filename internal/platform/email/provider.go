package email

import "context"

// Message represents a single outbound email.
type Message struct {
	From    string
	To      string
	Bcc     string // optional, comma-separated list of BCC recipients
	Subject string
	HTML    string
	Text    string
	Tag     string // optional categorization tag (e.g. "magic-link", "invoice")
	// Headers carries extra SMTP headers. Used for List-Unsubscribe /
	// List-Unsubscribe-Post (RFC 2369 / RFC 8058), which give Gmail and Apple
	// Mail a native unsubscribe control — the alternative for a recipient who
	// wants out is the spam button, and that costs sending reputation on the
	// same domain as order confirmations and invoices.
	Headers map[string]string
}

// TemplatedMessage sends an email using a provider-managed template.
type TemplatedMessage struct {
	From          string
	To            string
	TemplateAlias string
	TemplateModel map[string]any
	Tag           string
}

// SendResult contains the provider's response after sending.
type SendResult struct {
	MessageID string
}

// Sender is the interface for sending transactional emails.
// Implementations live alongside this file (e.g. postmark.go).
type Sender interface {
	// Send sends a single email with inline content.
	Send(ctx context.Context, msg Message) (*SendResult, error)

	// SendTemplate sends a single email using a provider-managed template.
	SendTemplate(ctx context.Context, msg TemplatedMessage) (*SendResult, error)
}
