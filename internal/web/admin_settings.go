package web

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	"github.com/dukerupert/hiri/internal/app"
	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/jobs"
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
	Shipping admin.ShippingSettings
	// Postponements are the delivery runs moved off their scheduled day, read
	// alongside the shipping config because they are edited on the same page
	// and are meaningless without the schedule they amend.
	Postponements []domain.DeliveryPostponement
	QB            admin.QBConnectionStatus
	QBEnabled     bool
	// QBAppUnreadable marks a saved Intuit app configuration that would not
	// decrypt. Carried separately from QBEnabled because it is a different
	// answer to a different question: not "no app is configured" but "one is,
	// and this server cannot read it".
	QBAppUnreadable bool
	// BoxPresets is the full list, not a count: the attention list needs to
	// know whether any exist and the box-presets page needs to draw them, and
	// reading it twice would let the "No box presets" warning render above a
	// table that has one.
	BoxPresets []domain.BoxPreset
	// ServiceEnabled draws the Service tab. Read here rather than per-page so
	// every tab in the section agrees about whether the strip has six entries
	// or five.
	ServiceEnabled bool
}

// nav derives the tab strip + attention list for a staffer.
func (s settingsSection) nav(role string) admin.SettingsNav {
	return admin.SettingsNav{
		StaffRole:      role,
		Issues:         admin.SettingsIssuesFor(s.Shipping, s.QB, s.QBEnabled, len(s.BoxPresets)),
		ServiceEnabled: s.ServiceEnabled,
	}
}

// loadSettingsSection reads the section-wide state: a few small reads on pages
// staff open a handful of times a week, cheap enough to pay on every one of
// them so a broken setting cannot hide behind a tab nobody clicked.
//
// Two transactions, not one, and deliberately — the QuickBooks status is read
// on its own so a failure there stays a failure there. See the comment at that
// read.
func (d *Deps) loadSettingsSection(ctx context.Context) (settingsSection, error) {
	out := settingsSection{
		ServiceEnabled: d.ModuleService.Enabled(domain.ModuleEquipmentService),
	}

	// The QuickBooks status gets its own transaction, deliberately. A failed
	// status read must not take the settings page down — the shipping form
	// below is still editable, and "not connected" is a different fact from
	// "could not tell", which is what sends staff to reconnect a connection
	// that was fine. Sharing the transaction below would make that graceful
	// path unreachable: a real database error aborts the pgx transaction, so
	// every read after it fails too and the page 500s regardless.
	qbErr := store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		configured, err := d.QB.ConfiguredTx(ctx, tx)
		switch {
		case errors.Is(err, quickbooks.ErrAppConfigUnreadable):
			// Not a section-level failure. Everything else on this page is
			// readable, and the integrations card has a panel that explains
			// this precise case — surfacing it as "couldn't read the status"
			// would send staff to reconnect a connection that is fine.
			out.QBAppUnreadable = true
		case err != nil:
			return err
		default:
			out.QBEnabled = configured
		}

		// Read even when no app is configured. The connection and the app it
		// was made through are stored separately, and a shop whose
		// configuration was cleared out from under a live connection needs the
		// page to say so rather than to render as a fresh install.
		status, err := d.QB.Status(ctx, tx)
		if err != nil {
			return err
		}
		out.QB.Connected = status.Connected
		out.QB.RealmID = status.RealmID
		out.QB.RefreshExpiresAt = status.RefreshExpiresAt
		return nil
	})
	if qbErr != nil {
		slog.Error("admin settings: quickbooks status", "error", qbErr)
		out.QB = admin.QBConnectionStatus{Unavailable: true}
	}

	err := store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		cfg, cfgErr := d.CheckoutService.GetShippingConfig(ctx, tx)
		if cfgErr != nil {
			return cfgErr
		}
		out.Shipping = shippingSettingsFromConfig(cfg)
		out.Postponements = cfg.DeliveryPostponements

		presets, presetErr := d.FulfillmentService.ListBoxPresets(ctx, tx)
		if presetErr != nil {
			return presetErr
		}
		out.BoxPresets = presets
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

