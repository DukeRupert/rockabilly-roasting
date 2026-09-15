package quickbooks

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"sync"
)

// Sales tax on a QBO invoice cannot be asserted — it has to be referenced.
//
// Verified against the sandbox 2026-09-15, because the obvious approach fails
// silently. Sending `TxnTaxDetail: {"TotalTax": 8.80}` on an invoice is
// accepted with a 200 and comes back as `TotalTax: 0`: the number is dropped,
// the invoice is short by the tax, and nothing anywhere says so. Sending a
// full TaxLine that references an existing TaxRate does produce tax — but QBO
// recomputes the amount from *that rate's* percentage and ignores the one in
// the request. A request carrying TaxPercent 8.8 against California's 8% rate
// came back as 8%, $8.00, on a $158.00 invoice the shop meant to bill at
// $158.80.
//
// So an invoice can only report something close to the number Hiri computed by
// QBO holding a rate equal to the one Hiri charged and the invoice pointing at
// it. Close, not equal: matching the rate is necessary but not sufficient,
// because Hiri rounds tax per line and QBO rounds once over the summed taxable
// base, so an order with several taxable lines can land a cent apart. The
// invoice job treats a cent as arithmetic and anything larger as a real
// divergence — see taxRoundingToleranceCents. That is what this file does, and it is deliberately the same shape as
// FindOrCreateTerm: match what the company already has, create it once if it
// has nothing, cache for the life of the client (see taxCodeCache).
//
// Matching is by rate value rather than by name for the same reason terms are
// matched by DueDays — a bookkeeper who renames "WA Sales Tax" must not cause
// a duplicate rate to be created, and a duplicate rate is worse here than a
// duplicate term, because it splits the shop's sales-tax liability across two
// accounts in their books.

// qbTaxRate is a QBO TaxRate: a named percentage.
type qbTaxRate struct {
	ID        string  `json:"Id"`
	Name      string  `json:"Name"`
	RateValue float64 `json:"RateValue"`
	// Active is a pointer because QBO omits it on the built-in entries and
	// sends it on user-defined ones. Absent must read as active: a plain bool
	// would zero-value to false and reject exactly the codes every company
	// has. See isActive.
	Active *bool `json:"Active"`
}

// qbTaxCode is a QBO TaxCode: what an invoice line or transaction points at.
// A code groups one or more rates; the shop's is always a single rate.
type qbTaxCode struct {
	ID       string `json:"Id"`
	Name     string `json:"Name"`
	Active   *bool  `json:"Active"` // see qbTaxRate.Active
	Taxable  bool   `json:"Taxable"`
	TaxGroup bool   `json:"TaxGroup"`

	SalesTaxRateList struct {
		TaxRateDetail []struct {
			TaxRateRef qbRef `json:"TaxRateRef"`
		} `json:"TaxRateDetail"`
	} `json:"SalesTaxRateList"`
}

type qbTaxRateQueryResponse struct {
	QueryResponse struct {
		TaxRate []qbTaxRate `json:"TaxRate"`
	} `json:"QueryResponse"`
}

type qbTaxCodeQueryResponse struct {
	QueryResponse struct {
		TaxCode []qbTaxCode `json:"TaxCode"`
	} `json:"QueryResponse"`
}

type qbTaxAgencyQueryResponse struct {
	QueryResponse struct {
		TaxAgency []struct {
			ID                string `json:"Id"`
			DisplayName       string `json:"DisplayName"`
			TaxTrackedOnSales bool   `json:"TaxTrackedOnSales"`
		} `json:"TaxAgency"`
	} `json:"QueryResponse"`
}

// qbTaxServiceRequest creates a TaxCode and its rate in one call. This goes to
// /taxservice/taxcode, not to /taxcode — QBO has no plain create endpoint for
// a TaxCode entity, which is why this request shape looks unlike every other
// one in this package.
type qbTaxServiceRequest struct {
	TaxCode        string                  `json:"TaxCode"`
	TaxRateDetails []qbTaxRateDetailCreate `json:"TaxRateDetails"`
}

type qbTaxRateDetailCreate struct {
	TaxRateName     string  `json:"TaxRateName"`
	RateValue       float64 `json:"RateValue"`
	TaxAgencyID     string  `json:"TaxAgencyId"`
	TaxApplicableOn string  `json:"TaxApplicableOn"` // "Sales"
}

type qbTaxServiceResponse struct {
	TaxCodeID      string `json:"TaxCodeId"`
	TaxRateDetails []struct {
		TaxRateID string `json:"TaxRateId"`
	} `json:"TaxRateDetails"`
}

type qbTaxAgencyRequest struct {
	DisplayName string `json:"DisplayName"`
}

type qbTaxAgencyResponse struct {
	TaxAgency struct {
		ID string `json:"Id"`
	} `json:"TaxAgency"`
}

