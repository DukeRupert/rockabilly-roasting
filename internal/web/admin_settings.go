package web

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
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

// The Settings section: one page per concern (shipping, box presets,
// wholesale, integrations, team), sharing a tab strip and a list of anything
// currently misconfigured. Every page in the section loads the same section
// data so that list is complete wherever the staffer is standing — see
// ui/admin/settings_nav.templ.

// settingsSection is the shared state behind the section nav: the settings
// whose values decide whether anything is broken.
type settingsSection struct {
	Shipping       admin.ShippingSettings
	QB             admin.QBConnectionStatus
	QBEnabled      bool
	BoxPresetCount int
}

// nav derives the tab strip + attention list for a staffer.
func (s settingsSection) nav(role string) admin.SettingsNav {
	return admin.SettingsNav{
		StaffRole: role,
		Issues:    admin.SettingsIssuesFor(s.Shipping, s.QB, s.QBEnabled, s.BoxPresetCount),
	}
}

// loadSettingsSection reads the section-wide state in one transaction. Three
// small reads on a page staff open a handful of times a week — cheap enough to
// pay on every settings page so a broken setting cannot hide behind a tab
// nobody clicked.
func (d *Deps) loadSettingsSection(ctx context.Context) (settingsSection, error) {
	out := settingsSection{QBEnabled: d.QBOAuthManager != nil}

	err := store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		if out.QBEnabled {
			// A QB status read failing must not take the whole settings page
			// down with it — the shipping form below is still editable.
			if status, qbErr := d.QBOAuthManager.Status(ctx, tx); qbErr == nil {
				out.QB.Connected = status.Connected
				out.QB.RealmID = status.RealmID
				out.QB.RefreshExpiresAt = status.RefreshExpiresAt
			}
		}

		cfg, cfgErr := d.CheckoutService.GetShippingConfig(ctx, tx)
		if cfgErr != nil {
			return cfgErr
		}
		out.Shipping = shippingSettingsFromConfig(cfg)

		presets, presetErr := d.FulfillmentService.ListBoxPresets(ctx, tx)
		if presetErr != nil {
			return presetErr
		}
		out.BoxPresetCount = len(presets)
		return nil
	})
	return out, err
}

// shippingSettingsFromConfig maps the stored config onto the form's props.
func shippingSettingsFromConfig(cfg *domain.ShippingConfig) admin.ShippingSettings {
	if cfg == nil {
		return admin.ShippingSettings{}
	}
	return admin.ShippingSettings{
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
}

// settingsFlash reads the one-shot message off the query string. Errors travel
// under their own parameter so the page can paint them as failures — a
// rejected save used to arrive in the same green panel as a successful one.
func settingsFlash(r *http.Request) admin.Flash {
	if msg := r.URL.Query().Get("flash_error"); msg != "" {
		return admin.Flash{Message: msg, Error: true}
	}
	return admin.Flash{Message: r.URL.Query().Get("flash")}
}

// redirectFlash and redirectFlashError send the staffer back to a settings page
// with a message. Values are query-escaped here so callers can write the
// sentence rather than its encoding.
func redirectFlash(w http.ResponseWriter, r *http.Request, path, msg string) {
	http.Redirect(w, r, path+"?flash="+url.QueryEscape(msg), http.StatusSeeOther)
}

func redirectFlashError(w http.ResponseWriter, r *http.Request, path, msg string) {
	http.Redirect(w, r, path+"?flash_error="+url.QueryEscape(msg), http.StatusSeeOther)
}

// handleAdminSettings renders the Shipping tab — the section's landing page.
func (d *Deps) handleAdminSettings(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	section, err := d.loadSettingsSection(ctx)
	if err != nil {
		slog.Error("admin settings: load", "error", err)
		Error(w, r, err)
		return
	}

	name, role := staffNameRole(r)
	d.renderShippingSettings(w, r, admin.SettingsProps{
		Nav:        section.nav(role),
		Shipping:   section.Shipping,
		Flash:      settingsFlash(r),
		MerchantTZ: d.MerchantTZ,
		StaffName:  name,
		StaffRole:  role,
	})
}

// renderShippingSettings renders the shipping page, htmx partial or whole.
//
// A rejected save renders 200, not 422: hx-boost is on for the whole admin, and
// htmx does not swap 4xx responses by default — a correct status code here would
// mean the staffer clicks Save and the page silently does nothing. Same choice
// the Team page makes for its form errors.
func (d *Deps) renderShippingSettings(w http.ResponseWriter, r *http.Request, props admin.SettingsProps) {
	if IsHTMX(r) {
		admin.SettingsContent(props).Render(r.Context(), w) //nolint:errcheck
		return
	}
	admin.Settings(props).Render(r.Context(), w) //nolint:errcheck
}

// handleAdminSettingsWholesale renders the Wholesale tab: the store-wide
// default price list.
func (d *Deps) handleAdminSettingsWholesale(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	section, err := d.loadSettingsSection(ctx)
	if err != nil {
		slog.Error("admin settings: load", "error", err)
		Error(w, r, err)
		return
	}

	var priceLists []domain.PriceList
	var defaultPriceListID *uuid.UUID
	err = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
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
		return nil
	})
	if err != nil {
		slog.Error("admin settings: load wholesale", "error", err)
		Error(w, r, err)
		return
	}

	name, role := staffNameRole(r)
	props := admin.SettingsWholesaleProps{
		Nav:                section.nav(role),
		PriceLists:         priceLists,
		DefaultPriceListID: defaultPriceListID,
		Flash:              settingsFlash(r),
		StaffName:          name,
		StaffRole:          role,
	}
	if IsHTMX(r) {
		admin.SettingsWholesaleContent(props).Render(ctx, w) //nolint:errcheck
		return
	}
	admin.SettingsWholesale(props).Render(ctx, w) //nolint:errcheck
}