// redirectFlash and redirectFlashError send the staffer back to a page with a
// message. Values are query-escaped here so callers can write the sentence
// rather than its encoding.
//
// The separator is chosen, not assumed. These originally appended "?flash=" to
// whatever they were given, which is right for a bare path and silently wrong
// for one that already carries a query: the maintenance due list redirects back
// to "…/maintenance?scope=overdue", and the result was
// "?scope=overdue?flash=…" — one parameter whose value is the rest of the URL.
// The scope came back as garbage and fell to the default, bouncing the staffer
// off the tab they were working, and the message was never read at all, so a
// rejected save looked exactly like a successful one.
func redirectFlash(w http.ResponseWriter, r *http.Request, path, msg string) {
	http.Redirect(w, r, withQuery(path, "flash", msg), http.StatusSeeOther)
}

func redirectFlashError(w http.ResponseWriter, r *http.Request, path, msg string) {
	http.Redirect(w, r, withQuery(path, "flash_error", msg), http.StatusSeeOther)
}

// withQuery appends one query parameter to a path that may already have some.
func withQuery(path, key, value string) string {
	sep := "?"
	if strings.Contains(path, "?") {
		sep = "&"
	}
	return path + sep + key + "=" + url.QueryEscape(value)
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
	d.renderShippingSettings(w, r, section, admin.SettingsProps{
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
//
// Rendering in place rather than redirecting is what makes the form's POST
// target load-bearing. hx-boost pushes the action URL into history, so whatever
// the form posts to is what the address bar reads afterwards and what a refresh
// or a back-then-forward will GET. The form therefore posts to /admin/settings —
// the tab's own URL, which answers GET — and not to a POST-only verb path, which
// would answer that refresh with a 405 on the very page the staffer was just
// told to go fix. Team and Box presets are safe for the same reason: both post
// to a URL that has a GET route.
func (d *Deps) renderShippingSettings(w http.ResponseWriter, r *http.Request, section settingsSection, props admin.SettingsProps) {
	// Stamped here rather than at the call sites so the echo sentences cannot
	// be handed a draft by a caller that forgot: props.Shipping is what the
	// fields render, props.Saved is what is on disk, and only this function
	// decides the second one.
	props.Saved = section.Shipping
	// Same reasoning for the moved runs. They are never a draft — the
	// postponement forms are their own POSTs, so a rejected shipping save has
	// not touched them — and stamping them here means the rejected-save path
	// cannot render the panel empty by forgetting to carry them over.
	props.Postponements = postponementRows(section.Postponements, d.MerchantTZ, time.Now())
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
	props, err := d.integrationsProps(r)
	if err != nil {
		slog.Error("admin settings: load", "error", err)
		Error(w, r, err)
		return
	}
	props.Flash = settingsFlash(r)
	d.renderIntegrations(w, r, props)
}

// renderIntegrations renders the Integrations tab, htmx partial or whole.
//
// A rejected save renders 200, not 422, for the same reason the shipping form
// does: hx-boost is on across the admin and htmx does not swap 4xx responses,
// so a correct status code would mean the staffer clicks Save and the page
// silently does nothing. See renderShippingSettings for the rest of that
// argument, including why the form posts to this tab's own URL.
func (d *Deps) renderIntegrations(w http.ResponseWriter, r *http.Request, props admin.SettingsIntegrationsProps) {
	ctx := r.Context()
	if IsHTMX(r) {
		admin.SettingsIntegrationsContent(props).Render(ctx, w) //nolint:errcheck
		return
	}
	admin.SettingsIntegrations(props).Render(ctx, w) //nolint:errcheck
}

// integrationsProps assembles everything the Integrations tab renders from
// what is currently stored.
//
// Separate from the handler so a rejected save can re-render the page as it
// actually is and then overlay its draft, rather than assembling a second,
// almost-identical set of props that would drift out of step with this one.
func (d *Deps) integrationsProps(r *http.Request) (admin.SettingsIntegrationsProps, error) {
	ctx := r.Context()

	section, err := d.loadSettingsSection(ctx)
	if err != nil {
		return admin.SettingsIntegrationsProps{}, err
	}

	// Billing mode and the review count are read here rather than in
	// loadSettingsSection: they are only meaningful on this tab, and the
	// section loader is shared by every settings page.
	var mode domain.QBBillingMode
	var previewCount int
	if section.QBEnabled {
		if err := store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
			var txErr error
			if mode, txErr = d.OrderService.QBBillingMode(ctx, tx); txErr != nil {
				return txErr
			}
			previewCount, txErr = d.OrderService.CountQBPreviews(ctx, tx)
			return txErr
		}); err != nil {
			// A failed read here must not take the page down — the connection
			// panel above it is the reason staff came. Fall back to shadow,
			// which is the safe thing to claim when we cannot tell.
			slog.Error("admin settings: qb billing mode", "error", err)
			mode = domain.DefaultQBBillingMode
		}
	}

	// Which items invoices bill against, and what they could be. A live
	// read-only call, safe in either billing mode.
	// Two phases, deliberately. Listing a company's items is an Intuit round
	// trip, and holding a pooled connection open across it would also nest a
	// second acquisition inside the first — the token read opens its own
	// transaction — which is how a busy shop runs the pool dry.
	var itemPanel admin.QBItemPanel
	if section.QB.Connected {
		itemPanel.EnvFallback = os.Getenv("QB_SALES_ITEM_ID")

		var cfg store.QBItemConfig
		if err := store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
			var txErr error
			cfg, txErr = d.OrderService.QBItemConfigFor(ctx, tx)
			return txErr
		}); err != nil {
			slog.Error("admin settings: qb item config", "error", err)
			itemPanel.ConfigUnreadable = true
		}
		itemPanel.SalesItemID = cfg.SalesItemID
		itemPanel.SalesItemName = cfg.SalesItemName
		itemPanel.ShippingItemID = cfg.ShippingItemID
		itemPanel.ShippingItemName = cfg.ShippingItemName

		items, listErr := d.QB.ListItems(ctx)
		if listErr != nil {
			// Cannot offer a choice, which is not the same as the company
			// having nothing to choose from.
			itemPanel.Unavailable = true
			// Not configured is the ordinary state of a shop that has not
			// connected yet, and logging it as an error every time the
			// settings page loads would bury the failures that matter.
			if !errors.Is(listErr, quickbooks.ErrNotConfigured) {
				slog.Error("admin settings: qb list items", "error", listErr)
			}
		}
		for _, item := range items {
			itemPanel.Choices = append(itemPanel.Choices, admin.QBItemChoice{
				ID: item.ID, Name: item.Name, IncomeAccount: item.IncomeAccount,
			})
		}
	}

	// Which Intuit app is in force, and the form for changing it.
	appPanel := admin.QBAppPanel{
		RedirectURI: d.QB.RedirectURI(),
		Connected:   section.QB.Connected,
		Unreadable:  section.QBAppUnreadable,
	}
	if !appPanel.Unreadable {
		cfg, configured, cfgErr := d.QB.AppConfig(ctx)
		if cfgErr != nil {
			// loadSettingsSection asked the same question a moment ago and got
			// an answer, so this is a database that has just gone away. The
			// card renders as unconfigured; the shipping form below is still
			// the reason most staff opened the page.
			slog.Error("admin settings: qb app config", "error", cfgErr)
		} else {
			appPanel.Configured = configured
			appPanel.FromEnvironment = configured && !cfg.FromDatabase
			appPanel.HasStoredSecrets = cfg.FromDatabase
			appPanel.ClientID = cfg.ClientID
			appPanel.Environment = cfg.Environment
		}
	}

	name, role := staffNameRole(r)
	return admin.SettingsIntegrationsProps{
		Nav:            section.nav(role),
		QB:             section.QB,
		QBApp:          appPanel,
		QBEnabled:      section.QBEnabled,
		QBBillingMode:  mode,
		QBPreviewCount: previewCount,
		QBItems:        itemPanel,
		MerchantTZ:     d.MerchantTZ,
		StaffName:      name,
		StaffRole:      role,
	}, nil
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
		// The nav's issue list and the echo sentences are derived from what is
		// *saved*, not from the rejected draft — nothing has changed on disk
		// yet, so what a customer meets is still the stored rule.
		d.renderShippingSettings(w, r, section, admin.SettingsProps{
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
	// coerced version of it. Draft is what tells the form to render those raw
	// strings verbatim — including the empty ones, so a field that was cleared
	// comes back cleared instead of reverting to the stored number.
	submitted := shippingSettingsFromConfig(&cfg)
	submitted.Draft = true
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
	// ParseFloat accepts "Inf", "+Inf" and "NaN" as valid float64s. Neither is
	// caught by the negative check below — NaN compares false against
	// everything, and +Inf is cheerfully positive — and converting either to an
	// int is undefined behaviour that lands on a large negative number in
	// practice. A settings field that turns "NaN" into a negative rate is worse
	// than one that refuses it.
	if math.IsNaN(dollars) || math.IsInf(dollars, 0) {
		return 0, fmt.Errorf("not an amount")
	}
	if dollars < 0 {
		return 0, fmt.Errorf("negative amount")
	}
	// Bounded so the conversion cannot overflow. The ceiling is far above any
	// figure a shop setting holds — the point is that it exists, not where it
	// sits.
	if cents := dollars*100 + 0.5; cents > math.MaxInt32 {
		return 0, fmt.Errorf("amount too large")
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

// handleAdminQBAppConfigUpdate saves which Intuit app the shop connects
// QuickBooks through.
//
// The two secrets arrive here and are never sent back: a blank field means
// "keep what is stored", which is what lets a staffer correct a client ID
// without retyping credentials they may not have in front of them.
func (d *Deps) handleAdminQBAppConfigUpdate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	actor := staffActor(r)

	in := quickbooks.AppConfigInput{
		ClientID:        r.FormValue("qb_client_id"),
		ClientSecret:    r.FormValue("qb_client_secret"),
		WebhookVerifier: r.FormValue("qb_webhook_verifier"),
		Environment:     r.FormValue("qb_environment"),
	}

	err := store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		if err := d.QB.SaveAppConfig(ctx, tx, in); err != nil {
			return err
		}
		return d.AuditWriter.Record(ctx, tx, audit.AuditEntry{
			ActorType:    actor.Type,
			ActorID:      actor.ID,
			ActorName:    actor.Name,
			Action:       audit.AuditQBAppConfigUpdated,
			ResourceType: "qb_app_config",
			ResourceID:   d.QB.TenantID(),
			// The client ID and the environment only. Neither secret may ever
			// reach the audit log — it is readable by every staffer who can
			// open the audit page, and by anyone who reads a database backup.
			After: map[string]any{
				"client_id":   strings.TrimSpace(in.ClientID),
				"environment": strings.TrimSpace(in.Environment),
			},
		})
	})
	var invalid *quickbooks.AppConfigError
	switch {
	case errors.As(err, &invalid):
		// Re-rendered in place, not redirected with a flash. The secrets
		// cannot be echoed back, so a rejected save that also dropped the
		// client ID would cost the staffer every field on the form for one
		// mistake — the same trap the shipping form was fixed for.
		d.renderRejectedQBAppConfig(w, r, in, invalid.Fields)
	case errors.Is(err, quickbooks.ErrConnected):
		redirectFlashError(w, r, "/admin/settings/integrations", "Disconnect QuickBooks before changing the app credentials")
	case err != nil:
		slog.Error("qb: save app config", "error", err)
		redirectFlashError(w, r, "/admin/settings/integrations", "Couldn't save the QuickBooks app credentials")
	default:
		redirectFlash(w, r, "/admin/settings/integrations", "QuickBooks app credentials saved")
	}
}

