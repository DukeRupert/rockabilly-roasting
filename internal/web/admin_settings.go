package web

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

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

	err := store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		if qbEnabled {
			if status, qbErr := d.QBOAuthManager.Status(ctx, tx); qbErr == nil {
				qbStatus.Connected = status.Connected
				qbStatus.RealmID = status.RealmID
				qbStatus.RefreshExpiresAt = status.RefreshExpiresAt
			}
		}

		cfg, cfgErr := d.CheckoutService.GetShippingConfig(ctx, tx)
		if cfgErr != nil {
			return cfgErr
		}
		shipping = admin.ShippingSettings{
			FlatRateCents:         cfg.FlatRateCents,
			FreeShippingThreshold: cfg.FreeShippingThreshold,
			Currency:              cfg.Currency,
			LocalZipCodes:         cfg.LocalZipCodes,
			OriginName:            cfg.OriginName,
			OriginStreet1:         cfg.OriginStreet1,
			OriginStreet2:         cfg.OriginStreet2,
			OriginCity:            cfg.OriginCity,
			OriginState:           cfg.OriginState,
			OriginZip:             cfg.OriginZip,
			OriginCountry:         cfg.OriginCountry,
			TareWeightOz:          cfg.TareWeightOz,
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
		QB:         qbStatus,
		QBEnabled:  qbEnabled,
		Shipping:   shipping,
		Flash:      r.URL.Query().Get("flash"),
		MerchantTZ: d.MerchantTZ,
		StaffName:  name,
		StaffRole:  role,
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
// TODO: origin fields are informational today (Pirate Ship has its own origin
// config). When a live-rate provider starts consuming them, tighten state +
// zip + country validation here.
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

	cfg := domain.ShippingConfig{
		FlatRateCents:         flatRateCents,
		FreeShippingThreshold: threshold,
		Currency:              "usd",
		LocalZipCodes:         zips,
		OriginName:            strings.TrimSpace(r.FormValue("origin_name")),
		OriginStreet1:         strings.TrimSpace(r.FormValue("origin_street1")),
		OriginStreet2:         strings.TrimSpace(r.FormValue("origin_street2")),
		OriginCity:            strings.TrimSpace(r.FormValue("origin_city")),
		OriginState:           strings.ToUpper(strings.TrimSpace(r.FormValue("origin_state"))),
		OriginZip:             strings.TrimSpace(r.FormValue("origin_zip")),
		OriginCountry:         originCountry,
		TareWeightOz:          tareOz,
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
