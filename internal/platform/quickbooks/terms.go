package quickbooks

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"sync"
)

// qbTerm is a QBO Term entity — the named payment terms ("Net 15") shown on an
// invoice and used by QBO's own reporting.
type qbTerm struct {
	ID   string `json:"Id"`
	Name string `json:"Name"`
	Type string `json:"Type"` // STANDARD | DATE_DRIVEN
	// DueDays is absent on a DATE_DRIVEN term ("Net 15 on the 5th"), which
	// carries DayOfMonthDue instead. Both are read so such a term cannot
	// unmarshal to DueDays 0 and be mistaken for "Due on receipt".
	DueDays       int `json:"DueDays"`
	DayOfMonthDue int `json:"DayOfMonthDue"`
}

// isStandard reports whether the term counts days from the invoice date, which
// is the only kind that can represent NET terms. Verified against QBO that
// Type is populated on stock terms.
func (t qbTerm) isStandard() bool {
	return t.DayOfMonthDue == 0 && (t.Type == "" || t.Type == "STANDARD")
}

type qbTermQueryResponse struct {
	QueryResponse struct {
		Term []qbTerm `json:"Term"`
	} `json:"QueryResponse"`
}

type qbTermResponse struct {
	Term qbTerm `json:"Term"`
}

// qbTermRequest creates a Term. DueDays drives the due date QBO derives; Name
// is what a human sees on the invoice.
type qbTermRequest struct {
	Name    string `json:"Name"`
	DueDays int    `json:"DueDays"`
	Type    string `json:"Type"`
}

// termCache memoizes DueDays -> Term ID for the life of the process. Terms are
// a tiny, effectively static list, and the invoice job would otherwise query
// QBO twice per invoice.
type termCache struct {
	mu sync.Mutex
	id map[int]string
}

func newTermCache() *termCache { return &termCache{id: make(map[int]string)} }

func (c *termCache) get(dueDays int) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	id, ok := c.id[dueDays]
	return id, ok
}

func (c *termCache) put(dueDays int, id string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.id[dueDays] = id
}

// forget drops any entry pointing at the given Term ID. Called when QBO
// rejects an invoice that referenced it — a Term deleted or deactivated in
// QBO after being cached would otherwise poison every later invoice for the
// life of the process.
func (c *termCache) forget(id string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for days, cached := range c.id {
		if cached == id {
			delete(c.id, days)
		}
	}
}

// FindOrCreateTerm returns the QBO Term ID for the given NET terms in days,
// creating the Term if the company does not already have one.
//
// QBO ships Due on receipt (0) and Net 10/15/30/60. The house default of net-7
// is not among them, so on a fresh company this creates a "Net 7" Term the
// first time an invoice needs one. Matching is by DueDays rather than by name
// because the name is user-editable in QBO — a bookkeeper who renames "Net 30"
// to "30 days" must not cause a duplicate Term to be created.
//
// Terms are matched and created per realm; the cache is per process and keyed
// on DueDays alone, which is safe because a QBClient is bound to one tenant.
func (c *QBClient) FindOrCreateTerm(ctx context.Context, dueDays int) (string, error) {
	if dueDays < 0 {
		return "", fmt.Errorf("%w: negative payment terms (%d days)", ErrBadRequest, dueDays)
	}
	if id, ok := c.terms.get(dueDays); ok {
		return id, nil
	}

	// Every Term is fetched and matched here rather than filtered with a
	// WHERE clause: QBO's query language rejects an integer comparison on
	// DueDays ("Error parsing query ... was expecting true/false"). The list
	// is a handful of rows, and this call is cached per process anyway.
	respBody, err := c.doAPI(ctx, "GET", "/query?query="+urlEncode("SELECT * FROM Term"), nil)
	if err != nil {
		return "", fmt.Errorf("query QB terms: %w", err)
	}
	var found qbTermQueryResponse
	if err := json.Unmarshal(respBody, &found); err != nil {
		return "", fmt.Errorf("unmarshal QB term query: %w", err)
	}
	for _, term := range found.QueryResponse.Term {
		if term.isStandard() && term.DueDays == dueDays {
			c.terms.put(dueDays, term.ID)
			return term.ID, nil
		}
	}

	body := qbTermRequest{
		Name:    termName(dueDays),
		DueDays: dueDays,
		Type:    "STANDARD",
	}
	created, err := c.doAPI(ctx, "POST", "/term", body)
	if err != nil {
		return "", fmt.Errorf("create QB term: %w", err)
	}
	var resp qbTermResponse
	if err := json.Unmarshal(created, &resp); err != nil {
		return "", fmt.Errorf("unmarshal QB term response: %w", err)
	}
	c.terms.put(dueDays, resp.Term.ID)
	return resp.Term.ID, nil
}

// termName is the Term name to create when the company has none for these
// terms. It mirrors QBO's own naming so a created "Net 7" sits naturally
// beside the stock "Net 15".
func termName(dueDays int) string {
	if dueDays == 0 {
		return "Due on receipt"
	}
	return "Net " + strconv.Itoa(dueDays)
}
