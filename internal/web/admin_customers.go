package web

import (
	"errors"
	"fmt"
	"net/http"
	"net/mail"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/dukerupert/hiri/internal/app"
	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/platform/auth"
	"github.com/dukerupert/hiri/internal/store"
	"github.com/dukerupert/hiri/internal/ui/admin"
)

// handleAdminCustomerList serves both channels of the customer list. Retail and
// wholesale are the same query with a different account type, a different column
// set, and — on the wholesale side — the application review actions on the row.
// They used to be two pages (/admin/customers and /admin/wholesale) listing the
// same rows, which left staff guessing which one carried the button they wanted.
func (d *Deps) handleAdminCustomerList(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	channel := customerChannel(r)
	wholesale := channel == "wholesale"

	q := r.URL.Query()
	search := strings.TrimSpace(q.Get("q"))

	page := 1
	if p, err := strconv.Atoi(q.Get("page")); err == nil && p > 0 {
		page = p
	}

	// The application status only exists on wholesale rows; carrying it into the
	// retail channel would filter every row out.
	statusFilter := ""
	if wholesale {
		statusFilter = normalizeCustomerStatus(q.Get("status"))
	}
	verifiedFilter := normalizeCustomerVerified(q.Get("verified"))
	sort := normalizeCustomerSort(q.Get("sort"))

	perPage := 25
	filter := store.CustomerFilter{
		Search: search,
		Sort:   sort,
		Limit:  perPage + 1,
		Offset: (page - 1) * perPage,
	}
	applyCustomerFilters(channel, statusFilter, verifiedFilter, &filter)

	var customers []domain.Customer
	var suggestions []domain.Customer
	var priceLists []domain.PriceList
	var totalCount int
	counts := map[string]int{}

	err := store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		var txErr error
		customers, txErr = d.CustomerService.ListCustomers(ctx, tx, filter)
		if txErr != nil {
			return txErr
		}
		totalCount, txErr = d.CustomerService.CountCustomers(ctx, tx, filter)
		if txErr != nil {
			return txErr
		}

		// Per-status counts drive the wholesale status pills and — as "pending" —
		// the badge on the channel toggle, which is why they are loaded on the
		// retail channel too: waiting applications are work either way. They
		// ignore the search term and the status pill, so each number is what
		// clicking that pill would show.
		base := store.CustomerFilter{AccountType: ptrTo(domain.AccountTypeWholesale)}
		if verifiedFilter != "" {
			applyCustomerFilters("wholesale", "", verifiedFilter, &base)
		}
		for _, st := range wholesaleStatusValues {
			f := base
			f.WholesaleStatus = ptrTo(domain.WholesaleStatus(st))
			counts[st], txErr = d.CustomerService.CountCustomers(ctx, tx, f)
			if txErr != nil {
				return txErr
			}
		}

		if wholesale {
			priceLists, txErr = d.PriceListService.List(ctx, tx)
			if txErr != nil {
				return txErr
			}
		}

		// Only reach for fuzzy matches when an actual search term found nothing
		// — otherwise the operator is just paging an empty filter combination.
		// Suggestions span both channels, so a search on the wrong side still
		// finds the account.
		if len(customers) == 0 && search != "" {
			suggestions, txErr = d.CustomerService.SuggestCustomers(ctx, tx, search, 5)
			if txErr != nil {
				return txErr
			}
		}
		return nil
	})
	if err != nil {
		Error(w, r, err)
		return
	}

	hasMore := len(customers) > perPage
	if hasMore {
		customers = customers[:perPage]
	}

	name, role := staffNameRole(r)
	props := admin.CustomerListProps{
		Customers:      customers,
		Suggestions:    suggestions,
		Channel:        channel,
		Search:         search,
		StatusFilter:   statusFilter,
		VerifiedFilter: verifiedFilter,
		Sort:           string(sort),
		PriceLists:     priceLists,
		Counts:         counts,
		TotalCount:     totalCount,
		Page:           page,
		PerPage:        perPage,
		HasMore:        hasMore,
		MerchantTZ:     d.MerchantTZ,
		StaffName:      name,
		StaffRole:      role,
	}

	if IsHTMX(r) {
		admin.CustomerListContent(props).Render(ctx, w) //nolint:errcheck
		return
	}
	admin.CustomerList(props).Render(ctx, w) //nolint:errcheck
}

