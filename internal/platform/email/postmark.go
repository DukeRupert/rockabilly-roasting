package email

import (
	"context"
	"fmt"
	"sort"

	"github.com/mrz1836/postmark"
)

// PostmarkSender implements Sender using the Postmark transactional email API.
type PostmarkSender struct {
	client *postmark.Client
}

// NewPostmarkSender creates a Sender backed by Postmark.
// serverToken authenticates against a specific Postmark server (message stream).
func NewPostmarkSender(serverToken string) *PostmarkSender {
	// Account token is only needed for account-level management (not sending).
	client := postmark.NewClient(serverToken, "")
	return &PostmarkSender{client: client}
}

// Send sends a single email with inline HTML/text content.
func (s *PostmarkSender) Send(ctx context.Context, msg Message) (*SendResult, error) {
	resp, err := s.client.SendEmail(ctx, postmark.Email{
		From:     msg.From,
		To:       msg.To,
		Bcc:      msg.Bcc,
		Subject:  msg.Subject,
		HTMLBody: msg.HTML,
		TextBody: msg.Text,
		Tag:      msg.Tag,
		Headers:  postmarkHeaders(msg.Headers),
	})
	if err != nil {
		return nil, fmt.Errorf("postmark send: %w", err)
	}

	return &SendResult{MessageID: resp.MessageID}, nil
}

// SendTemplate sends a single email using a Postmark server template.
func (s *PostmarkSender) SendTemplate(ctx context.Context, msg TemplatedMessage) (*SendResult, error) {
	resp, err := s.client.SendTemplatedEmail(ctx, postmark.TemplatedEmail{
		TemplateAlias: msg.TemplateAlias,
		From:          msg.From,
		To:            msg.To,
		Tag:           msg.Tag,
		TemplateModel: msg.TemplateModel,
	})
	if err != nil {
		return nil, fmt.Errorf("postmark send template: %w", err)
	}

	return &SendResult{MessageID: resp.MessageID}, nil
}

// postmarkHeaders converts the provider-agnostic header map into Postmark's
// slice form. Keys are sorted so the outgoing message is deterministic, which
// keeps tests and logged payloads stable.
func postmarkHeaders(h map[string]string) []postmark.Header {
	if len(h) == 0 {
		return nil
	}
	names := make([]string, 0, len(h))
	for name := range h {
		names = append(names, name)
	}
	sort.Strings(names)

	out := make([]postmark.Header, 0, len(names))
	for _, name := range names {
		out = append(out, postmark.Header{Name: name, Value: h[name]})
	}
	return out
}
