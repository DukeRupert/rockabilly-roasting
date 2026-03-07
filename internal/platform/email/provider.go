package email

import "context"

// Message represents a single outbound email.
type Message struct {
	From    string
	To      string
	Subject string
	HTML    string
	Text    string
	Tag     string // optional categorization tag (e.g. "magic-link", "invoice")
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
