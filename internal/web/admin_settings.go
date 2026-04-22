package web

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/jackc/pgx/v5"

	"github.com/dukerupert/hiri/internal/platform/audit"
	"github.com/dukerupert/hiri/internal/platform/quickbooks"
	"github.com/dukerupert/hiri/internal/store"
	"github.com/dukerupert/hiri/internal/ui/admin"
)

// handleAdminSettings renders the Settings page with integration status.
func (d *Deps) handleAdminSettings(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	qbStatus := admin.QBConnectionStatus{}
	qbEnabled := d.QBOAuthManager != nil

	if qbEnabled {
		_ = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
			status, err := d.QBOAuthManager.Status(ctx, tx)
			if err != nil {
				return nil // treat errors as "not connected"
			}
			qbStatus.Connected = status.Connected
			qbStatus.RealmID = status.RealmID
			qbStatus.RefreshExpiresAt = status.RefreshExpiresAt
			return nil
		})
	}

	name, role := staffNameRole(r)
	props := admin.SettingsProps{
		QB:        qbStatus,
		QBEnabled: qbEnabled,
		Flash:     r.URL.Query().Get("flash"),
		StaffName: name,
		StaffRole: role,
	}

	if IsHTMX(r) {
		admin.SettingsContent(props).Render(ctx, w) //nolint:errcheck
		return
	}
	admin.Settings(props).Render(ctx, w) //nolint:errcheck
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