// handleAdminSettingsIntegrations renders the Integrations tab.
func (d *Deps) handleAdminSettingsIntegrations(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	section, err := d.loadSettingsSection(ctx)
	if err != nil {
		slog.Error("admin settings: load", "error", err)
		Error(w, r, err)
		return
	}

	name, role := staffNameRole(r)
	props := admin.SettingsIntegrationsProps{
		Nav:        section.nav(role),
		QB:         section.QB,
		QBEnabled:  section.QBEnabled,
		Flash:      settingsFlash(r),
		MerchantTZ: d.MerchantTZ,
		StaffName:  name,
		StaffRole:  role,
	}
	if IsHTMX(r) {
		admin.SettingsIntegrationsContent(props).Render(ctx, w) //nolint:errcheck
		return
	}
	admin.SettingsIntegrations(props).Render(ctx, w) //nolint:errcheck
}

// handleAdminShippingSettingsUpdate persists the edited shipping config and
// records the audit event inside the same transaction.
//
// A rejected save re-renders the form with what was submitted rather than
// redirecting with a flash. The form carries twenty-odd fields and a single
// mistyped number used to discard every other edit on the page along with it.
//
// TODO: when the live-rate provider starts consuming the origin fields,
// tighten state + zip + country validation here.
func (d *Deps) handleAdminShippingSettingsUpdate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	cfg, submitted, fieldErrors := parseShippingForm(r)

	if len(fieldErrors) > 0 {
		section, err := d.loadSettingsSection(ctx)
		if err != nil {
			slog.Error("admin settings: load", "error", err)
			Error(w, r, err)
			return
		}
		name, role := staffNameRole(r)
		// The nav's issue list is derived from what is *saved*, not from the
		// rejected draft — nothing has changed on disk yet.
		d.renderShippingSettings(w, r, admin.SettingsProps{
			Nav:         section.nav(role),
			Shipping:    submitted,
			FieldErrors: fieldErrors,
			Flash:       admin.Flash{Message: "Nothing was saved — check the fields marked below.", Error: true},
			MerchantTZ:  d.MerchantTZ,
			StaffName:   name,
			StaffRole:   role,
		})
		return
	}

	actor := staffActor(r)
	err := store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		return d.CheckoutService.UpdateShippingConfig(ctx, tx, cfg, actor)
	})
	if err != nil {
		slog.Error("admin settings: update shipping", "error", err)
		redirectFlashError(w, r, "/admin/settings", "Failed to save shipping settings")
		return
	}

	redirectFlash(w, r, "/admin/settings", "Shipping settings saved")
}

