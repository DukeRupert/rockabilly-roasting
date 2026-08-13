package web

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/platform/audit"
	"github.com/dukerupert/hiri/internal/platform/quickbooks"
	"github.com/dukerupert/hiri/internal/store"
	"github.com/dukerupert/hiri/internal/ui/admin"
)

// handleAdminSettings renders the Settings page with integration status and
// merchant-level config (shipping).
func (d *Deps) handleAdminSettings(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	qbStatus := admin.QBConnectionStatus{}
	qbEnabled := d.QBOAuthManager != nil
	var shipping admin.ShippingSettings
	var priceLists []domain.PriceList
	var defaultPriceListID *uuid.UUID

	err := store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		if qbEnabled {
			if status, qbErr := d.QBOAuthManager.Status(ctx, tx); qbErr == nil {
				qbStatus.Connected = status.Connected
				qbStatus.RealmID = status.RealmID
				qbStatus.RefreshExpiresAt = status.RefreshExpiresAt
			}
		}

		lists, listErr := d.PriceListService.List(ctx, tx)
		if listErr != nil {
			return listErr
		}
		priceLists = lists

		defaultID, defErr := d.PriceListService.GetDefaultWholesale(ctx, tx)
		if defErr != nil {
			return defErr
		}
		defaultPriceListID = defaultID

		cfg, cfgErr := d.CheckoutService.GetShippingConfig(ctx, tx)
		if cfgErr != nil {
			return cfgErr
		}
		shipping = admin.ShippingSettings{
			FlatRateCents:           cfg.FlatRateCents,
			FreeShippingThreshold:   cfg.FreeShippingThreshold,
			Currency:                cfg.Currency,
			LocalZipCodes:           cfg.LocalZipCodes,
			LocalDeliveryEnabled:    cfg.LocalDeliveryEnabled,
			LocalPickupEnabled:      cfg.LocalPickupEnabled,
			LocalPickupInstructions: cfg.LocalPickupInstructions,
			LocalDeliveryWeekdays:   cfg.LocalDeliveryWeekdays,
			LocalDeliveryCutoff:     formatCutoffInput(cfg.LocalDeliveryCutoffMinutes),
			OriginName:              cfg.OriginName,
			OriginStreet1:           cfg.OriginStreet1,
			OriginStreet2:           cfg.OriginStreet2,
			OriginCity:              cfg.OriginCity,
			OriginState:             cfg.OriginState,
			OriginZip:               cfg.OriginZip,
			OriginCountry:           cfg.OriginCountry,
			OriginEmail:             cfg.OriginEmail,
			OriginPhone:             cfg.OriginPhone,
			TareWeightOz:            cfg.TareWeightOz,
		}
		return nil
	})
	if err != nil {
		slog.Error("admin settings: load", "error", err)
		Error(w, r, err)
		return
	}

	name, role := staffNameRole(r)
	props := admin.SettingsProps{
		QB:                 qbStatus,
		QBEnabled:          qbEnabled,
		Shipping:           shipping,
		PriceLists:         priceLists,
		DefaultPriceListID: defaultPriceListID,
		Flash:              r.URL.Query().Get("flash"),
		MerchantTZ:         d.MerchantTZ,
		StaffName:          name,
		StaffRole:          role,
	}

	if IsHTMX(r) {
		admin.SettingsContent(props).Render(ctx, w) //nolint:errcheck
		return
	}
	admin.Settings(props).Render(ctx, w) //nolint:errcheck
}

