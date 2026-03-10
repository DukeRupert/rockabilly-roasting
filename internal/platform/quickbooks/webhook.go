package quickbooks

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
)

// WebhookPayload is the top-level structure of a QBO webhook notification.
type WebhookPayload struct {
	EventNotifications []EventNotification `json:"eventNotifications"`
}

// EventNotification represents a single notification within a webhook payload.
type EventNotification struct {
	RealmID         string          `json:"realmId"`
	DataChangeEvent DataChangeEvent `json:"dataChangeEvent"`
}

// DataChangeEvent contains the entities that changed.
type DataChangeEvent struct {
	Entities []Entity `json:"entities"`
}

// Entity represents a changed entity in a QBO webhook.
type Entity struct {
	Name      string `json:"name"`
	ID        string `json:"id"`
	Operation string `json:"operation"`
}

// VerifySignature checks the HMAC-SHA256 signature of a QBO webhook payload.
func VerifySignature(signature string, body []byte, verifierToken string) bool {
	if signature == "" || verifierToken == "" {
		return false
	}
	mac := hmac.New(sha256.New, []byte(verifierToken))
	mac.Write(body)
	expected := base64.StdEncoding.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(signature), []byte(expected))
}