// renderRejectedQBAppConfig re-renders the Integrations tab with the submitted
// values still in the form and each bad field marked.
//
// The page is assembled from what is stored and only then overlaid with the
// draft, so everything else on the tab — the connection status, the invoice
// items, the billing mode, and the summary above the form — keeps describing
// what is actually saved. Nothing reached the database.
func (d *Deps) renderRejectedQBAppConfig(w http.ResponseWriter, r *http.Request, in quickbooks.AppConfigInput, fieldErrors map[string]string) {
	props, err := d.integrationsProps(r)
	if err != nil {
		slog.Error("admin settings: load", "error", err)
		Error(w, r, err)
		return
	}
	props.QBApp.Draft = &admin.QBAppDraft{
		ClientID:    strings.TrimSpace(in.ClientID),
		Environment: strings.TrimSpace(in.Environment),
	}
	props.FieldErrors = fieldErrors
	props.Flash = admin.Flash{Message: "Nothing was saved — check the fields marked below.", Error: true}
	d.renderIntegrations(w, r, props)
}

// handleAdminQBAppConfigClear forgets the saved app configuration, returning
// the deployment to whatever the environment supplies — usually nothing.
func (d *Deps) handleAdminQBAppConfigClear(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	actor := staffActor(r)

	err := store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		if err := d.QB.ClearAppConfig(ctx, tx); err != nil {
			return err
		}
		return d.AuditWriter.Record(ctx, tx, audit.AuditEntry{
			ActorType:    actor.Type,
			ActorID:      actor.ID,
			ActorName:    actor.Name,
			Action:       audit.AuditQBAppConfigCleared,
			ResourceType: "qb_app_config",
			ResourceID:   d.QB.TenantID(),
		})
	})
	switch {
	case errors.Is(err, quickbooks.ErrConnected):
		redirectFlashError(w, r, "/admin/settings/integrations", "Disconnect QuickBooks before removing the app credentials")
	case err != nil:
		slog.Error("qb: clear app config", "error", err)
		redirectFlashError(w, r, "/admin/settings/integrations", "Couldn't remove the QuickBooks app credentials")
	default:
		redirectFlash(w, r, "/admin/settings/integrations", "QuickBooks app credentials removed")
	}
}

