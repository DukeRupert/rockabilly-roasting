package email

import (
	"context"
	"sync"
)

// TestSender implements Sender for tests — captures messages, never sends.
type TestSender struct {
	mu   sync.Mutex
	Sent []Message
}

// NewTestSender creates a TestSender.
func NewTestSender() *TestSender {
	return &TestSender{}
}

// Send captures the message without sending it.
func (s *TestSender) Send(_ context.Context, msg Message) (*SendResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Sent = append(s.Sent, msg)
	return &SendResult{MessageID: "test-" + msg.Tag}, nil
}

// SendTemplate captures the template message as a regular Message.
func (s *TestSender) SendTemplate(_ context.Context, msg TemplatedMessage) (*SendResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Sent = append(s.Sent, Message{
		From: msg.From,
		To:   msg.To,
		Tag:  msg.Tag,
	})
	return &SendResult{MessageID: "test-template-" + msg.Tag}, nil
}

// Reset clears captured messages.
func (s *TestSender) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Sent = nil
}

// Last returns the most recently sent message, or an empty Message if none.
func (s *TestSender) Last() Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.Sent) == 0 {
		return Message{}
	}
	return s.Sent[len(s.Sent)-1]
}