// TaxCodeRef is the pair an invoice needs: the code the transaction points at,
// and the rate its tax line points at. QBO requires both — the code alone
// produces no TaxLine, and a rate alone has nothing to attach to.
type TaxCodeRef struct {
	TaxCodeID string
	TaxRateID string
}

// taxCodeCache memoizes rate-in-basis-points -> refs for the life of the
// client. Rates change about once a legislative session; the invoice job would
// otherwise run two queries per invoice.
//
// The client outlives a request but not a change of Intuit app: Provider
// rebuilds it when the configured credentials change, which drops this cache
// with it. That is the correct behaviour rather than a limitation — the refs
// are ids in one company's books, and they mean nothing in another's.
type taxCodeCache struct {
	mu   sync.Mutex
	refs map[int]TaxCodeRef
}

func newTaxCodeCache() *taxCodeCache { return &taxCodeCache{refs: make(map[int]TaxCodeRef)} }

func (c *taxCodeCache) get(bps int) (TaxCodeRef, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	ref, ok := c.refs[bps]
	return ref, ok
}

func (c *taxCodeCache) put(bps int, ref TaxCodeRef) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.refs[bps] = ref
}

// forget drops any entry pointing at the given TaxCode ID, for the same reason
// termCache.forget exists: a code deactivated in QBO after being cached would
// otherwise fail every later invoice in the process.
func (c *taxCodeCache) forget(taxCodeID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for bps, cached := range c.refs {
		if cached.TaxCodeID == taxCodeID {
			delete(c.refs, bps)
		}
	}
}

// rateBasisPoints converts a percentage to hundredths of a percent, which is
// how rates are compared here. Floating-point percentages do not compare
// equal across a JSON round trip — 8.8 read back from QBO is not always the
// 8.8 that was sent — and sales tax rates are published to at most two
// decimals, so 880 is both exact and sufficient.
func rateBasisPoints(percent float64) int {
	return int(math.Round(percent * 100))
}

// taxCodeName is what a human sees in QuickBooks. Never used for matching.
func taxCodeName(label string, percent float64) string {
	label = strings.TrimSpace(label)
	if label == "" {
		label = "Sales Tax"
	}
	return fmt.Sprintf("%s %s%%", label, strconv.FormatFloat(percent, 'f', -1, 64))
}

// FindOrCreateTaxCode returns the QBO refs for the given rate, creating the
// code and its rate if the company has neither.
//
// percent is a percentage (8.8), not a fraction — store_settings holds 0.088
// and the caller converts, because QBO's RateValue is a percentage and doing
// the conversion at the boundary keeps one spelling of the rate inside this
// package.
func (c *QBClient) FindOrCreateTaxCode(ctx context.Context, label string, percent float64) (TaxCodeRef, error) {
	if percent <= 0 {
		return TaxCodeRef{}, fmt.Errorf("%w: tax rate must be positive, got %v", ErrBadRequest, percent)
	}
	bps := rateBasisPoints(percent)
	if ref, ok := c.taxCodes.get(bps); ok {
		return ref, nil
	}

	ref, err := c.FindTaxCode(ctx, percent)
	if err != nil {
		return TaxCodeRef{}, err
	}
	if ref.TaxCodeID != "" {
		c.taxCodes.put(bps, ref)
		return ref, nil
	}

	agencyID, err := c.findOrCreateTaxAgency(ctx, label)
	if err != nil {
		return TaxCodeRef{}, err
	}

	name := taxCodeName(label, percent)
	body := qbTaxServiceRequest{
		TaxCode: name,
		TaxRateDetails: []qbTaxRateDetailCreate{{
			TaxRateName:     name,
			RateValue:       percent,
			TaxAgencyID:     agencyID,
			TaxApplicableOn: "Sales",
		}},
	}
	created, err := c.doAPI(ctx, "POST", "/taxservice/taxcode", body)
	if err != nil {
		// Check-then-create is not atomic and River runs invoice workers
		// concurrently — the same race FindOrCreateTerm documents. A second
		// lookup answers whether the rival attempt got there first.
		if found, findErr := c.FindTaxCode(ctx, percent); findErr == nil && found.TaxCodeID != "" {
			c.taxCodes.put(bps, found)
			return found, nil
		}
		return TaxCodeRef{}, fmt.Errorf("create QB tax code: %w", err)
	}

	var resp qbTaxServiceResponse
	if err := json.Unmarshal(created, &resp); err != nil {
		return TaxCodeRef{}, fmt.Errorf("unmarshal QB tax code response: %w", err)
	}
	if resp.TaxCodeID == "" || len(resp.TaxRateDetails) == 0 {
		return TaxCodeRef{}, fmt.Errorf("%w: QuickBooks created a tax code with no rate", ErrBadRequest)
	}
	out := TaxCodeRef{TaxCodeID: resp.TaxCodeID, TaxRateID: resp.TaxRateDetails[0].TaxRateID}
	c.taxCodes.put(bps, out)
	return out, nil
}