// qbOAuth resolves the OAuth manager for the app currently configured,
// writing the response itself when there is none to resolve. Every OAuth
// handler starts here: the configuration is a database row now, so "is
// QuickBooks configured" is a question each request asks rather than one main
// answered at boot with a nil.
func (d *Deps) qbOAuth(w http.ResponseWriter, r *http.Request) (*quickbooks.OAuthManager, bool) {
	oauth, err := d.QB.OAuth(r.Context())
	switch {
	case errors.Is(err, quickbooks.ErrNotConfigured), errors.Is(err, quickbooks.ErrInvalidAppConfig):
		http.Error(w, "QuickBooks not configured", http.StatusBadRequest)
		return nil, false
	case err != nil:
		slog.Error("qb oauth: resolve manager", "error", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return nil, false
	}
	return oauth, true
}

// handleAdminQBConnect initiates the OAuth2 flow to connect QuickBooks.
func (d *Deps) handleAdminQBConnect(w http.ResponseWriter, r *http.Request) {
	oauth, ok := d.qbOAuth(w, r)
	if !ok {
		return
	}

	authURL, err := oauth.StartAuth(w)
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

	oauth, ok := d.qbOAuth(w, r)
	if !ok {
		return
	}

	creds, err := oauth.ExchangeCallback(ctx, r)
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
		if err := oauth.SaveCredentials(ctx, tx, creds); err != nil {
			return err
		}
		return d.AuditWriter.Record(ctx, tx, audit.AuditEntry{
			ActorType:    actor.Type,
			ActorID:      actor.ID,
			ActorName:    actor.Name,
			Action:       audit.AuditQBConnected,
			ResourceType: "qb_credentials",
			ResourceID:   d.QB.TenantID(),
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

	oauth, ok := d.qbOAuth(w, r)
	if !ok {
		return
	}

	actor := staffActor(r)

	// Phase 1 (read tx): fetch + decrypt the refresh token so we can revoke it
	// with Intuit before forgetting it locally.
	var refreshToken string
	if err := store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		rt, err := oauth.RefreshTokenForRevoke(ctx, tx)
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
		if err := oauth.Revoke(ctx, refreshToken); err != nil {
			slog.Warn("qb: token revoke failed, disconnecting locally anyway", "error", err)
		}
	}

	// Phase 3 (write tx): delete the local credential and audit, atomically.
	err := store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		if err := oauth.Disconnect(ctx, tx); err != nil {
			return err
		}
		return d.AuditWriter.Record(ctx, tx, audit.AuditEntry{
			ActorType:    actor.Type,
			ActorID:      actor.ID,
			ActorName:    actor.Name,
			Action:       audit.AuditQBDisconnected,
			ResourceType: "qb_credentials",
			ResourceID:   d.QB.TenantID(),
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

// qbPreviewPageSize bounds the review list. A proof period is meant to run for
// a week or two, so this is generous enough to hold one without paging while
// still refusing to render an unbounded table.
const qbPreviewPageSize = 200

// qbPreviewPath is where every action on the review list returns to.
const qbPreviewPath = "/admin/settings/integrations/quickbooks/preview"

// handleAdminQBItemsUpdate records which QuickBooks items invoices bill
// against.
func (d *Deps) handleAdminQBItemsUpdate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	salesID := r.FormValue("qb_sales_item_id")

	// The shipping select says "same as sales" explicitly. An empty value
	// means its option was dropped — which is what a browser does with the
	// disabled option a deactivated item renders as — and treating that as
	// "same as sales" would silently rebind shipping revenue while reporting
	// success.
	shippingRaw := r.FormValue("qb_shipping_item_id")
	if shippingRaw == "" {
		redirectFlashError(w, r, "/admin/settings/integrations",
			"The shipping item you had is no longer active in QuickBooks. Choose one, or pick \"Same as product lines\".")
		return
	}
	shippingID := shippingRaw
	if shippingRaw == admin.QBShippingSameAsSales {
		shippingID = ""
	}

	// Resolve against the company before opening a transaction: the IDs have
	// to exist in QuickBooks or every invoice fails, and this is the last
	// cheap place to say so. Outside any tx, per the external-call rule.
	cfg, err := d.resolveQBItems(ctx, salesID, shippingID)
	if err == nil {
		err = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
			return d.OrderService.SetQBItems(ctx, tx, cfg, staffActor(r))
		})
	}
	switch {
	case errors.Is(err, app.ErrQBSalesItemRequired):
		redirectFlashError(w, r, "/admin/settings/integrations", "Choose the item invoices should bill against.")
	case errors.Is(err, app.ErrQBItemNotFound):
		redirectFlashError(w, r, "/admin/settings/integrations", "That item no longer exists in QuickBooks. Reload and choose again.")
	case errors.Is(err, app.ErrQBNotConnected):
		redirectFlashError(w, r, "/admin/settings/integrations", "QuickBooks is not connected, so there are no items to choose from.")
	case errors.Is(err, app.ErrQBNoActiveItems):
		redirectFlashError(w, r, "/admin/settings/integrations", "This QuickBooks company has no active products or services. Add one in QuickBooks, then reload.")
	case err != nil:
		slog.Error("admin qb items: update", "error", err)
		Error(w, r, err)
	default:
		redirectFlash(w, r, "/admin/settings/integrations", "Invoice items saved.")
	}
}

// resolveQBItems turns the submitted item IDs into a stored mapping, checking
// each against the connected company and picking up its name for display.
//
// Lives here rather than in the service because it is an external call, and
// app may not reach into platform/quickbooks.
func (d *Deps) resolveQBItems(ctx context.Context, salesID, shippingID string) (store.QBItemConfig, error) {
	if salesID == "" {
		return store.QBItemConfig{}, app.ErrQBSalesItemRequired
	}
	items, err := d.QB.ListItems(ctx)
	if errors.Is(err, quickbooks.ErrNotConfigured) {
		// A different fact from "that item is gone", and worth saying so: the
		// form should not be reachable at all without a configured app.
		return store.QBItemConfig{}, app.ErrQBNotConnected
	}
	if err != nil {
		return store.QBItemConfig{}, fmt.Errorf("qb list items: %w", err)
	}
	if len(items) == 0 {
		// A connected company with nothing to sell. Saying "not connected"
		// here would be the same guess-stated-as-fact this code keeps being
		// corrected for, just pointing the other way.
		return store.QBItemConfig{}, app.ErrQBNoActiveItems
	}
	byID := make(map[string]quickbooks.Item, len(items))
	for _, item := range items {
		byID[item.ID] = item
	}

	sales, ok := byID[salesID]
	if !ok {
		return store.QBItemConfig{}, fmt.Errorf("sales item %q: %w", salesID, app.ErrQBItemNotFound)
	}
	cfg := store.QBItemConfig{SalesItemID: sales.ID, SalesItemName: sales.Name}
	if shippingID != "" {
		shipping, found := byID[shippingID]
		if !found {
			return store.QBItemConfig{}, fmt.Errorf("shipping item %q: %w", shippingID, app.ErrQBItemNotFound)
		}
		cfg.ShippingItemID = shipping.ID
		cfg.ShippingItemName = shipping.Name
	}
	return cfg, nil
}

// handleAdminQBPreview renders the orders invoicing did not bill: all of them
// while the shop is in test mode, and afterwards the accounts on manual
// billing.
func (d *Deps) handleAdminQBPreview(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	section, err := d.loadSettingsSection(ctx)
	if err != nil {
		slog.Error("admin qb preview: load settings", "error", err)
		Error(w, r, err)
		return
	}

	var summary app.QBShadowSummary
	var mode domain.QBBillingMode
	if err := store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		var txErr error
		if mode, txErr = d.OrderService.QBBillingMode(ctx, tx); txErr != nil {
			return txErr
		}
		summary, txErr = d.OrderService.ListQBPreviews(ctx, tx, qbPreviewPageSize)
		return txErr
	}); err != nil {
		slog.Error("admin qb preview: list", "error", err)
		Error(w, r, err)
		return
	}

	rows := make([]admin.QBPreviewRow, 0, len(summary.Rows))
	for _, row := range summary.Rows {
		rows = append(rows, admin.QBPreviewRow{
			QBInvoicePreview: row.QBInvoicePreview,
			OrderNumber:      row.OrderNumber,
			CustomerName:     row.CustomerName,
		})
	}

	name, role := staffNameRole(r)
	props := admin.QBPreviewProps{
		Nav:            section.nav(role),
		Rows:           rows,
		Mode:           mode,
		Attention:      summary.Attention,
		TotalCents:     summary.TotalCents,
		Count:          summary.Count,
		Truncated:      summary.Truncated,
		AwaitingManual: summary.AwaitingManual,
		Flash:          settingsFlash(r),
		StaffName:      name,
		StaffRole:      role,
	}
	if IsHTMX(r) {
		admin.QBPreviewContent(props).Render(ctx, w) //nolint:errcheck
		return
	}
	admin.QBPreview(props).Render(ctx, w) //nolint:errcheck
}