// handleAdminShippingSettingsUpdate persists the edited shipping config and
// records the audit event inside the same transaction.
//
// TODO: when the live-rate provider starts consuming the origin fields,
// tighten state + zip + country validation here.
func (d *Deps) handleAdminShippingSettingsUpdate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	flatRateCents, err := parseDollarsCents(r.FormValue("flat_rate"))
	if err != nil {
		http.Redirect(w, r, "/admin/settings?flash=Invalid+flat+rate", http.StatusSeeOther)
		return
	}
	var threshold *int
	if raw := strings.TrimSpace(r.FormValue("free_threshold")); raw != "" {
		cents, tErr := parseDollarsCents(raw)
		if tErr != nil {
			http.Redirect(w, r, "/admin/settings?flash=Invalid+free-shipping+threshold", http.StatusSeeOther)
			return
		}
		threshold = &cents
	}
	zips := parseZipList(r.FormValue("local_zip_codes"))

	tareOz := 0.0
	if raw := strings.TrimSpace(r.FormValue("tare_weight_oz")); raw != "" {
		oz, tErr := strconv.ParseFloat(raw, 64)
		if tErr != nil || oz < 0 {
			http.Redirect(w, r, "/admin/settings?flash=Invalid+tare+weight", http.StatusSeeOther)
			return
		}
		tareOz = oz
	}

	originCountry := strings.ToUpper(strings.TrimSpace(r.FormValue("origin_country")))
	if originCountry == "" {
		originCountry = "US"
	}

	cutoffMinutes, err := parseCutoffInput(r.FormValue("local_delivery_cutoff"))
	if err != nil {
		http.Redirect(w, r, "/admin/settings?flash=Invalid+delivery+cutoff+time", http.StatusSeeOther)
		return
	}
	weekdays := parseWeekdayCheckboxes(r.Form["local_delivery_weekdays"])
	// A delivery schedule with no days is unschedulable: checkout and the
	// confirmation email would silently drop back to vague phrasing with no
	// hint as to why. Refuse it rather than let the route quietly go dark.
	if r.FormValue("local_delivery_enabled") != "" && len(weekdays) == 0 {
		http.Redirect(w, r, "/admin/settings?flash=Pick+at+least+one+local+delivery+day", http.StatusSeeOther)
		return
	}

	cfg := domain.ShippingConfig{
		FlatRateCents:              flatRateCents,
		FreeShippingThreshold:      threshold,
		Currency:                   "usd",
		LocalZipCodes:              zips,
		LocalDeliveryEnabled:       r.FormValue("local_delivery_enabled") != "",
		LocalPickupEnabled:         r.FormValue("local_pickup_enabled") != "",
		LocalPickupInstructions:    strings.TrimSpace(r.FormValue("local_pickup_instructions")),
		LocalDeliveryWeekdays:      weekdays,
		LocalDeliveryCutoffMinutes: cutoffMinutes,
		OriginName:                 strings.TrimSpace(r.FormValue("origin_name")),
		OriginStreet1:              strings.TrimSpace(r.FormValue("origin_street1")),
		OriginStreet2:              strings.TrimSpace(r.FormValue("origin_street2")),
		OriginCity:                 strings.TrimSpace(r.FormValue("origin_city")),
		OriginState:                strings.ToUpper(strings.TrimSpace(r.FormValue("origin_state"))),
		OriginZip:                  strings.TrimSpace(r.FormValue("origin_zip")),
		OriginCountry:              originCountry,
		OriginEmail:                strings.TrimSpace(r.FormValue("origin_email")),
		OriginPhone:                strings.TrimSpace(r.FormValue("origin_phone")),
		TareWeightOz:               tareOz,
	}

	actor := staffActor(r)
	err = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		return d.CheckoutService.UpdateShippingConfig(ctx, tx, cfg, actor)
	})
	if err != nil {
		slog.Error("admin settings: update shipping", "error", err)
		http.Redirect(w, r, "/admin/settings?flash=Failed+to+save+shipping+settings", http.StatusSeeOther)
		return
	}

	http.Redirect(w, r, "/admin/settings?flash=Shipping+settings+saved", http.StatusSeeOther)
}

// handleAdminDefaultPriceListUpdate sets the store-wide default wholesale price
// list. An empty selection clears the default (wholesale customers without an
// assigned list fall back to base prices).
func (d *Deps) handleAdminDefaultPriceListUpdate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var priceListID *uuid.UUID
	if v := strings.TrimSpace(r.FormValue("default_price_list_id")); v != "" {
		parsed, err := uuid.Parse(v)
		if err != nil {
			http.Redirect(w, r, "/admin/settings?flash=Invalid+price+list", http.StatusSeeOther)
			return
		}
		priceListID = &parsed
	}

	actor := staffActor(r)
	err := store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		return d.PriceListService.SetDefaultWholesale(ctx, tx, priceListID, actor)
	})
	if err != nil {
		slog.Error("admin settings: update default price list", "error", err)
		http.Redirect(w, r, "/admin/settings?flash=Failed+to+save+default+price+list", http.StatusSeeOther)
		return
	}

	http.Redirect(w, r, "/admin/settings?flash=Default+wholesale+price+list+saved", http.StatusSeeOther)
}

