package quickbooks

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
)

// Item is a QuickBooks product or service an invoice line can bill against.
type Item struct {
	ID     string
	Name   string
	Type   string // Service | Inventory | NonInventory
	Active bool
	// IncomeAccount is the account this item posts revenue to. Shown in the
	// picker because "which item" is really a question about which account,
	// and an item name alone does not answer it for a bookkeeper.
	IncomeAccount string
}

type qbItemQueryResponse struct {
	QueryResponse struct {
		Item []struct {
			ID               string `json:"Id"`
			Name             string `json:"Name"`
			Type             string `json:"Type"`
			Active           bool   `json:"Active"`
			IncomeAccountRef *struct {
				Value string `json:"value"`
				Name  string `json:"name"`
			} `json:"IncomeAccountRef"`
		} `json:"Item"`
	} `json:"QueryResponse"`
}

// qbItemPageSize is how many items one query returns. QBO caps a query at 1000
// rows and defaults to 100, so this is asked for explicitly rather than left to
// the default — a shop with 300 products would otherwise silently see the
// first hundred and wonder where the rest went.
const qbItemPageSize = 1000

// ListItems returns the company's active items, for the settings picker.
//
// Read-only, so it is safe to call while billing is in test mode — choosing
// which item to bill against is exactly the kind of thing a shop should be
// able to settle during a proof period.
func (c *QBClient) ListItems(ctx context.Context) ([]Item, error) {
	query := fmt.Sprintf(
		"SELECT Id, Name, Type, Active, IncomeAccountRef FROM Item WHERE Active = true MAXRESULTS %d",
		qbItemPageSize)
	respBody, err := c.doAPI(ctx, "GET", "/query?query="+urlEncode(query), nil)
	if err != nil {
		return nil, fmt.Errorf("query QB items: %w", err)
	}

	var resp qbItemQueryResponse
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return nil, fmt.Errorf("unmarshal QB item query: %w", err)
	}

	items := make([]Item, 0, len(resp.QueryResponse.Item))
	for _, raw := range resp.QueryResponse.Item {
		item := Item{ID: raw.ID, Name: raw.Name, Type: raw.Type, Active: raw.Active}
		if raw.IncomeAccountRef != nil {
			item.IncomeAccount = raw.IncomeAccountRef.Name
		}
		items = append(items, item)
	}
	// By name: QBO returns them in its own order, and a picker a bookkeeper
	// has to scan wants the order they already know their items in.
	sort.Slice(items, func(i, j int) bool { return items[i].Name < items[j].Name })
	return items, nil
}
