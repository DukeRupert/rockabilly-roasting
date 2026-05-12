package web

import (
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/dukerupert/hiri/internal/app"
	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/store"
	"github.com/dukerupert/hiri/internal/ui/admin"
)

// handleAdminBoxPresets renders the box preset CRUD page.
func (d *Deps) handleAdminBoxPresets(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var presets []domain.BoxPreset
	err := store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		var txErr error
		presets, txErr = d.FulfillmentService.ListBoxPresets(ctx, tx)
		return txErr
	})
	if err != nil {
		slog.Error("admin box presets: load", "error", err)
		Error(w, r, err)
		return
	}

	name, role := staffNameRole(r)
	props := admin.BoxPresetsProps{
		Presets:   presets,
		Flash:     r.URL.Query().Get("flash"),
		StaffName: name,
		StaffRole: role,
	}

	if IsHTMX(r) {
		admin.BoxPresetsContent(props).Render(ctx, w) //nolint:errcheck
		return
	}
	admin.BoxPresets(props).Render(ctx, w) //nolint:errcheck
}

// handleAdminBoxPresetCreate creates a new box preset.
func (d *Deps) handleAdminBoxPresetCreate(w http.ResponseWriter, r *http.Request) {
	in, err := parseBoxPresetInput(r)
	if err != nil {
		http.Redirect(w, r, "/admin/settings/box-presets?flash="+err.Error(), http.StatusSeeOther)
		return
	}

	ctx := r.Context()
	actor := staffActor(r)
	err = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		_, txErr := d.FulfillmentService.CreateBoxPreset(ctx, tx, in, actor)
		return txErr
	})
	if err != nil {
		slog.Error("admin box presets: create", "error", err)
		http.Redirect(w, r, "/admin/settings/box-presets?flash=Failed+to+add+preset", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/admin/settings/box-presets?flash=Preset+added", http.StatusSeeOther)
}

// handleAdminBoxPresetUpdate edits an existing preset.
func (d *Deps) handleAdminBoxPresetUpdate(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	in, err := parseBoxPresetInput(r)
	if err != nil {
		http.Redirect(w, r, "/admin/settings/box-presets?flash="+err.Error(), http.StatusSeeOther)
		return
	}

	ctx := r.Context()
	actor := staffActor(r)
	err = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		_, txErr := d.FulfillmentService.UpdateBoxPreset(ctx, tx, id, in, actor)
		return txErr
	})
	if err != nil {
		slog.Error("admin box presets: update", "error", err, "id", id)
		http.Redirect(w, r, "/admin/settings/box-presets?flash=Failed+to+save+preset", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/admin/settings/box-presets?flash=Preset+saved", http.StatusSeeOther)
}

// handleAdminBoxPresetDelete removes a preset.
func (d *Deps) handleAdminBoxPresetDelete(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	ctx := r.Context()
	actor := staffActor(r)
	err = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		return d.FulfillmentService.DeleteBoxPreset(ctx, tx, id, actor)
	})
	if err != nil {
		slog.Error("admin box presets: delete", "error", err, "id", id)
		http.Redirect(w, r, "/admin/settings/box-presets?flash=Failed+to+delete+preset", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/admin/settings/box-presets?flash=Preset+deleted", http.StatusSeeOther)
}

func parseBoxPresetInput(r *http.Request) (app.BoxPresetInput, error) {
	if err := r.ParseForm(); err != nil {
		return app.BoxPresetInput{}, err
	}
	length, err := strconv.ParseFloat(strings.TrimSpace(r.FormValue("length_in")), 64)
	if err != nil {
		return app.BoxPresetInput{}, app.ErrBoxPresetDimensionsInvalid
	}
	width, err := strconv.ParseFloat(strings.TrimSpace(r.FormValue("width_in")), 64)
	if err != nil {
		return app.BoxPresetInput{}, app.ErrBoxPresetDimensionsInvalid
	}
	height, err := strconv.ParseFloat(strings.TrimSpace(r.FormValue("height_in")), 64)
	if err != nil {
		return app.BoxPresetInput{}, app.ErrBoxPresetDimensionsInvalid
	}
	maxOz, err := strconv.ParseFloat(strings.TrimSpace(r.FormValue("max_weight_oz")), 64)
	if err != nil {
		return app.BoxPresetInput{}, app.ErrBoxPresetMaxWeightInvalid
	}
	sortOrder := 0
	if raw := strings.TrimSpace(r.FormValue("sort_order")); raw != "" {
		if n, parseErr := strconv.Atoi(raw); parseErr == nil {
			sortOrder = n
		}
	}
	return app.BoxPresetInput{
		Name:        strings.TrimSpace(r.FormValue("name")),
		LengthIn:    length,
		WidthIn:     width,
		HeightIn:    height,
		MaxWeightOz: maxOz,
		SortOrder:   sortOrder,
	}, nil
}