// handleAdminWholesaleRedirect keeps the old /admin/wholesale bookmark working:
// it is now the wholesale channel of the customer list.
func (d *Deps) handleAdminWholesaleRedirect(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, wholesaleListURL(r.URL.Query().Get("status")), http.StatusMovedPermanently)
}

// customerChannel reads the channel from the route: /admin/customers/wholesale
// is the wholesale side, everything else is retail.
func customerChannel(r *http.Request) string {
	if strings.HasPrefix(r.URL.Path, admin.CustomersWholesalePath) {
		return "wholesale"
	}
	return ""
}

// wholesaleStatusValues are the application statuses, in pill display order.
var wholesaleStatusValues = []string{"pending", "approved", "suspended", "declined"}

// normalizeCustomerStatus clamps ?status= to the wholesale status values.
func normalizeCustomerStatus(v string) string {
	switch v {
	case "pending", "approved", "suspended", "declined":
		return v
	default:
		return ""
	}
}

// normalizeCustomerVerified clamps ?verified= to yes/no/all.
func normalizeCustomerVerified(v string) string {
	switch v {
	case "yes", "no":
		return v
	default:
		return ""
	}
}

// normalizeCustomerSort clamps ?sort= to the closed sort enum.
func normalizeCustomerSort(v string) store.CustomerSort {
	switch store.CustomerSort(v) {
	case store.CustomerSortCreatedAsc,
		store.CustomerSortNameAsc,
		store.CustomerSortNameDesc,
		store.CustomerSortEmailAsc,
		store.CustomerSortEmailDesc:
		return store.CustomerSort(v)
	default:
		return store.CustomerSortCreatedDesc
	}
}

// applyCustomerFilters translates the normalized query params onto a store
// filter. The channel picks the account type; a wholesale status implies it too,
// since the column is NULL on retail rows and pairing the two returns nothing.
func applyCustomerFilters(channel, statusFilter, verifiedFilter string, f *store.CustomerFilter) {
	f.AccountType = nil
	f.WholesaleStatus = nil
	f.EmailVerified = nil

	switch channel {
	case "wholesale":
		f.AccountType = ptrTo(domain.AccountTypeWholesale)
	default:
		f.AccountType = ptrTo(domain.AccountTypeRetail)
	}

	if statusFilter != "" {
		f.WholesaleStatus = ptrTo(domain.WholesaleStatus(statusFilter))
		f.AccountType = ptrTo(domain.AccountTypeWholesale)
	}

	switch verifiedFilter {
	case "yes":
		f.EmailVerified = ptrTo(true)
	case "no":
		f.EmailVerified = ptrTo(false)
	}
}