// parseDollarsCents converts a dollar amount (e.g. "6.00", "6", "6.5") into
// integer cents.
func parseDollarsCents(raw string) (int, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return 0, fmt.Errorf("empty amount")
	}
	// Accept values like "6", "6.5", "6.50"
	dollars, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, err
	}
	if dollars < 0 {
		return 0, fmt.Errorf("negative amount")
	}
	return int(dollars*100 + 0.5), nil
}

// parseCutoffInput converts an <input type="time"> value ("09:00") into minutes
// past midnight. An empty value means the browser sent nothing — fall back to
// 9am rather than midnight, which would silently push every same-day order to
// the following run.
func parseCutoffInput(raw string) (int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return defaultCutoffMinutes, nil
	}
	// Some browsers append seconds ("09:00:00") when the input has a step.
	t, err := time.Parse("15:04:05", raw)
	if err != nil {
		t, err = time.Parse("15:04", raw)
		if err != nil {
			return 0, fmt.Errorf("parse cutoff %q: %w", raw, err)
		}
	}
	return t.Hour()*60 + t.Minute(), nil
}

// formatCutoffInput renders minutes past midnight back into the "09:00" form an
// <input type="time"> expects.
func formatCutoffInput(minutes int) string {
	if minutes < 0 || minutes > 1439 {
		minutes = defaultCutoffMinutes
	}
	return fmt.Sprintf("%02d:%02d", minutes/60, minutes%60)
}

// defaultCutoffMinutes mirrors the column default in migration 064 (9:00am).
const defaultCutoffMinutes = 9 * 60

// parseWeekdayCheckboxes converts the submitted weekday checkbox values into
// time.Weekday. Values are Go weekday numbers ("0".."6") as rendered by the
// form; anything out of range is dropped rather than rejected, so a hand-crafted
// POST cannot store a weekday the schedule search will never match.
func parseWeekdayCheckboxes(raw []string) []time.Weekday {
	out := make([]time.Weekday, 0, len(raw))
	seen := map[time.Weekday]bool{}
	for _, v := range raw {
		n, err := strconv.Atoi(strings.TrimSpace(v))
		if err != nil || n < 0 || n > 6 {
			continue
		}
		day := time.Weekday(n)
		if seen[day] {
			continue
		}
		seen[day] = true
		out = append(out, day)
	}
	return out
}

// parseZipList splits a free-form user-entered list of zips on any of ",",
// whitespace, or newlines. Entries are normalized to their 5-digit form;
// anything not matching a 5-digit prefix is dropped.
func parseZipList(raw string) []string {
	fields := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == ';' || r == '\n' || r == '\r' || r == '\t' || r == ' '
	})
	out := make([]string, 0, len(fields))
	seen := map[string]bool{}
	for _, f := range fields {
		z := strings.TrimSpace(f)
		if i := strings.Index(z, "-"); i >= 0 {
			z = z[:i]
		}
		if len(z) != 5 {
			continue
		}
		allDigits := true
		for _, c := range z {
			if c < '0' || c > '9' {
				allDigits = false
				break
			}
		}
		if !allDigits || seen[z] {
			continue
		}
		seen[z] = true
		out = append(out, z)
	}
	return out
}