// FindTaxCode returns the refs for a rate the company already has, or a zero
// TaxCodeRef when it has none. It never writes, which is what shadow billing
// needs: a proof run reports the tax an invoice would carry without creating
// anything in the merchant's books.
func (c *QBClient) FindTaxCode(ctx context.Context, percent float64) (TaxCodeRef, error) {
	rateBody, err := c.doAPI(ctx, "GET", "/query?query="+urlEncode("select * from TaxRate maxresults 200"), nil)
	if err != nil {
		return TaxCodeRef{}, fmt.Errorf("query QB tax rates: %w", err)
	}
	var rates qbTaxRateQueryResponse
	if err := json.Unmarshal(rateBody, &rates); err != nil {
		return TaxCodeRef{}, fmt.Errorf("unmarshal QB tax rates: %w", err)
	}

	codeBody, err := c.doAPI(ctx, "GET", "/query?query="+urlEncode("select * from TaxCode maxresults 200"), nil)
	if err != nil {
		return TaxCodeRef{}, fmt.Errorf("query QB tax codes: %w", err)
	}
	var codes qbTaxCodeQueryResponse
	if err := json.Unmarshal(codeBody, &codes); err != nil {
		return TaxCodeRef{}, fmt.Errorf("unmarshal QB tax codes: %w", err)
	}

	return matchTaxCode(rates.QueryResponse.TaxRate, codes.QueryResponse.TaxCode, percent), nil
}

// isActive reads QBO's tri-state Active flag: present and true, present and
// false, or absent. Absent means active — the built-in TAX and NON codes carry
// no Active field at all.
func isActive(flag *bool) bool { return flag == nil || *flag }

// matchTaxCode picks the code that charges exactly the rate asked for, or a
// zero TaxCodeRef when the company has none.
//
// Split from the two queries above so the rules below can be tested without a
// QuickBooks. Each one rejects a code that would bill something other than the
// rate requested, which is the only outcome worth protecting against here — a
// near miss is a wrong invoice, and a wrong invoice is a wrong amount of money
// collected from a real customer.
func matchTaxCode(rates []qbTaxRate, codes []qbTaxCode, percent float64) TaxCodeRef {
	want := rateBasisPoints(percent)

	matching := make(map[string]bool)
	for _, r := range rates {
		// A deactivated rate still answers the query and still has the right
		// value. Referencing one produces an invoice QBO rejects, cached for
		// the life of the client — forget() exists to recover from that, but
		// only after the first failure has already happened.
		if rateBasisPoints(r.RateValue) == want && isActive(r.Active) {
			matching[r.ID] = true
		}
	}
	if len(matching) == 0 {
		return TaxCodeRef{}
	}

	for _, code := range codes {
		if !code.Taxable || !isActive(code.Active) {
			continue
		}
		// A code carrying more than one rate charges their sum, which is not
		// the rate asked for even when one member matches. Skip it rather than
		// bill a combined rate the shop did not configure.
		details := code.SalesTaxRateList.TaxRateDetail
		if len(details) != 1 {
			continue
		}
		if matching[details[0].TaxRateRef.Value] {
			return TaxCodeRef{TaxCodeID: code.ID, TaxRateID: details[0].TaxRateRef.Value}
		}
	}
	return TaxCodeRef{}
}

// findOrCreateTaxAgency returns the agency a created rate is reported under.
// QBO requires one on every rate; a company that has never charged sales tax
// has none, so one is created with the shop's own tax label.
func (c *QBClient) findOrCreateTaxAgency(ctx context.Context, label string) (string, error) {
	body, err := c.doAPI(ctx, "GET", "/query?query="+urlEncode("select * from TaxAgency maxresults 50"), nil)
	if err != nil {
		return "", fmt.Errorf("query QB tax agencies: %w", err)
	}
	var resp qbTaxAgencyQueryResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return "", fmt.Errorf("unmarshal QB tax agencies: %w", err)
	}
	for _, a := range resp.QueryResponse.TaxAgency {
		if a.TaxTrackedOnSales {
			return a.ID, nil
		}
	}

	name := strings.TrimSpace(label)
	if name == "" {
		name = "Sales Tax"
	}
	created, err := c.doAPI(ctx, "POST", "/taxagency", qbTaxAgencyRequest{DisplayName: name})
	if err != nil {
		return "", fmt.Errorf("create QB tax agency: %w", err)
	}
	var agency qbTaxAgencyResponse
	if err := json.Unmarshal(created, &agency); err != nil {
		return "", fmt.Errorf("unmarshal QB tax agency response: %w", err)
	}
	return agency.TaxAgency.ID, nil
}
