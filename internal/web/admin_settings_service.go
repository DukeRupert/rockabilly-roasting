package web

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/store"
	"github.com/dukerupert/hiri/internal/ui/admin"
)

// Settings → Service: what an hour of the crew's time costs the shop.
//
// Behind requireModule as well as the settings permission — on an instance
// without the equipment service module these settings mean nothing, and the tab
// is not drawn either.

// handleAdminSettingsService renders the tab.
func (d *Deps) handleAdminSettingsService(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	section, err := d.loadSettingsSection(ctx)
	if err != nil {
		Error(w, r, err)
		return
	}

	var rates domain.ServiceLaborRates
	if err := store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		var txErr error
		rates, txErr = d.ServiceTicketService.LaborRates(ctx, tx)
		return txErr
	}); err != nil {
		Error(w, r, err)
		return
	}

	staffName, staffRole := staffNameRole(r)
	d.renderServiceSettings(w, r, admin.SettingsServiceProps{
		Nav:       section.nav(staffRole),
		Values:    serviceRateValuesFrom(rates),
		StaffName: staffName,
		StaffRole: staffRole,
		Flash:     settingsFlash(r),
	})
}

// handleAdminServiceLaborRatesUpdate saves the rates.
//
// A rejected save re-renders in place with what was typed rather than
// redirecting, so a mistyped rate does not cost the other field's value —
// the same shape the shipping form on the first tab uses.
func (d *Deps) handleAdminServiceLaborRatesUpdate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if err := r.ParseForm(); err != nil {
		Error(w, r, err)
		return
	}

	values := admin.ServiceRateValues{
		LaborRate:  strings.TrimSpace(r.FormValue("labor_rate")),
		TravelRate: strings.TrimSpace(r.FormValue("travel_rate")),
	}

	labor, err := parseOptionalRate(values.LaborRate)
	if err != nil {
		d.rejectServiceSettings(w, r, values, "That labour rate is not an amount.")
		return
	}
	travel, err := parseOptionalRate(values.TravelRate)
	if err != nil {
		d.rejectServiceSettings(w, r, values, "That travel rate is not an amount.")
		return
	}

	if err := store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		return d.ServiceTicketService.SetLaborRates(ctx, tx, domain.ServiceLaborRates{
			LaborCentsPerHour:  labor,
			TravelCentsPerHour: travel,
		}, staffActor(r))
	}); err != nil {
		if !expectedFailure(err) {
			Error(w, r, err)
			return
		}
		d.rejectServiceSettings(w, r, values, err.Error())
		return
	}

	if labor == nil {
		redirectFlash(w, r, "/admin/settings/service", "Rates cleared. Cost reports show hours and parts separately.")
		return
	}
	redirectFlash(w, r, "/admin/settings/service", "Rates saved.")
}

// parseOptionalRate reads a dollars-and-cents rate, where blank means unset.
//
// Blank and zero are deliberately different: blank takes the money column off
// the reports entirely, and zero says travel is absorbed. parseDollarsCents
// rejects the empty string — correctly, for the settings fields where a missing
// amount is a mistake — so the blank case is decided here.
func parseOptionalRate(raw string) (*int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	// A leading "$" is what somebody copying a figure off an invoice will
	// paste, and rejecting it teaches nothing.
	raw = strings.TrimPrefix(raw, "$")
	cents, err := parseDollarsCents(raw)
	if err != nil {
		return nil, fmt.Errorf("rate: %w", err)
	}
	return &cents, nil
}

// serviceRateValuesFrom renders stored rates back into form strings, leaving an
// unset rate blank rather than printing "0.00" — which would read as a decision
// nobody made.
func serviceRateValuesFrom(rates domain.ServiceLaborRates) admin.ServiceRateValues {
	return admin.ServiceRateValues{
		LaborRate:  rateInput(rates.LaborCentsPerHour),
		TravelRate: rateInput(rates.TravelCentsPerHour),
	}
}

func rateInput(cents *int) string {
	if cents == nil {
		return ""
	}
	return fmt.Sprintf("%d.%02d", *cents/100, *cents%100)
}

// rejectServiceSettings re-renders the tab with the message and what was typed.
func (d *Deps) rejectServiceSettings(w http.ResponseWriter, r *http.Request, values admin.ServiceRateValues, msg string) {
	ctx := r.Context()

	section, err := d.loadSettingsSection(ctx)
	if err != nil {
		Error(w, r, err)
		return
	}

	staffName, staffRole := staffNameRole(r)
	d.renderServiceSettings(w, r, admin.SettingsServiceProps{
		Nav:       section.nav(staffRole),
		Values:    values,
		Error:     msg,
		StaffName: staffName,
		StaffRole: staffRole,
	})
}

func (d *Deps) renderServiceSettings(w http.ResponseWriter, r *http.Request, props admin.SettingsServiceProps) {
	if IsHTMX(r) {
		admin.SettingsServiceContent(props).Render(r.Context(), w) //nolint:errcheck
		return
	}
	admin.SettingsService(props).Render(r.Context(), w) //nolint:errcheck
}
