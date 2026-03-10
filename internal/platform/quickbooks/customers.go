package quickbooks

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"

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

// qbQueryResponse is the generic response shape for QB query endpoints.
type qbQueryResponse struct {
	QueryResponse struct {
		Customer []struct {
			ID               string  `json:"Id"`
			DisplayName      string  `json:"DisplayName"`
			PrimaryEmailAddr *qbEmail `json:"PrimaryEmailAddr"`
		} `json:"Customer"`
		TotalCount int `json:"totalCount"`
	} `json:"QueryResponse"`
}

// FindCustomer searches QBO for an existing customer by display name first,
// then by email. Returns nil (not an error) if no match is found.
func (c *QBClient) FindCustomer(ctx context.Context, displayName, email string) (*QBCustomer, error) {
	// Try display name first (unique in QB, most reliable match for wholesale)
	if displayName != "" {
		found, err := c.queryCustomer(ctx, "DisplayName", displayName)
		if err != nil {
			return nil, err
		}
		if found != nil {
			return found, nil
		}
	}

	// Fall back to email
	if email != "" {
		found, err := c.queryCustomer(ctx, "PrimaryEmailAddr", email)
		if err != nil {
			return nil, err
		}
		if found != nil {
			return found, nil
		}
	}

	return nil, nil
}

// queryCustomer runs a QB query for a customer by a single field.
func (c *QBClient) queryCustomer(ctx context.Context, field, value string) (*QBCustomer, error) {
	// Whitelist allowed query fields to prevent injection via the field parameter.
	switch field {
	case "DisplayName", "PrimaryEmailAddr":
		// allowed
	default:
		return nil, fmt.Errorf("qb customer query: unsupported field %q", field)
	}

	// QB query language uses doubled single quotes to escape (like SQL), not backslashes.
	escaped := escapeQBQuery(value)
	query := fmt.Sprintf("SELECT * FROM Customer WHERE %s = '%s'", field, escaped)

	respBody, err := c.doAPI(ctx, "GET", "/query?query="+urlEncode(query), nil)
	if err != nil {
		return nil, fmt.Errorf("qb customer query (%s): %w", field, err)
	}

	var resp qbQueryResponse
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return nil, fmt.Errorf("unmarshal qb customer query: %w", err)
	}

	if len(resp.QueryResponse.Customer) == 0 {
		return nil, nil
	}

	match := resp.QueryResponse.Customer[0]
	qbc := &QBCustomer{
		ID:          match.ID,
		DisplayName: match.DisplayName,
	}
	if match.PrimaryEmailAddr != nil {
		qbc.Email = match.PrimaryEmailAddr.Address
	}
	return qbc, nil
}

// escapeQBQuery escapes single quotes for QB's query language.
// QB uses doubled single quotes ('') as the escape sequence, like SQL.
func escapeQBQuery(s string) string {
	result := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		if s[i] == '\'' {
			result = append(result, '\'', '\'')
		} else {
			result = append(result, s[i])
		}
	}
	return string(result)
}

// urlEncode encodes a query string value for use in a URL path.
func urlEncode(s string) string {
	// Use net/url for proper encoding
	return url.QueryEscape(s)
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
