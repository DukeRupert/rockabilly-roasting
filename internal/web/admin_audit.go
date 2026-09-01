package web

import (
	"context"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/dukerupert/hiri/internal/app"
	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/store"
	"github.com/dukerupert/hiri/internal/ui/admin"
)

// handleAdminAuditList renders the audit log with search, filters, and paging.
//
// The log is the record staff reach for when something has already gone wrong,
// which means the question is always narrow — what did this person do, what
// happened to this order, what fired last Tuesday. An unfiltered reverse-
// chronological wall answers none of those, so every control here exists to
// turn one of those questions into a query.
func (d *Deps) handleAdminAuditList(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	q := r.URL.Query()

	search := strings.TrimSpace(q.Get("q"))
	actorType := normalizeAuditActorType(q.Get("actor_type"))
	area := strings.TrimSpace(q.Get("area"))
	action := strings.TrimSpace(q.Get("action"))
	resourceType := strings.TrimSpace(q.Get("resource_type"))
	sort := normalizeAuditSort(q.Get("sort"))
	dateRange := normalizeDateRange(q.Get("range"))
	from, to := listDateBounds(dateRange, q.Get("from"), q.Get("to"), d.MerchantTZ, time.Now())

	// Identity filters arrive from links rather than controls — "audit log" on a
	// customer page, or a click on an actor in the table. They render as
	// removable chips so it is never a mystery why the list is short.
	actorID := parseUUIDParam(q.Get("actor_id"))
	resourceID := parseUUIDParam(q.Get("resource_id"))
	customerID := parseUUIDParam(q.Get("customer_id"))

	page := 1
	if p, err := strconv.Atoi(q.Get("page")); err == nil && p > 0 {
		page = p
	}

	perPage := 50
	filter := store.AuditFilter{
		Search:     search,
		ActorID:    actorID,
		ResourceID: resourceID,
		CustomerID: customerID,
		From:       from,
		To:         to,
		Sort:       sort,
		Limit:      perPage + 1,
		Offset:     (page - 1) * perPage,
	}
	if actorType != "" {
		filter.ActorType = &actorType
	}
	if area != "" {
		filter.ActionArea = &area
	}
	if resourceType != "" {
		filter.ResourceType = &resourceType
	}
	action = auditActionForArea(action, area)
	if action != "" {
		filter.Action = &action
	}

	var entries []domain.AuditEntry
	var totalCount int
	var facets app.AuditFacets
	var actorLabel, resourceLabel, customerLabel string

	err := store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		var txErr error
		entries, txErr = d.AuditQueryService.List(ctx, tx, filter)
		if txErr != nil {
			return txErr
		}
		// See AuditFilter.Narrows: an unfiltered count is a scan of the whole
		// log to produce a number nobody came here for. Zero reads as "no
		// total" to the template, which is the same thing an empty result set
		// renders, so there is one code path either way.
		if filter.Narrows() {
			totalCount, txErr = d.AuditQueryService.Count(ctx, tx, filter)
			if txErr != nil {
				return txErr
			}
		}
		facets, txErr = d.AuditQueryService.ListFacets(ctx, tx, area)
		if txErr != nil {
			return txErr
		}

		// Name the chips. A raw uuid tells a staffer nothing about who they
		// are looking at, and the label is only needed for the one or two ids
		// actually pinned — cheap enough to resolve per request.
		actorLabel = auditActorLabel(ctx, tx, d, actorID, entries)
		resourceLabel = auditResourceLabel(resourceID, entries)
		if customerID != nil {
			if c, cErr := d.CustomerService.GetCustomer(ctx, tx, *customerID); cErr == nil && c != nil {
				customerLabel = customerDisplayName(c)
			}
		}
		return nil
	})
	if err != nil {
		Error(w, r, err)
		return
	}

	hasMore := len(entries) > perPage
	if hasMore {
		entries = entries[:perPage]
	}

	name, role := staffNameRole(r)
	props := admin.AuditListProps{
		Entries:         entries,
		Search:          search,
		ActorTypeFilter: actorType,
		Area:            area,
		ActionFilter:    action,
		ResourceFilter:  resourceType,
		ActorID:         uuidParam(actorID),
		ActorLabel:      actorLabel,
		ResourceID:      uuidParam(resourceID),
		ResourceLabel:   resourceLabel,
		CustomerID:      uuidParam(customerID),
		CustomerLabel:   customerLabel,
		Range:           dateRange,
		From:            minRawDate(q.Get("from"), dateRange),
		To:              minRawDate(q.Get("to"), dateRange),
		Sort:            string(sort),
		Areas:           facets.Areas,
		Actions:         facets.Actions,
		ResourceTypes:   facets.ResourceTypes,
		TotalCount:      totalCount,
		Page:            page,
		PerPage:         perPage,
		HasMore:         hasMore,
		MerchantTZ:      d.MerchantTZ,
		StaffName:       name,
		StaffRole:       role,
	}

	if IsHTMX(r) {
		admin.AuditListContent(props).Render(ctx, w) //nolint:errcheck
		return
	}
	admin.AuditList(props).Render(ctx, w) //nolint:errcheck
}

