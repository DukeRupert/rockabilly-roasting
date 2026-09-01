package store

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/store/sqlcgen"
)

// AuditStore provides database access for audit log entries.
type AuditStore struct{}

// NewAuditStore creates a new AuditStore.
func NewAuditStore() *AuditStore {
	return &AuditStore{}
}

// CreateAuditEntryParams holds the fields needed to create an audit entry.
type CreateAuditEntryParams struct {
	ActorType     domain.AuditActorType
	ActorID       *uuid.UUID
	ActorName     string
	Action        string
	ResourceType  string
	ResourceID    uuid.UUID
	AfterSnapshot json.RawMessage
	RequestID     string
	IPAddress     *string
	Reason        *string
	Metadata      map[string]any
}

// Create inserts a new audit log entry and returns it.
func (s *AuditStore) Create(ctx context.Context, tx pgx.Tx, p CreateAuditEntryParams) (*domain.AuditEntry, error) {
	row, err := sqlcgen.New(tx).CreateAuditEntry(ctx, sqlcgen.CreateAuditEntryParams{
		ID:            uuid.New(),
		ActorType:     string(p.ActorType),
		ActorID:       p.ActorID,
		ActorName:     p.ActorName,
		Action:        p.Action,
		ResourceType:  p.ResourceType,
		ResourceID:    p.ResourceID,
		AfterSnapshot: p.AfterSnapshot,
		RequestID:     p.RequestID,
		IpAddress:     p.IPAddress,
		Reason:        p.Reason,
		Metadata:      metadataToJSON(p.Metadata),
	})
	if err != nil {
		return nil, fmt.Errorf("insert audit entry: %w", err)
	}
	return auditEntryFromRow(row), nil
}

// ListByResource returns audit entries for a specific resource.
func (s *AuditStore) ListByResource(ctx context.Context, tx pgx.Tx, resourceType string, resourceID uuid.UUID) ([]domain.AuditEntry, error) {
	rows, err := sqlcgen.New(tx).ListAuditByResource(ctx, sqlcgen.ListAuditByResourceParams{
		ResourceType: resourceType,
		ResourceID:   resourceID,
	})
	if err != nil {
		return nil, fmt.Errorf("list audit by resource: %w", err)
	}
	return auditEntriesFromRows(rows), nil
}

// ListByActor returns audit entries for a specific actor.
func (s *AuditStore) ListByActor(ctx context.Context, tx pgx.Tx, actorID uuid.UUID) ([]domain.AuditEntry, error) {
	rows, err := sqlcgen.New(tx).ListAuditByActor(ctx, &actorID)
	if err != nil {
		return nil, fmt.Errorf("list audit by actor: %w", err)
	}
	return auditEntriesFromRows(rows), nil
}

// ListByAction returns audit entries for a specific action.
func (s *AuditStore) ListByAction(ctx context.Context, tx pgx.Tx, action string) ([]domain.AuditEntry, error) {
	rows, err := sqlcgen.New(tx).ListAuditByAction(ctx, action)
	if err != nil {
		return nil, fmt.Errorf("list audit by action: %w", err)
	}
	return auditEntriesFromRows(rows), nil
}

// ListForCustomer returns audit entries that relate to a single customer,
// newest first, capped at limit. See AuditFilter.CustomerID for what "relate"
// means; it delegates so the customer detail page's timeline and the audit
// log's customer filter can never disagree about which entries those are.
func (s *AuditStore) ListForCustomer(ctx context.Context, tx pgx.Tx, customerID uuid.UUID, limit int) ([]domain.AuditEntry, error) {
	if limit <= 0 {
		limit = 25
	}
	return s.List(ctx, tx, AuditFilter{CustomerID: &customerID, Limit: limit})
}

// AuditSort names the orderings the audit list offers. The log is a
// chronology and nothing else, so date is the only axis worth sorting on —
// the only question is which end of it you want to read from.
type AuditSort string

const (
	AuditSortNewest AuditSort = "newest"
	AuditSortOldest AuditSort = "oldest"
)

