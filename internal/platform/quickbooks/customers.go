package quickbooks

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/dukerupert/hiri/internal/domain"
)

// qbCustomerRequest is the JSON body for creating/updating a QB customer.
type qbCustomerRequest struct {
	DisplayName     string       `json:"DisplayName"`
	GivenName       string       `json:"GivenName,omitempty"`
	FamilyName      string       `json:"FamilyName,omitempty"`
	PrimaryEmailAddr *qbEmail    `json:"PrimaryEmailAddr,omitempty"`
	PrimaryPhone     *qbPhone    `json:"PrimaryPhone,omitempty"`
	SyncToken        string      `json:"SyncToken,omitempty"` // required for updates
	ID               string      `json:"Id,omitempty"`        // required for updates
}

type qbEmail struct {
	Address string `json:"Address"`
}

type qbPhone struct {
	FreeFormNumber string `json:"FreeFormNumber"`
}

// qbCustomerResponse is the JSON response from QB customer endpoints.
type qbCustomerResponse struct {
	Customer struct {
		ID        string `json:"Id"`
		SyncToken string `json:"SyncToken"`
	} `json:"Customer"`
}

// CreateCustomer creates a customer in QBO and returns their QB customer ID.
func (c *QBClient) CreateCustomer(ctx context.Context, cust *domain.Customer) (string, error) {
	displayName := customerDisplayName(cust)

	body := qbCustomerRequest{
		DisplayName: displayName,
		GivenName:   cust.FirstName,
		FamilyName:  cust.LastName,
		PrimaryEmailAddr: &qbEmail{Address: cust.Email},
	}
	if cust.Phone != nil {
		body.PrimaryPhone = &qbPhone{FreeFormNumber: *cust.Phone}
	}

	respBody, err := c.doAPI(ctx, "POST", "/customer", body)
	if err != nil {
		return "", fmt.Errorf("create QB customer: %w", err)
	}

	var resp qbCustomerResponse
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return "", fmt.Errorf("unmarshal QB customer response: %w", err)
	}

	return resp.Customer.ID, nil
}

// UpdateCustomer updates an existing QBO customer.
func (c *QBClient) UpdateCustomer(ctx context.Context, qbID string, cust *domain.Customer) error {
	// First fetch current customer to get SyncToken (required for sparse updates)
	fetchResp, err := c.doAPI(ctx, "GET", fmt.Sprintf("/customer/%s", qbID), nil)
	if err != nil {
		return fmt.Errorf("fetch QB customer for update: %w", err)
	}

	var current qbCustomerResponse
	if err := json.Unmarshal(fetchResp, &current); err != nil {
		return fmt.Errorf("unmarshal current QB customer: %w", err)
	}

	displayName := customerDisplayName(cust)

	body := qbCustomerRequest{
		ID:          qbID,
		SyncToken:   current.Customer.SyncToken,
		DisplayName: displayName,
		GivenName:   cust.FirstName,
		FamilyName:  cust.LastName,
		PrimaryEmailAddr: &qbEmail{Address: cust.Email},
	}
	if cust.Phone != nil {
		body.PrimaryPhone = &qbPhone{FreeFormNumber: *cust.Phone}
	}

	_, err = c.doAPI(ctx, "POST", "/customer", body)
	if err != nil {
		return fmt.Errorf("update QB customer: %w", err)
	}

	return nil
}

// customerDisplayName returns the display name for a QB customer.
// Uses CompanyName if available (wholesale), otherwise "FirstName LastName".
func customerDisplayName(c *domain.Customer) string {
	if c.CompanyName != nil && *c.CompanyName != "" {
		return *c.CompanyName
	}
	return c.FirstName + " " + c.LastName
}