// auditActionForArea keeps the two action controls consistent.
//
// An exact action only means anything inside its own area, and the area select
// resets the action when it changes — but a hand-edited or stale URL can still
// pair "order" with "product.updated". Honouring that combination would render
// an empty list under two controls that each look reasonable, so the mismatched
// action is dropped instead.
func auditActionForArea(action, area string) string {
	if action == "" || area == "" || !strings.HasPrefix(action, area+".") {
		return ""
	}
	return action
}

// normalizeAuditActorType clamps ?actor_type= to the three actor kinds.
func normalizeAuditActorType(v string) string {
	switch domain.AuditActorType(v) {
	case domain.AuditActorTypeStaff, domain.AuditActorTypeCustomer, domain.AuditActorTypeSystem:
		return v
	default:
		return ""
	}
}

// normalizeAuditSort clamps ?sort= to the two directions the log offers.
func normalizeAuditSort(v string) store.AuditSort {
	if store.AuditSort(v) == store.AuditSortOldest {
		return store.AuditSortOldest
	}
	return store.AuditSortNewest
}

// parseUUIDParam reads an optional uuid query parameter, treating anything
// unparseable as absent — a mangled link should widen the list, not 400.
func parseUUIDParam(raw string) *uuid.UUID {
	id, err := uuid.Parse(strings.TrimSpace(raw))
	if err != nil {
		return nil
	}
	return &id
}

// uuidParam renders an optional uuid back into a query string value.
func uuidParam(id *uuid.UUID) string {
	if id == nil {
		return ""
	}
	return id.String()
}

// auditActorLabel names the pinned actor for the filter chip.
//
// The visible page usually already carries the name — every entry by that
// actor records it — so the rows are consulted first and the customer lookup
// is a fallback for the case where the filter combination excluded all of
// them. A failed lookup leaves the chip unlabelled rather than failing the
// page: the filter still works, it just reads as an id.
func auditActorLabel(ctx context.Context, tx pgx.Tx, d *Deps, actorID *uuid.UUID, entries []domain.AuditEntry) string {
	if actorID == nil {
		return ""
	}
	if i := slices.IndexFunc(entries, func(e domain.AuditEntry) bool {
		return e.ActorID != nil && *e.ActorID == *actorID && e.ActorName != ""
	}); i >= 0 {
		return entries[i].ActorName
	}
	if c, err := d.CustomerService.GetCustomer(ctx, tx, *actorID); err == nil && c != nil {
		return customerDisplayName(c)
	}
	return ""
}

// auditResourceLabel names the pinned resource for the filter chip.
//
// The type alone is not a name: every order pins as "order", so two different
// records produce an identical chip and the operator cannot tell which one
// they are looking at. The id fragment is what distinguishes them — the same
// eight characters the Resource column shows — so the label carries both, and
// falls back to the fragment when no visible row names the type.
func auditResourceLabel(resourceID *uuid.UUID, entries []domain.AuditEntry) string {
	if resourceID == nil {
		return ""
	}
	short := resourceID.String()[:8]
	if i := slices.IndexFunc(entries, func(e domain.AuditEntry) bool {
		return e.ResourceID == *resourceID
	}); i >= 0 {
		return entries[i].ResourceType + " " + short
	}
	return short
}