// AuditFilter holds optional filters for listing audit entries.
//
// Every dimension here is served by an index (migration 085) — the audit log is
// append-only and grows without bound, so a filter that falls back to a
// sequential scan gets slower every week it is left in place.
//
// "Served by an index" includes the awkward shapes: ResourceID has one of its
// own rather than riding idx_audit_log_resource, where it is the second column
// and an id without a type beside it would degrade to a full index scan.
type AuditFilter struct {
	// Search is one free-text term. See auditSearchClause for how it is read:
	// an identifier searches identifiers, anything else searches names and
	// actions.
	Search string

	ActorType *string
	ActorID   *uuid.UUID

	// Action matches one exact action ("order.refunded"); ActionArea matches
	// every action in a namespace ("order"). They are separate fields because
	// the UI offers them as separate controls — pick an area, then optionally
	// narrow to a single action within it.
	Action     *string
	ActionArea *string

	ResourceType *string
	ResourceID   *uuid.UUID

	// CustomerID matches everything about one customer, from either side: the
	// things they did themselves (logins, self-service edits) and the things
	// done to their account (a staff approval, a wholesale suspension). Those
	// are two different columns, which is why it cannot be expressed with
	// ActorID and ResourceID — and why "show me this customer" needs its own
	// field rather than being assembled by the caller.
	CustomerID *uuid.UUID

	// From and To bound created_at inclusively. The caller resolves presets
	// like "this month" into instants in the merchant's timezone.
	From *time.Time
	To   *time.Time

	Sort   AuditSort
	Limit  int
	Offset int
}

// auditColumns is the full projection the hand-written audit reads share, so
// a column added to the table shows up in one place rather than two.
const auditColumns = `id, actor_type, actor_id, actor_name, action, resource_type, resource_id,
	                  after_snapshot, request_id, ip_address, reason, metadata, created_at`

// auditSearchClause interprets one free-text term.
//
// The two readings are kept deliberately disjoint rather than OR'd together.
// A term that looks like an identifier searches identifiers; anything else
// searches actor_name and action. OR-ing an equality against two trigram
// matches would force the planner to scan the whole table to satisfy the one
// branch it has no index for, which is exactly the outcome migration 085
// exists to avoid.
//
// The text half stops at actor_name and action on purpose: those are the two
// columns the list actually renders. Matching on reason or metadata would
// return rows whose reason for matching is invisible on screen.
func auditSearchClause(term string, argN int) (string, []any, int) {
	if id, err := uuid.Parse(term); err == nil {
		return fmt.Sprintf(" AND (actor_id = $%d OR resource_id = $%d)", argN, argN),
			[]any{id}, argN + 1
	}
	// The list renders the first 8 characters of each resource id, so staff
	// paste that fragment back in. Anything shorter is treated as text —
	// otherwise a word like "added" would be read as a hex fragment.
	if isHexFragment(term) {
		return fmt.Sprintf(" AND resource_id::text LIKE $%d || '%%'", argN),
			[]any{strings.ToLower(term)}, argN + 1
	}
	return fmt.Sprintf(" AND (actor_name ILIKE '%%' || $%d || '%%' OR action ILIKE '%%' || $%d || '%%')", argN, argN),
		[]any{term}, argN + 1
}

// isHexFragment reports whether a term reads as the leading chunk of a uuid.
// The 8-character floor matches the fragment the list displays.
func isHexFragment(term string) bool {
	if len(term) < 8 {
		return false
	}
	for _, r := range strings.ToLower(term) {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') && r != '-' {
			return false
		}
	}
	return true
}

// auditWhere grows query with the filter's WHERE clauses and returns the query,
// its args, and the next free placeholder.
//
// List and Count both build their WHERE here. If they drift, the "X–Y of Z"
// total starts contradicting the rows underneath it.
func auditWhere(query string, f AuditFilter) (string, []any, int) {
	args := []any{}
	argN := 1
	add := func(clause string, v any) {
		query += fmt.Sprintf(" AND "+clause, argN)
		args = append(args, v)
		argN++
	}

	if f.ActorType != nil {
		add("actor_type = $%d", *f.ActorType)
	}
	if f.ActorID != nil {
		add("actor_id = $%d", *f.ActorID)
	}
	if f.Action != nil {
		add("action = $%d", *f.Action)
	}
	if f.ActionArea != nil {
		add("split_part(action, '.', 1) = $%d", *f.ActionArea)
	}
	if f.ResourceType != nil {
		add("resource_type = $%d", *f.ResourceType)
	}
	if f.ResourceID != nil {
		add("resource_id = $%d", *f.ResourceID)
	}
	if f.CustomerID != nil {
		query += fmt.Sprintf(
			" AND ((actor_type = 'customer' AND actor_id = $%d) OR (resource_type = 'customer' AND resource_id = $%d))",
			argN, argN)
		args = append(args, *f.CustomerID)
		argN++
	}
	if f.From != nil {
		add("created_at >= $%d", *f.From)
	}
	if f.To != nil {
		add("created_at <= $%d", *f.To)
	}
	if term := strings.TrimSpace(f.Search); term != "" {
		clause, sargs, next := auditSearchClause(term, argN)
		query += clause
		args = append(args, sargs...)
		argN = next
	}
	return query, args, argN
}

