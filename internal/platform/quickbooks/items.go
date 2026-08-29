package quickbooks

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
)

// Item is a QuickBooks product or service an invoice line can bill against.
type Item struct {
	ID   string
	Name string
	Type string // Service | Inventory | NonInventory
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

// qbItemPageSize is how many items one query asks for. QBO caps a page at
// 1000 and defaults to 100.
const qbItemPageSize = 1000

// ListItems returns the company's active items, for the settings picker.
//
// Read-only, so it is safe to call while billing is in test mode — choosing
// which item to bill against is exactly the kind of thing a shop should be
// able to settle during a proof period.
//
// Paged rather than capped. A single MAXRESULTS would reintroduce the failure
// it was meant to avoid, just at a larger number: a company with more items
// than the page size would silently offer a truncated list, and the item
// somebody was looking for would simply not be there.
func (c *QBClient) ListItems(ctx context.Context) ([]Item, error) {
	var items []Item
	// Bounded, not merely terminating on a short page. The loop's exit depends
	// on QBO honouring STARTPOSITION; a server that ignored it would return a
	// full page forever, and each turn of that loop is an HTTP call with a
	// thirty second timeout on a request-scoped context. Ten pages is far more
	// items than any coffee roaster has and still a finite promise.
	const maxPages = 10
	// QBO's STARTPOSITION is 1-based.
	for page := 0; page < maxPages; page++ {
		start := 1 + page*qbItemPageSize
		// SELECT * rather than a field list: Intuit's query language documents
		// * as the supported select clause, and a field list is not reliably
		// honoured — a silently ignored one would look like a company with no
		// items. Every other query in this package does the same.
		query := fmt.Sprintf("SELECT * FROM Item WHERE Active = true STARTPOSITION %d MAXRESULTS %d",
			start, qbItemPageSize)
		respBody, err := c.doAPI(ctx, "GET", "/query?query="+urlEncode(query), nil)
		if err != nil {
			return nil, fmt.Errorf("query QB items: %w", err)
		}

		var resp qbItemQueryResponse
		if err := json.Unmarshal(respBody, &resp); err != nil {
			return nil, fmt.Errorf("unmarshal QB item query: %w", err)
		}

		got := resp.QueryResponse.Item
		for _, raw := range got {
			item := Item{ID: raw.ID, Name: raw.Name, Type: raw.Type}
			if raw.IncomeAccountRef != nil {
				item.IncomeAccount = raw.IncomeAccountRef.Name
			}
			items = append(items, item)
		}
		if len(got) < qbItemPageSize {
			break
		}
	}

	// By name: QBO returns them in its own order, and a picker a bookkeeper
	// has to scan wants the order they already know their items in.
	sort.Slice(items, func(i, j int) bool { return items[i].Name < items[j].Name })
	return items, nil
}
