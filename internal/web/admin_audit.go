package web

import (
	"net/http"
	"strconv"

	"github.com/jackc/pgx/v5"

	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/store"
	"github.com/dukerupert/hiri/internal/ui/admin"
)

func (d *Deps) handleAdminAuditList(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	actorTypeFilter := r.URL.Query().Get("actor_type")
	actionFilter := r.URL.Query().Get("action")
	resourceFilter := r.URL.Query().Get("resource_type")
	pageStr := r.URL.Query().Get("page")

	page := 1
	if p, err := strconv.Atoi(pageStr); err == nil && p > 0 {
		page = p
	}

	perPage := 50
	filter := store.AuditFilter{
		Limit:  perPage + 1,
		Offset: (page - 1) * perPage,
	}

	if actorTypeFilter != "" {
		filter.ActorType = &actorTypeFilter
	}
	if actionFilter != "" {
		filter.Action = &actionFilter
	}
	if resourceFilter != "" {
		filter.ResourceType = &resourceFilter
	}

	var entries []domain.AuditEntry

	err := store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		var txErr error
		entries, txErr = d.AuditQueryService.List(ctx, tx, filter)
		return txErr
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
		ActorTypeFilter: actorTypeFilter,
		ActionFilter:    actionFilter,
		ResourceFilter:  resourceFilter,
		Page:            page,
		PerPage:         perPage,
		HasMore:         hasMore,
		StaffName:       name,
		StaffRole:       role,
	}

	if IsHTMX(r) {
		admin.AuditListContent(props).Render(ctx, w) //nolint:errcheck
		return
	}
	admin.AuditList(props).Render(ctx, w) //nolint:errcheck
}