func (d *Deps) handleAdminCustomerShow(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	var customer *domain.Customer
	var addresses []domain.Address
	var priceLists []domain.PriceList
	var recentOrders []domain.Order
	var subscriptions []domain.Subscription
	var activity []domain.AuditEntry
	var orderCount, lifetimeSpend int
	var announcementsEnabled bool
	var approvedByName string
	var teamMembers []domain.CustomerUser
	// Only read when the equipment service module is on. A shop that does not
	// service machines pays nothing for a card it will never see.
	serviceEnabled := d.ModuleService.Enabled(domain.ModuleEquipmentService)
	var equipment []domain.Equipment
	var serviceCost []domain.ServiceCostWindow
	var serviceLaborRates domain.ServiceLaborRates

	err = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		var txErr error
		customer, txErr = d.CustomerService.GetCustomer(ctx, tx, id)
		if txErr != nil {
			return txErr
		}
		addresses, txErr = d.CustomerService.ListAddresses(ctx, tx, id)
		if txErr != nil {
			return txErr
		}
		priceLists, txErr = d.PriceListService.List(ctx, tx)
		if txErr != nil {
			return txErr
		}
		recentOrders, txErr = d.OrderService.ListOrders(ctx, tx, store.OrderFilter{
			CustomerID: &id,
			Limit:      5,
		})
		if txErr != nil {
			return txErr
		}
		// Count and spend exclude abandoned intents and cancelled/refunded
		// orders, so the header numbers mean the same thing the revenue
		// dashboards mean. Recent orders above stay unfiltered — staff need to
		// see the cancelled one they are being asked about.
		lifetime := store.OrderFilter{
			CustomerID:               &id,
			ExcludeUnconfirmed:       true,
			ExcludeCancelledRefunded: true,
		}
		orderCount, txErr = d.OrderService.CountOrders(ctx, tx, lifetime)
		if txErr != nil {
			return txErr
		}
		lifetimeSpend, txErr = d.OrderService.SumOrderRevenue(ctx, tx, lifetime)
		if txErr != nil {
			return txErr
		}
		subscriptions, txErr = d.SubscriptionService.ListSubscriptionsByCustomer(ctx, tx, id)
		if txErr != nil {
			return txErr
		}
		announcementsEnabled, txErr = d.AnnouncementService.GetAnnouncementsEnabled(ctx, tx, id)
		if txErr != nil {
			return txErr
		}
		activity, txErr = d.AuditQueryService.ListForCustomer(ctx, tx, id, 25)
		if txErr != nil {
			return txErr
		}

		// Additional logins on the account. Retail customers never have any, so
		// this is empty for them and the rail card does not render.
		teamMembers, txErr = d.CustomerUserService.List(ctx, tx, id)
		if txErr != nil {
			return txErr
		}

		if serviceEnabled {
			equipment, txErr = d.EquipmentService.ListForCustomer(ctx, tx, id)
			if txErr != nil {
				return txErr
			}
			// Scoped to the customer rather than to their machines, so the
			// call-outs that never named one still count — those hours went
			// into the account just the same.
			serviceCost, txErr = d.ServiceTicketService.CostForCustomer(ctx, tx, id, time.Now())
			if txErr != nil {
				return txErr
			}
			serviceLaborRates, txErr = d.ServiceTicketService.LaborRates(ctx, tx)
			if txErr != nil {
				return txErr
			}
		}

		// Who approved the wholesale application. A staff record that has since
		// been removed leaves the name blank rather than failing the page — the
		// approval date is still worth showing on its own.
		if customer.ApprovedBy != nil {
			approver, sErr := d.StaffService.Get(ctx, tx, *customer.ApprovedBy)
			if sErr == nil && approver != nil {
				approvedByName = approver.Name
			} else if sErr != nil && !errors.Is(sErr, app.ErrStaffNotFound) {
				return sErr
			}
		}
		return nil
	})
	if err != nil {
		Error(w, r, err)
		return
	}

	// ListOrders is newest-first, so the first row is the last order placed.
	var lastOrderAt *time.Time
	if len(recentOrders) > 0 {
		placed := recentOrders[0].PlacedAt
		lastOrderAt = &placed
	}

	name, role := staffNameRole(r)
	props := admin.CustomerShowProps{
		Customer:             customer,
		Addresses:            addresses,
		PriceLists:           priceLists,
		RecentOrders:         recentOrders,
		Subscriptions:        subscriptions,
		Activity:             activity,
		AnnouncementsEnabled: announcementsEnabled,
		OrderCount:           orderCount,
		LifetimeSpend:        lifetimeSpend,
		LastOrderAt:          lastOrderAt,
		MerchantTZ:           d.MerchantTZ,
		StaffName:            name,
		StaffRole:            role,
		CanEditEmail:         staffCan(r, auth.PermEditCustomers),
		Equipment:            equipment,
		ServiceCost:          serviceCost,
		ServiceLaborRates:    serviceLaborRates,
		ServiceEnabled:       serviceEnabled,
		CanWriteService:      staffCan(r, auth.PermWriteService),
		ApprovedByName:       approvedByName,
		TeamMembers:          teamMembers,
	}

	if IsHTMX(r) {
		admin.CustomerShowContent(props).Render(ctx, w) //nolint:errcheck
		return
	}
	admin.CustomerShow(props).Render(ctx, w) //nolint:errcheck
}

func (d *Deps) handleAdminCustomerPaymentTerms(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	var days *int
	if v := r.FormValue("payment_terms_days"); v != "" {
		d, err := strconv.Atoi(v)
		if err != nil || !domain.ValidPaymentTermsDays(d) {
			http.Error(w, "Invalid payment terms", http.StatusBadRequest)
			return
		}
		days = &d
	}

	err = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		return d.CustomerService.UpdatePaymentTerms(ctx, tx, id, days, staffActor(r))
	})
	if err != nil {
		Error(w, r, err)
		return
	}

	http.Redirect(w, r, fmt.Sprintf("/admin/customers/%s", id), http.StatusSeeOther)
}