// Narrows reports whether the filter restricts the log at all.
//
// It exists so the list can decline to count. COUNT(*) over an unfiltered
// audit_log is a sequential scan of every row ever written, and on the default
// view it would run on every page load to answer "how many entries exist in
// total" — a number nobody opened this page to learn. Once any filter is on,
// the count is both cheap and the thing the operator actually wants.
func (f AuditFilter) Narrows() bool {
	return f.Search != "" || f.ActorType != nil || f.ActorID != nil ||
		f.Action != nil || f.ActionArea != nil || f.ResourceType != nil ||
		f.ResourceID != nil || f.CustomerID != nil || f.From != nil || f.To != nil
}

// auditOrderBy returns the ORDER BY for a sort key. Newest-first is the
// default because an audit log is read from the top.
func auditOrderBy(sort AuditSort) string {
	if sort == AuditSortOldest {
		return " ORDER BY created_at ASC"
	}
	return " ORDER BY created_at DESC"
}

// List returns audit entries matching the given filter, paginated.
func (s *AuditStore) List(ctx context.Context, tx pgx.Tx, f AuditFilter) ([]domain.AuditEntry, error) {
	query, args, argN := auditWhere(`SELECT `+auditColumns+` FROM audit_log WHERE true`, f)
	query += auditOrderBy(f.Sort)

	if f.Limit > 0 {
		query += fmt.Sprintf(" LIMIT $%d", argN)
		args = append(args, f.Limit)
		argN++
	}
	if f.Offset > 0 {
		query += fmt.Sprintf(" OFFSET $%d", argN)
		args = append(args, f.Offset)
	}

	rows, err := tx.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list audit entries: %w", err)
	}
	return scanAuditEntries(rows)
}

// Count returns how many entries match the filter, ignoring limit and offset.
func (s *AuditStore) Count(ctx context.Context, tx pgx.Tx, f AuditFilter) (int, error) {
	query, args, _ := auditWhere(`SELECT COUNT(*) FROM audit_log WHERE true`, f)

	var count int
	if err := tx.QueryRow(ctx, query, args...).Scan(&count); err != nil {
		return 0, fmt.Errorf("count audit entries: %w", err)
	}
	return count, nil
}

// ListActionAreas returns the action namespaces present in the log, sorted.
//
// The dropdowns are built from what the table actually contains rather than
// from the action constants: there are 222 constants across 31 namespaces, and
// most of any one shop's are never written. A list of options that all return
// rows beats a complete one that mostly does not.
//
// The recursive form is a loose index scan, and it is not decoration. Postgres
// has no skip scan, so the obvious SELECT DISTINCT reads every row — a
// sequential scan of an append-only table, run on every page load including
// every keystroke in the search box. This walks the expression index one
// distinct value at a time instead: ~30 index probes rather than N heap rows,
// so the cost tracks the number of areas, which barely changes, not the size
// of the log, which only grows.
func (s *AuditStore) ListActionAreas(ctx context.Context, tx pgx.Tx) ([]string, error) {
	rows, err := tx.Query(ctx, `
		WITH RECURSIVE areas AS (
			(SELECT split_part(action, '.', 1) AS area FROM audit_log ORDER BY 1 LIMIT 1)
			UNION ALL
			SELECT (SELECT split_part(action, '.', 1) FROM audit_log
			         WHERE split_part(action, '.', 1) > areas.area ORDER BY 1 LIMIT 1)
			  FROM areas WHERE areas.area IS NOT NULL
		)
		SELECT area FROM areas WHERE area IS NOT NULL`)
	if err != nil {
		return nil, fmt.Errorf("list audit action areas: %w", err)
	}
	return scanStrings(rows, "audit action areas")
}

// ListActionsInArea returns the distinct actions inside one namespace, sorted.
// Only queried once an area is chosen, which keeps the second dropdown short.
func (s *AuditStore) ListActionsInArea(ctx context.Context, tx pgx.Tx, area string) ([]string, error) {
	rows, err := tx.Query(ctx,
		`SELECT DISTINCT action FROM audit_log WHERE split_part(action, '.', 1) = $1 ORDER BY 1`, area)
	if err != nil {
		return nil, fmt.Errorf("list audit actions in area: %w", err)
	}
	return scanStrings(rows, "audit actions in area")
}

