package quickbooks

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/dukerupert/hiri/internal/domain"
)

// qbCustomerRequest is the JSON body for creating/updating a QB customer.
type qbCustomerRequest struct {
	DisplayName      string   `json:"DisplayName"`
	GivenName        string   `json:"GivenName,omitempty"`
	FamilyName       string   `json:"FamilyName,omitempty"`
	PrimaryEmailAddr *qbEmail `json:"PrimaryEmailAddr,omitempty"`
	PrimaryPhone     *qbPhone `json:"PrimaryPhone,omitempty"`
	SyncToken        string   `json:"SyncToken,omitempty"` // required for updates
	ID               string   `json:"Id,omitempty"`        // required for updates
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
			ID               string   `json:"Id"`
			DisplayName      string   `json:"DisplayName"`
			PrimaryEmailAddr *qbEmail `json:"PrimaryEmailAddr"`
		} `json:"Customer"`
		TotalCount int `json:"totalCount"`
	} `json:"QueryResponse"`
}

// FindCustomer searches QBO for an existing customer by email first, then by
// display name. Returns nil (not an error) if no match is found.
//
// Email leads because it is the stabler identifier: company display names
// drift between systems ("Blue Heron Cafe" vs "Blue Heron Café") while the
// billing email rarely does. QBO query comparisons are case-insensitive, so
// Hiri's lowercased emails match however the address was entered in QB.
// PrimaryEmailAddr is NOT unique in QBO, so an ambiguous email hit is only
// trusted when a record carries our display name; otherwise the unique
// DisplayName lookup decides.
func (c *QBClient) FindCustomer(ctx context.Context, displayName, email string) (*QBCustomer, error) {
	if email != "" {
		matches, err := c.queryCustomers(ctx, "PrimaryEmailAddr", email)
		if err != nil {
			return nil, err
		}
		if m := pickEmailMatch(matches, displayName); m != nil {
			return m, nil
		}
	}

	// Fall back to display name (company name for wholesale; unique in QB)
	if displayName != "" {
		matches, err := c.queryCustomers(ctx, "DisplayName", displayName)
		if err != nil {
			return nil, err
		}
		if len(matches) > 0 {
			return &matches[0], nil
		}
	}

	return nil, nil
}

// pickEmailMatch resolves the results of an email query. A single hit is
// trusted; multiple hits (one billing email shared across QB customer records,
// e.g. two locations of the same owner) are only accepted when a record's
// display name matches ours — otherwise nil is returned so the caller falls
// through to the unique DisplayName lookup rather than guessing at
// QueryResponse order.
func pickEmailMatch(matches []QBCustomer, displayName string) *QBCustomer {
	if len(matches) == 1 {
		return &matches[0]
	}
	if displayName == "" {
		return nil
	}
	for i := range matches {
		if strings.EqualFold(matches[i].DisplayName, displayName) {
			return &matches[i]
		}
	}
	return nil
}

// queryCustomers runs a QB query for customers matching a single field.
func (c *QBClient) queryCustomers(ctx context.Context, field, value string) ([]QBCustomer, error) {
	// Whitelist allowed query fields to prevent injection via the field parameter.
	switch field {
	case "DisplayName", "PrimaryEmailAddr":
		// allowed
	default:
		return nil, fmt.Errorf("qb customer query: unsupported field %q", field)
	}

	// QB's query language escapes with a backslash, not by doubling the quote
	// — see escapeQBQuery.
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

	customers := make([]QBCustomer, 0, len(resp.QueryResponse.Customer))
	for _, match := range resp.QueryResponse.Customer {
		qbc := QBCustomer{
			ID:          match.ID,
			DisplayName: match.DisplayName,
		}
		if match.PrimaryEmailAddr != nil {
			qbc.Email = match.PrimaryEmailAddr.Address
		}
		customers = append(customers, qbc)
	}
	return customers, nil
}

// escapeQBQuery escapes a string literal for QB's query language.
//
// QBO is not SQL here: doubling the quote (”) is a parse error (fault 4000),
// and the escape sequence is a backslash. Verified against the sandbox on
// 2026-08-29 — an "O'Brien" customer is found by \' and by nothing else.
// Backslashes are escaped first so an input that already contains one does not
// turn its successor into an escape.
func escapeQBQuery(s string) string {
	result := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' || s[i] == '\'' {
			result = append(result, '\\')
		}
		result = append(result, s[i])
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
		DisplayName:      displayName,
		GivenName:        cust.FirstName,
		FamilyName:       cust.LastName,
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
		ID:               qbID,
		SyncToken:        current.Customer.SyncToken,
		DisplayName:      displayName,
		GivenName:        cust.FirstName,
		FamilyName:       cust.LastName,
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