// handleAdminCustomerEmail changes the address a customer signs in and receives
// mail at. Used when a customer loses access to the address on file — a shared
// shop inbox moving to a new provider, a staffer leaving a wholesale account.
// Mounted behind customers:write: this is the front half of an account
// takeover, so it stays with the roles trusted to edit customer records.
func (d *Deps) handleAdminCustomerEmail(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	email := strings.TrimSpace(r.FormValue("email"))
	if len(email) > 255 {
		http.Error(w, "Email address too long", http.StatusBadRequest)
		return
	}
	if _, err := mail.ParseAddress(email); err != nil {
		http.Error(w, "Invalid email address", http.StatusBadRequest)
		return
	}

	err = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		_, txErr := d.CustomerService.UpdateEmailAsStaff(ctx, tx, id, email, staffActor(r))
		return txErr
	})
	if err != nil {
		Error(w, r, err)
		return
	}

	http.Redirect(w, r, fmt.Sprintf("/admin/customers/%s", id), http.StatusSeeOther)
}

func (d *Deps) handleAdminCustomerPriceList(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	var priceListID *uuid.UUID
	if v := r.FormValue("price_list_id"); v != "" {
		parsed, err := uuid.Parse(v)
		if err != nil {
			http.Error(w, "Invalid price list", http.StatusBadRequest)
			return
		}
		priceListID = &parsed
	}

	err = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		return d.CustomerService.UpdatePriceList(ctx, tx, id, priceListID, staffActor(r))
	})
	if err != nil {
		Error(w, r, err)
		return
	}

	http.Redirect(w, r, fmt.Sprintf("/admin/customers/%s", id), http.StatusSeeOther)
}

func (d *Deps) handleAdminCustomerBillingMethod(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	method := domain.BillingMethod(r.FormValue("billing_method"))
	if method != domain.BillingMethodManual && method != domain.BillingMethodACH && method != domain.BillingMethodCreditCard {
		http.Error(w, "Invalid billing method", http.StatusBadRequest)
		return
	}

	err = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		return d.CustomerService.UpdateBillingMethod(ctx, tx, id, method, staffActor(r))
	})
	if err != nil {
		Error(w, r, err)
		return
	}

	http.Redirect(w, r, fmt.Sprintf("/admin/customers/%s", id), http.StatusSeeOther)
}

// handleAdminCustomerSendPasswordSetup triggers a password setup/reset email
// to a customer. Works whether the customer has a password set or not — the
// email wording adapts. Used by staff when a customer reports trouble signing in.
func (d *Deps) handleAdminCustomerSendPasswordSetup(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	if err := d.AuthService.SendPasswordSetupEmail(ctx, d.Pool, id, staffActor(r)); err != nil {
		Error(w, r, err)
		return
	}

	http.Redirect(w, r, fmt.Sprintf("/admin/customers/%s", id), http.StatusSeeOther)
}

// handleAdminCustomerSendVerification emails the customer a link that proves
// the address on file works. Redeeming it also signs them in, which is the
// point after a staff-corrected address: the customer confirms the new address
// and lands in their account without a password reset they didn't ask for.
//
// Ungated like send-password-setup beside it — both mail a sign-in link to the
// address already on record, so neither widens what the recipient can reach.
// Changing that address is the privileged move, and that route is gated.
func (d *Deps) handleAdminCustomerSendVerification(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	if err := d.AuthService.SendVerificationEmailAsStaff(ctx, d.Pool, id, staffActor(r)); err != nil {
		Error(w, r, err)
		return
	}

	http.Redirect(w, r, fmt.Sprintf("/admin/customers/%s", id), http.StatusSeeOther)
}

func (d *Deps) handleAdminCustomerLocalFulfillment(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	var preferred *domain.ShippingMethod
	switch v := r.FormValue("local_fulfillment"); v {
	case "":
		preferred = nil
	case string(domain.ShippingMethodLocalDelivery):
		m := domain.ShippingMethodLocalDelivery
		preferred = &m
	case string(domain.ShippingMethodPickup):
		m := domain.ShippingMethodPickup
		preferred = &m
	default:
		http.Error(w, "Invalid local fulfillment preference", http.StatusBadRequest)
		return
	}

	err = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		return d.CustomerService.UpdatePreferredLocalFulfillment(ctx, tx, id, preferred, staffActor(r))
	})
	if err != nil {
		Error(w, r, err)
		return
	}

	http.Redirect(w, r, fmt.Sprintf("/admin/customers/%s", id), http.StatusSeeOther)
}