// handleAdminQBBillingModeUpdate switches QuickBooks billing between shadow
// and live.
//
// The mode arrives as an explicit value rather than as a toggle of whatever is
// stored: two staff on the page at once must not be able to flip each other
// into billing customers by both pressing the button they were looking at.
func (d *Deps) handleAdminQBBillingModeUpdate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	mode := domain.QBBillingMode(r.FormValue("mode"))
	if !mode.Valid() {
		redirectFlashError(w, r, "/admin/settings/integrations", "That is not a billing mode.")
		return
	}

	if err := store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		return d.OrderService.SetQBBillingMode(ctx, tx, mode, staffActor(r))
	}); err != nil {
		slog.Error("admin qb billing mode: update", "error", err, "mode", mode)
		Error(w, r, err)
		return
	}

	msg := "QuickBooks billing is in test mode. Nothing will be billed."
	if mode.IsLive() {
		msg = "QuickBooks billing is live. New wholesale orders will be invoiced and emailed."
	}
	redirectFlash(w, r, "/admin/settings/integrations", msg)
}

// handleAdminQBBillOrder starts the QuickBooks invoice chain for an order that
// was recorded in test mode rather than billed.
//
// Going live does not bill retrospectively — an order placed during a proof
// period is not silently invoiced weeks later just because someone flipped a
// switch. This is the deliberate alternative: staff choose which of those
// orders to bill, one at a time, having read what would go out.
func (d *Deps) handleAdminQBBillOrder(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	orderID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		redirectFlashError(w, r, qbPreviewPath, "That is not an order.")
		return
	}

	err = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		return d.OrderService.BillOrderNow(ctx, tx, orderID, d.enqueueQBChain, staffActor(r))
	})
	switch {
	case errors.Is(err, app.ErrQBBillingNotLive):
		redirectFlashError(w, r, qbPreviewPath, "Billing is still in test mode. Go live first, then bill this order.")
	case errors.Is(err, app.ErrQBOrderAlreadyInvoiced):
		redirectFlashError(w, r, qbPreviewPath, "That order already has a QuickBooks invoice.")
	case errors.Is(err, app.ErrQBOrderNotBillable):
		redirectFlashError(w, r, qbPreviewPath, "That order cannot be invoiced through QuickBooks.")
	case err != nil:
		slog.Error("admin qb bill order", "error", err, "order_id", orderID)
		Error(w, r, err)
	default:
		redirectFlash(w, r, qbPreviewPath, "Invoicing started. It appears in QuickBooks shortly.")
	}
}

// enqueueQBChain starts the invoice chain inside the caller's transaction, so
// the job cannot outlive a rolled-back decision to bill.
func (d *Deps) enqueueQBChain(ctx context.Context, tx pgx.Tx, customerID, orderID uuid.UUID, staffRequested bool) error {
	// Unique by args: two quick clicks on Bill now would otherwise enqueue two
	// chains. The DocNumber probe keeps QBO to one invoice, but nothing
	// downstream would stop two send jobs emailing the customer twice.
	//
	// The window is a minute, not an hour: long enough to swallow a double
	// click, short enough that a staffer retrying after a visible failure is
	// not silently ignored while the page tells them invoicing has started.
	_, err := d.RiverClient.InsertTx(ctx, tx, jobs.EnsureQBCustomerArgs{
		CustomerID:     customerID,
		OrderID:        orderID,
		StaffRequested: staffRequested,
	}, &river.InsertOpts{
		UniqueOpts: river.UniqueOpts{ByArgs: true, ByPeriod: time.Minute},
	})
	return err
}