// parseShippingForm reads the shipping form into a config, the submitted values
// (so a rejected save can be handed straight back), and a per-field error map.
//
// It validates every field before returning rather than bailing on the first
// problem: two mistyped numbers should be two marked fields, not two round
// trips.
func parseShippingForm(r *http.Request) (domain.ShippingConfig, admin.ShippingSettings, map[string]string) {
	fieldErrors := map[string]string{}

	rawFlat := strings.TrimSpace(r.FormValue("flat_rate"))
	flatRateCents, err := parseDollarsCents(rawFlat)
	if err != nil {
		fieldErrors["flat_rate"] = "Enter a dollar amount, e.g. 6.00."
	}

	rawThreshold := strings.TrimSpace(r.FormValue("free_threshold"))
	var threshold *int
	if rawThreshold != "" {
		cents, tErr := parseDollarsCents(rawThreshold)
		if tErr != nil {
			fieldErrors["free_threshold"] = "Enter a dollar amount, or leave blank for no threshold."
		} else {
			threshold = &cents
		}
	}

	rawTare := strings.TrimSpace(r.FormValue("tare_weight_oz"))
	tareOz := 0.0
	if rawTare != "" {
		oz, tErr := strconv.ParseFloat(rawTare, 64)
		if tErr != nil || oz < 0 {
			fieldErrors["tare_weight_oz"] = "Enter a weight in ounces, e.g. 2.50."
		} else {
			tareOz = oz
		}
	}

	cutoffMinutes, err := parseCutoffInput(r.FormValue("local_delivery_cutoff"))
	if err != nil {
		fieldErrors["local_delivery_cutoff"] = "Enter a time of day, e.g. 09:00."
	}

	deliveryEnabled := r.FormValue("local_delivery_enabled") != ""
	weekdays := parseWeekdayCheckboxes(r.Form["local_delivery_weekdays"])
	// A delivery schedule with no days is unschedulable: checkout and the
	// confirmation email would silently drop back to vague phrasing with no
	// hint as to why. Refuse it rather than let the route quietly go dark.
	if deliveryEnabled && len(weekdays) == 0 {
		fieldErrors["local_delivery_weekdays"] = "Pick at least one day the van runs, or turn local delivery off."
	}

	originCountry := strings.ToUpper(strings.TrimSpace(r.FormValue("origin_country")))
	if originCountry == "" {
		originCountry = "US"
	}
	originEmail := strings.TrimSpace(r.FormValue("origin_email"))
	originPhone := strings.TrimSpace(r.FormValue("origin_phone"))

	cfg := domain.ShippingConfig{
		FlatRateCents:              flatRateCents,
		FreeShippingThreshold:      threshold,
		Currency:                   "usd",
		LocalZipCodes:              parseZipList(r.FormValue("local_zip_codes")),
		LocalDeliveryEnabled:       deliveryEnabled,
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
		OriginEmail:                originEmail,
		OriginPhone:                originPhone,
		TareWeightOz:               tareOz,
	}

	// The submitted view keeps the raw strings for the numeric fields, so a
	// rejected save shows the staffer what they typed rather than a silently
	// coerced version of it.
	submitted := shippingSettingsFromConfig(&cfg)
	submitted.LocalDeliveryCutoff = strings.TrimSpace(r.FormValue("local_delivery_cutoff"))
	submitted.FlatRateInput = rawFlat
	submitted.ThresholdInput = rawThreshold
	submitted.TareInput = rawTare

	return cfg, submitted, fieldErrors
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
			redirectFlashError(w, r, "/admin/settings/wholesale", "That price list no longer exists")
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
		redirectFlashError(w, r, "/admin/settings/wholesale", "Failed to save default price list")
		return
	}

	redirectFlash(w, r, "/admin/settings/wholesale", "Default wholesale price list saved")
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
			redirectFlashError(w, r, "/admin/settings/integrations", "QuickBooks connection failed (invalid state)")
		case errors.Is(err, quickbooks.ErrMissingCallbackParams):
			errorDesc := r.URL.Query().Get("error")
			slog.Error("qb oauth: missing code or realmId", "error", errorDesc)
			redirectFlashError(w, r, "/admin/settings/integrations", "QuickBooks connection failed")
		default:
			slog.Error("qb oauth: exchange callback", "error", err)
			redirectFlashError(w, r, "/admin/settings/integrations", "QuickBooks connection failed (token exchange)")
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
		redirectFlashError(w, r, "/admin/settings/integrations", "QuickBooks connection failed (database error)")
		return
	}

	slog.Info("qb: connected", "realm_id", creds.RealmID)
	redirectFlash(w, r, "/admin/settings/integrations", "QuickBooks connected")
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
		redirectFlashError(w, r, "/admin/settings/integrations", "Failed to disconnect QuickBooks")
		return
	}

	slog.Info("qb: disconnected")
	redirectFlash(w, r, "/admin/settings/integrations", "QuickBooks disconnected")
}