// ListResourceTypes returns the resource types present in the log, sorted.
//
// Worth its own control alongside the action area because the two genuinely
// differ: an "email.shipment_sent" event is recorded against resource_type
// "order", so filtering by area finds the mail and filtering by resource finds
// everything that ever touched that order.
//
// Same loose index scan as ListActionAreas, for the same reason — see there.
func (s *AuditStore) ListResourceTypes(ctx context.Context, tx pgx.Tx) ([]string, error) {
	rows, err := tx.Query(ctx, `
		WITH RECURSIVE types AS (
			(SELECT resource_type AS t FROM audit_log ORDER BY 1 LIMIT 1)
			UNION ALL
			SELECT (SELECT resource_type FROM audit_log
			         WHERE resource_type > types.t ORDER BY 1 LIMIT 1)
			  FROM types WHERE types.t IS NOT NULL
		)
		SELECT t FROM types WHERE t IS NOT NULL`)
	if err != nil {
		return nil, fmt.Errorf("list audit resource types: %w", err)
	}
	return scanStrings(rows, "audit resource types")
}

func scanStrings(rows pgx.Rows, what string) ([]string, error) {
	defer rows.Close()
	var out []string
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, fmt.Errorf("scan %s: %w", what, err)
		}
		if v != "" {
			out = append(out, v)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate %s: %w", what, err)
	}
	return out, nil
}

// ListByResourceIDsWithActionPrefix returns audit entries for any of the given
// resource ids whose action starts with the prefix, newest first.
//
// It exists because some events are recorded against a *related* resource
// rather than the one whose page you are looking at: a skipped delivery stop is
// audited against the order, not the route, so a route's own resource_id lookup
// would miss it. The prefix keeps the join narrow — a route page wants the
// stop's "delivery_route.*" events, not the order's entire history.
func (s *AuditStore) ListByResourceIDsWithActionPrefix(
	ctx context.Context,
	tx pgx.Tx,
	resourceType string,
	ids []uuid.UUID,
	actionPrefix string,
) ([]domain.AuditEntry, error) {
	if len(ids) == 0 {
		return nil, nil
	}

	rows, err := tx.Query(ctx,
		`SELECT `+auditColumns+`
		   FROM audit_log
		  WHERE resource_type = $1 AND resource_id = ANY($2) AND action LIKE $3 || '%'
		  ORDER BY created_at DESC`,
		resourceType, ids, actionPrefix,
	)
	if err != nil {
		return nil, fmt.Errorf("list audit by resource ids: %w", err)
	}
	return scanAuditEntries(rows)
}

// --- Row converters ---

// scanAuditEntries drains a result set projecting auditColumns. Every
// hand-written audit query shares it so a column added to the projection has
// exactly one place to be read.
func scanAuditEntries(rows pgx.Rows) ([]domain.AuditEntry, error) {
	defer rows.Close()

	var entries []domain.AuditEntry
	for rows.Next() {
		var e domain.AuditEntry
		var actorType string
		var metadataJSON json.RawMessage
		if err := rows.Scan(
			&e.ID, &actorType, &e.ActorID, &e.ActorName,
			&e.Action, &e.ResourceType, &e.ResourceID,
			&e.AfterSnapshot, &e.RequestID, &e.IPAddress,
			&e.Reason, &metadataJSON, &e.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan audit entry: %w", err)
		}
		e.ActorType = domain.AuditActorType(actorType)
		e.Metadata = metadataFromJSON(metadataJSON)
		entries = append(entries, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate audit entries: %w", err)
	}
	return entries, nil
}

func auditEntryFromRow(r sqlcgen.AuditLog) *domain.AuditEntry {
	return &domain.AuditEntry{
		ID:            r.ID,
		ActorType:     domain.AuditActorType(r.ActorType),
		ActorID:       r.ActorID,
		ActorName:     r.ActorName,
		Action:        r.Action,
		ResourceType:  r.ResourceType,
		ResourceID:    r.ResourceID,
		AfterSnapshot: r.AfterSnapshot,
		RequestID:     r.RequestID,
		IPAddress:     r.IpAddress,
		Reason:        r.Reason,
		Metadata:      metadataFromJSON(r.Metadata),
		CreatedAt:     r.CreatedAt,
	}
}

func auditEntriesFromRows(rows []sqlcgen.AuditLog) []domain.AuditEntry {
	entries := make([]domain.AuditEntry, len(rows))
	for i, r := range rows {
		entries[i] = *auditEntryFromRow(r)
	}
	return entries
}