// handleAdminQBConnect initiates the OAuth2 flow to connect QuickBooks.
func (d *Deps) handleAdminQBConnect(w http.ResponseWriter, r *http.Request) {
	if d.QBOAuthManager == nil {
		http.Error(w, "QuickBooks not configured", http.StatusBadRequest)
		return
	}

	authURL, err := d.QBOAuthManager.StartAuth(w)
	if err != nil {
		slog.Error("qb oauth: start auth", "error", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, authURL, http.StatusFound)
}

// handleAdminQBCallback handles the OAuth2 callback from QuickBooks.
// Token exchange happens outside any transaction (external HTTP call); the
// returned credentials are then persisted and audited in a single tx.
func (d *Deps) handleAdminQBCallback(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if d.QBOAuthManager == nil {
		http.Error(w, "QuickBooks not configured", http.StatusBadRequest)
		return
	}

	creds, err := d.QBOAuthManager.ExchangeCallback(ctx, r)
	if err != nil {
		switch {
		case errors.Is(err, quickbooks.ErrInvalidState):
			slog.Error("qb oauth: invalid state parameter")
			http.Redirect(w, r, "/admin/settings?flash=QuickBooks+connection+failed+(invalid+state)", http.StatusSeeOther)
		case errors.Is(err, quickbooks.ErrMissingCallbackParams):
			errorDesc := r.URL.Query().Get("error")
			slog.Error("qb oauth: missing code or realmId", "error", errorDesc)
			http.Redirect(w, r, "/admin/settings?flash=QuickBooks+connection+failed", http.StatusSeeOther)
		default:
			slog.Error("qb oauth: exchange callback", "error", err)
			http.Redirect(w, r, "/admin/settings?flash=QuickBooks+connection+failed+(token+exchange)", http.StatusSeeOther)
		}
		return
	}

	actor := staffActor(r)
	err = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		if err := d.QBOAuthManager.SaveCredentials(ctx, tx, creds); err != nil {
			return err
		}
		return d.AuditWriter.Record(ctx, tx, audit.AuditEntry{
			ActorType:    actor.Type,
			ActorID:      actor.ID,
			ActorName:    actor.Name,
			Action:       audit.AuditQBConnected,
			ResourceType: "qb_credentials",
			ResourceID:   d.QBOAuthManager.TenantID(),
			After:        map[string]any{"realm_id": creds.RealmID},
		})
	})
	if err != nil {
		slog.Error("qb oauth: save credentials", "error", err)
		http.Redirect(w, r, "/admin/settings?flash=QuickBooks+connection+failed+(database+error)", http.StatusSeeOther)
		return
	}

	slog.Info("qb: connected", "realm_id", creds.RealmID)
	http.Redirect(w, r, "/admin/settings?flash=QuickBooks+connected+successfully", http.StatusSeeOther)
}

// handleAdminQBDisconnect removes the QuickBooks connection.
func (d *Deps) handleAdminQBDisconnect(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if d.QBOAuthManager == nil {
		http.Error(w, "QuickBooks not configured", http.StatusBadRequest)
		return
	}

	actor := staffActor(r)

	// Phase 1 (read tx): fetch + decrypt the refresh token so we can revoke it
	// with Intuit before forgetting it locally.
	var refreshToken string
	if err := store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		rt, err := d.QBOAuthManager.RefreshTokenForRevoke(ctx, tx)
		refreshToken = rt
		return err
	}); err != nil {
		// Non-fatal: log and fall through to the local delete so the admin can
		// still disconnect even if reading the token failed.
		slog.Error("qb: read token for revoke", "error", err)
	}

	// Phase 2 (no tx): revoke the grant on Intuit's side. Best-effort — a
	// revoke failure (Intuit down, token already revoked) must not block the
	// local disconnect below.
	if refreshToken != "" {
		if err := d.QBOAuthManager.Revoke(ctx, refreshToken); err != nil {
			slog.Warn("qb: token revoke failed, disconnecting locally anyway", "error", err)
		}
	}

	// Phase 3 (write tx): delete the local credential and audit, atomically.
	err := store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		if err := d.QBOAuthManager.Disconnect(ctx, tx); err != nil {
			return err
		}
		return d.AuditWriter.Record(ctx, tx, audit.AuditEntry{
			ActorType:    actor.Type,
			ActorID:      actor.ID,
			ActorName:    actor.Name,
			Action:       audit.AuditQBDisconnected,
			ResourceType: "qb_credentials",
			ResourceID:   d.QBOAuthManager.TenantID(),
		})
	})
	if err != nil {
		slog.Error("qb: disconnect failed", "error", err)
		http.Redirect(w, r, "/admin/settings?flash=Failed+to+disconnect+QuickBooks", http.StatusSeeOther)
		return
	}

	slog.Info("qb: disconnected")
	http.Redirect(w, r, "/admin/settings?flash=QuickBooks+disconnected", http.StatusSeeOther)
}
