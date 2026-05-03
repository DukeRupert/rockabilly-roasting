package testutil

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"testing"

	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/store/sqlcgen"
	"github.com/dukerupert/hiri/internal/app"
)

// --- Customer ---

type customerInput struct {
	params      sqlcgen.CreateCustomerParams
	priceListID *uuid.UUID
}

type CustomerOption func(*customerInput)

func WithEmail(email string) CustomerOption {
	return func(i *customerInput) { i.params.Email = email }
}

func WithCustomerName(first, last string) CustomerOption {
	return func(i *customerInput) {
		i.params.FirstName = first
		i.params.LastName = last
	}
}

func WithPhone(phone *string) CustomerOption {
	return func(i *customerInput) { i.params.Phone = phone }
}

// WithPriceList stamps the customer's price_list_id after insert. The caller
// is responsible for ensuring the price list exists.
func WithPriceList(id uuid.UUID) CustomerOption {
	return func(i *customerInput) { i.priceListID = &id }
}

func CreateCustomer(t *testing.T, tx pgx.Tx, opts ...CustomerOption) *domain.Customer {
	t.Helper()
	in := customerInput{
		params: sqlcgen.CreateCustomerParams{
			ID:        uuid.New(),
			Email:     fmt.Sprintf("test-%s@example.com", uuid.New().String()[:8]),
			FirstName: "Test",
			LastName:  "Customer",
		},
	}
	for _, o := range opts {
		o(&in)
	}
	row, err := sqlcgen.New(tx).CreateCustomer(context.Background(), in.params)
	if err != nil {
		t.Fatalf("create customer fixture: %v", err)
	}
	if in.priceListID != nil {
		if err := sqlcgen.New(tx).UpdateCustomerPriceList(context.Background(), sqlcgen.UpdateCustomerPriceListParams{
			ID:          row.ID,
			PriceListID: in.priceListID,
		}); err != nil {
			t.Fatalf("set price list on customer fixture: %v", err)
		}
	}
	return &domain.Customer{
		ID:            row.ID,
		Email:         row.Email,
		EmailVerified: row.EmailVerified,
		PasswordHash:  row.PasswordHash,
		FirstName:     row.FirstName,
		LastName:      row.LastName,
		Phone:         row.Phone,
		TaxExempt:     row.TaxExempt,
		PriceListID:   in.priceListID,
		CreatedAt:     row.CreatedAt,
		UpdatedAt:     row.UpdatedAt,
	}
}

// --- Customer group ---

func CreateCustomerGroup(t *testing.T, tx pgx.Tx, name string) *domain.CustomerGroup {
	t.Helper()
	if name == "" {
		name = fmt.Sprintf("group-%s", uuid.New().String()[:8])
	}
	row, err := sqlcgen.New(tx).CreateCustomerGroup(context.Background(), sqlcgen.CreateCustomerGroupParams{
		ID:       uuid.New(),
		Name:     name,
		Metadata: json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("create customer group fixture: %v", err)
	}
	return &domain.CustomerGroup{
		ID:   row.ID,
		Name: row.Name,
	}
}

// --- Price list ---

type priceListInput struct {
	name   string
	status string
}

type PriceListOption func(*priceListInput)

func WithPriceListStatus(status string) PriceListOption {
	return func(i *priceListInput) { i.status = status }
}

// CreatePriceList inserts a row into price_lists and returns it. Status defaults
// to "active". Inserts directly via SQL — there is no PriceListStore today.
func CreatePriceList(t *testing.T, tx pgx.Tx, opts ...PriceListOption) *domain.PriceList {
	t.Helper()
	in := priceListInput{
		name:   fmt.Sprintf("price-list-%s", uuid.New().String()[:8]),
		status: string(domain.PriceListStatusActive),
	}
	for _, o := range opts {
		o(&in)
	}
	id := uuid.New()
	_, err := tx.Exec(context.Background(),
		`INSERT INTO price_lists (id, name, type, status) VALUES ($1, $2, 'override', $3)`,
		id, in.name, in.status,
	)
	if err != nil {
		t.Fatalf("create price list fixture: %v", err)
	}
	return &domain.PriceList{
		ID:     id,
		Name:   in.name,
		Type:   domain.PriceListTypeOverride,
		Status: domain.PriceListStatus(in.status),
	}
}

// CreatePriceListPrice writes a (price_set, price_list, amount) row for a variant.
// Auto-creates the price_set if one doesn't exist for the variant.
func CreatePriceListPrice(t *testing.T, tx pgx.Tx, priceListID, variantID uuid.UUID, amountCents int, currencyCode string) *domain.Price {
	t.Helper()
	psID := getOrCreatePriceSet(t, tx, variantID)
	priceID := uuid.New()
	_, err := tx.Exec(context.Background(),
		`INSERT INTO prices (id, price_set_id, amount, currency_code, price_list_id)
		 VALUES ($1, $2, $3, $4, $5)`,
		priceID, psID, amountCents, currencyCode, priceListID,
	)
	if err != nil {
		t.Fatalf("create price list price fixture: %v", err)
	}
	pl := priceListID
	return &domain.Price{
		ID:           priceID,
		PriceSetID:   psID,
		Amount:       amountCents,
		CurrencyCode: currencyCode,
		PriceListID:  &pl,
	}
}

// SetBasePriceForVariant inserts a base-price row for a variant. Mirrors
// PricingService.SetBasePrice but stays inside testutil so tests don't need
// a full service wiring.
func SetBasePriceForVariant(t *testing.T, tx pgx.Tx, variantID uuid.UUID, amountCents int, currencyCode string) {
	t.Helper()
	psID := getOrCreatePriceSet(t, tx, variantID)
	_, err := tx.Exec(context.Background(),
		`INSERT INTO prices (id, price_set_id, amount, currency_code) VALUES ($1, $2, $3, $4)`,
		uuid.New(), psID, amountCents, currencyCode,
	)
	if err != nil {
		t.Fatalf("set base price fixture: %v", err)
	}
}

func getOrCreatePriceSet(t *testing.T, tx pgx.Tx, variantID uuid.UUID) uuid.UUID {
	t.Helper()
	q := sqlcgen.New(tx)
	if row, err := q.GetPriceSetByVariant(context.Background(), variantID); err == nil {
		return row.ID
	}
	row, err := q.CreatePriceSet(context.Background(), sqlcgen.CreatePriceSetParams{
		ID:        uuid.New(),
		VariantID: variantID,
	})
	if err != nil {
		t.Fatalf("create price set fixture: %v", err)
	}
	return row.ID
}

// --- Address ---

type AddressOption func(*sqlcgen.CreateAddressParams)

func WithAddressLine1(line1 string) AddressOption {
	return func(p *sqlcgen.CreateAddressParams) { p.Line1 = line1 }
}

func CreateAddress(t *testing.T, tx pgx.Tx, customerID uuid.UUID, opts ...AddressOption) *domain.Address {
	t.Helper()
	p := sqlcgen.CreateAddressParams{
		ID:          uuid.New(),
		CustomerID:  &customerID,
		FirstName:   "Test",
		LastName:    "Address",
		Line1:       "123 Main St",
		City:        "Portland",
		State:       "OR",
		PostalCode:  "97201",
		CountryCode: "US",
		IsDefault:   false,
	}
	for _, o := range opts {
		o(&p)
	}
	row, err := sqlcgen.New(tx).CreateAddress(context.Background(), p)
	if err != nil {
		t.Fatalf("create address fixture: %v", err)
	}
	return &domain.Address{
		ID:          row.ID,
		CustomerID:  row.CustomerID,
		FirstName:   row.FirstName,
		LastName:    row.LastName,
		Company:     row.Company,
		Line1:       row.Line1,
		Line2:       row.Line2,
		City:        row.City,
		State:       row.State,
		PostalCode:  row.PostalCode,
		CountryCode: row.CountryCode,
		IsDefault:   row.IsDefault,
	}
}

// --- Taxon (needed for products) ---

func CreateTaxon(t *testing.T, tx pgx.Tx) *domain.Taxon {
	t.Helper()
	row, err := sqlcgen.New(tx).CreateTaxon(context.Background(), sqlcgen.CreateTaxonParams{
		ID:       uuid.New(),
		Name:     fmt.Sprintf("taxon-%s", uuid.New().String()[:8]),
		Slug:     fmt.Sprintf("taxon-%s", uuid.New().String()[:8]),
		Position: 0,
		Depth:    0,
	})
	if err != nil {
		t.Fatalf("create taxon fixture: %v", err)
	}
	return &domain.Taxon{
		ID:       row.ID,
		ParentID: row.ParentID,
		Name:     row.Name,
		Slug:     row.Slug,
		Position: int(row.Position),
		Depth:    int(row.Depth),
	}
}

// --- Product ---

type productInput struct {
	params     sqlcgen.CreateProductParams
	visibility *domain.ProductVisibility
}

type ProductOption func(*productInput)

func WithProductSlug(slug string) ProductOption {
	return func(i *productInput) { i.params.Slug = slug }
}

func WithProductTitle(title string) ProductOption {
	return func(i *productInput) { i.params.Title = title }
}

func WithProductStatus(status domain.ProductStatus) ProductOption {
	return func(i *productInput) { i.params.Status = string(status) }
}

func WithProductTaxonID(taxonID uuid.UUID) ProductOption {
	return func(i *productInput) { i.params.TaxonID = &taxonID }
}

// WithProductVisibility sets the product's visibility column ("public", "wholesale",
// or "restricted") via UPDATE after CreateProduct, since CreateProductParams does
// not expose the column.
func WithProductVisibility(v domain.ProductVisibility) ProductOption {
	return func(i *productInput) { i.visibility = &v }
}

func CreateProduct(t *testing.T, tx pgx.Tx, opts ...ProductOption) *domain.Product {
	t.Helper()
	slug := fmt.Sprintf("product-%s", uuid.New().String()[:8])
	in := productInput{
		params: sqlcgen.CreateProductParams{
			ID:          uuid.New(),
			Slug:        slug,
			Title:       "Test Product",
			Description: "A test product",
			Status:      string(domain.ProductStatusActive),
			Metadata:    json.RawMessage(`{}`),
		},
	}
	for _, o := range opts {
		o(&in)
	}
	// If no taxon set, create one.
	if in.params.TaxonID == nil {
		taxon := CreateTaxon(t, tx)
		in.params.TaxonID = &taxon.ID
	}
	row, err := sqlcgen.New(tx).CreateProduct(context.Background(), in.params)
	if err != nil {
		t.Fatalf("create product fixture: %v", err)
	}
	visibility := domain.ProductVisibility(row.Visibility)
	if in.visibility != nil {
		updated, err := sqlcgen.New(tx).UpdateProductVisibility(context.Background(), sqlcgen.UpdateProductVisibilityParams{
			ID:         row.ID,
			Visibility: string(*in.visibility),
		})
		if err != nil {
			t.Fatalf("set product visibility fixture: %v", err)
		}
		visibility = domain.ProductVisibility(updated.Visibility)
	}
	var taxonID uuid.UUID
	if row.TaxonID != nil {
		taxonID = *row.TaxonID
	}
	return &domain.Product{
		ID:          row.ID,
		Slug:        row.Slug,
		Title:       row.Title,
		Description: row.Description,
		Status:      domain.ProductStatus(row.Status),
		Visibility:  visibility,
		TaxonID:     taxonID,
		CreatedAt:   row.CreatedAt,
		UpdatedAt:   row.UpdatedAt,
	}
}

// AddProductGroupVisibility grants a customer group access to a restricted product.
func AddProductGroupVisibility(t *testing.T, tx pgx.Tx, productID, customerGroupID uuid.UUID) {
	t.Helper()
	if err := sqlcgen.New(tx).SetProductGroupVisibility(context.Background(), sqlcgen.SetProductGroupVisibilityParams{
		ProductID:       productID,
		CustomerGroupID: customerGroupID,
	}); err != nil {
		t.Fatalf("add product group visibility fixture: %v", err)
	}
}

// --- Variant ---

type VariantOption func(*sqlcgen.CreateVariantParams)

func WithSKU(sku string) VariantOption {
	return func(p *sqlcgen.CreateVariantParams) { p.Sku = sku }
}

func CreateVariant(t *testing.T, tx pgx.Tx, productID uuid.UUID, opts ...VariantOption) *domain.Variant {
	t.Helper()
	p := sqlcgen.CreateVariantParams{
		ID:        uuid.New(),
		ProductID: productID,
		Sku:       fmt.Sprintf("SKU-%s", uuid.New().String()[:8]),
		Position:  0,
		IsDefault: true,
		Metadata:  json.RawMessage(`{}`),
	}
	for _, o := range opts {
		o(&p)
	}
	row, err := sqlcgen.New(tx).CreateVariant(context.Background(), p)
	if err != nil {
		t.Fatalf("create variant fixture: %v", err)
	}
	return &domain.Variant{
		ID:        row.ID,
		ProductID: row.ProductID,
		SKU:       row.Sku,
		Barcode:   row.Barcode,
		Position:  int(row.Position),
		IsDefault: row.IsDefault,
		CreatedAt: row.CreatedAt,
		UpdatedAt: row.UpdatedAt,
	}
}

// --- Order ---

type OrderOption func(*sqlcgen.CreateOrderParams)

func WithOrderStatus(status domain.OrderStatus) OrderOption {
	return func(p *sqlcgen.CreateOrderParams) { p.Status = string(status) }
}

func WithPaymentStatus(status domain.PaymentStatus) OrderOption {
	return func(p *sqlcgen.CreateOrderParams) { p.PaymentStatus = string(status) }
}

func WithFulfillmentStatus(status domain.FulfillmentStatus) OrderOption {
	return func(p *sqlcgen.CreateOrderParams) { p.FulfillmentStatus = string(status) }
}

func WithOrderTotals(subtotal, discount, shipping, tax, total int) OrderOption {
	return func(p *sqlcgen.CreateOrderParams) {
		p.Subtotal = int32(subtotal)
		p.DiscountTotal = int32(discount)
		p.ShippingTotal = int32(shipping)
		p.TaxTotal = int32(tax)
		p.Total = int32(total)
	}
}

func WithShippingMethod(m domain.ShippingMethod) OrderOption {
	return func(p *sqlcgen.CreateOrderParams) {
		s := string(m)
		p.ShippingMethod = &s
	}
}

func CreateOrder(t *testing.T, tx pgx.Tx, customerID, shippingAddrID, billingAddrID uuid.UUID, opts ...OrderOption) *domain.Order {
	t.Helper()
	p := sqlcgen.CreateOrderParams{
		ID:                uuid.New(),
		Number:            fmt.Sprintf("ORD-TEST-%s", uuid.New().String()[:8]),
		CustomerID:        &customerID,
		Status:            string(domain.OrderStatusConfirmed),
		PaymentStatus:     string(domain.PaymentStatusCaptured),
		FulfillmentStatus: string(domain.FulfillmentStatusUnfulfilled),
		CurrencyCode:      "USD",
		Subtotal:          10000,
		DiscountTotal:     0,
		ShippingTotal:     500,
		TaxTotal:          800,
		Total:             11300,
		ShippingAddressID: shippingAddrID,
		BillingAddressID:  billingAddrID,
		Metadata:          json.RawMessage(`{}`),
		PlacedAt:          time.Now(),
	}
	for _, o := range opts {
		o(&p)
	}
	row, err := sqlcgen.New(tx).CreateOrder(context.Background(), p)
	if err != nil {
		t.Fatalf("create order fixture: %v", err)
	}
	return &domain.Order{
		ID:                row.ID,
		Number:            row.Number,
		CustomerID:        row.CustomerID,
		Status:            domain.OrderStatus(row.Status),
		PaymentStatus:     domain.PaymentStatus(row.PaymentStatus),
		FulfillmentStatus: domain.FulfillmentStatus(row.FulfillmentStatus),
		CurrencyCode:      row.CurrencyCode,
		Subtotal:          int(row.Subtotal),
		DiscountTotal:     int(row.DiscountTotal),
		ShippingTotal:     int(row.ShippingTotal),
		TaxTotal:          int(row.TaxTotal),
		Total:             int(row.Total),
		ShippingAddressID: row.ShippingAddressID,
		BillingAddressID:  row.BillingAddressID,
		PlacedAt:          row.PlacedAt,
		CreatedAt:         row.CreatedAt,
		UpdatedAt:         row.UpdatedAt,
	}
}

// --- Discount ---

type DiscountOption func(*createDiscountInput)

type createDiscountInput struct {
	Name              string
	Type              domain.DiscountType
	Value             int
	Active            bool
	MinimumOrderCents *int
	ExpiresAt         *time.Time
}

func WithDiscountType(dt domain.DiscountType) DiscountOption {
	return func(p *createDiscountInput) { p.Type = dt }
}

func WithDiscountValue(v int) DiscountOption {
	return func(p *createDiscountInput) { p.Value = v }
}

func WithDiscountActive(active bool) DiscountOption {
	return func(p *createDiscountInput) { p.Active = active }
}

func WithMinimumOrder(cents int) DiscountOption {
	return func(p *createDiscountInput) { p.MinimumOrderCents = &cents }
}

func WithDiscountExpiry(t time.Time) DiscountOption {
	return func(p *createDiscountInput) { p.ExpiresAt = &t }
}

func CreateDiscount(t *testing.T, tx pgx.Tx, opts ...DiscountOption) *domain.Discount {
	t.Helper()
	input := createDiscountInput{
		Name:   fmt.Sprintf("discount-%s", uuid.New().String()[:8]),
		Type:   domain.DiscountTypePercentage,
		Value:  10,
		Active: true,
	}
	for _, o := range opts {
		o(&input)
	}

	var minCents *int32
	if input.MinimumOrderCents != nil {
		v := int32(*input.MinimumOrderCents)
		minCents = &v
	}

	params := sqlcgen.CreateDiscountParams{
		ID:                uuid.New(),
		Name:              input.Name,
		Type:              string(input.Type),
		Value:             int32(input.Value),
		MinimumOrderCents: minCents,
		Active:            input.Active,
	}
	if input.ExpiresAt != nil {
		params.ExpiresAt = pgtype.Timestamptz{Time: *input.ExpiresAt, Valid: true}
	}

	row, err := sqlcgen.New(tx).CreateDiscount(context.Background(), params)
	if err != nil {
		t.Fatalf("create discount fixture: %v", err)
	}

	var expiresAt *time.Time
	if row.ExpiresAt.Valid {
		expiresAt = &row.ExpiresAt.Time
	}
	var startsAt *time.Time
	if row.StartsAt.Valid {
		startsAt = &row.StartsAt.Time
	}

	return &domain.Discount{
		ID:                row.ID,
		Name:              row.Name,
		Description:       row.Description,
		Type:              domain.DiscountType(row.Type),
		Value:             int(row.Value),
		MinimumOrderCents: int32PtrToIntPtr(row.MinimumOrderCents),
		StartsAt:          startsAt,
		ExpiresAt:         expiresAt,
		Active:            row.Active,
		CreatedAt:         row.CreatedAt,
		UpdatedAt:         row.UpdatedAt,
	}
}

// --- Coupon Code ---

type CouponOption func(*sqlcgen.CreateCouponCodeParams)

func WithCouponCode(code string) CouponOption {
	return func(p *sqlcgen.CreateCouponCodeParams) { p.Code = code }
}

func CreateCouponCode(t *testing.T, tx pgx.Tx, discountID uuid.UUID, opts ...CouponOption) *domain.CouponCode {
	t.Helper()
	p := sqlcgen.CreateCouponCodeParams{
		ID:         uuid.New(),
		DiscountID: discountID,
		Code:       fmt.Sprintf("COUPON-%s", uuid.New().String()[:8]),
	}
	for _, o := range opts {
		o(&p)
	}
	row, err := sqlcgen.New(tx).CreateCouponCode(context.Background(), p)
	if err != nil {
		t.Fatalf("create coupon code fixture: %v", err)
	}
	var redeemedAt *time.Time
	if row.RedeemedAt.Valid {
		redeemedAt = &row.RedeemedAt.Time
	}
	return &domain.CouponCode{
		ID:         row.ID,
		DiscountID: row.DiscountID,
		Code:       row.Code,
		CustomerID: row.CustomerID,
		RedeemedAt: redeemedAt,
		RedeemedBy: row.RedeemedBy,
		CreatedAt:  row.CreatedAt,
	}
}

// --- Staff ---

// CreateStaff inserts a staff row and returns its ID.
func CreateStaff(t *testing.T, tx pgx.Tx) uuid.UUID {
	t.Helper()
	id := uuid.New()
	_, err := sqlcgen.New(tx).CreateStaff(context.Background(), sqlcgen.CreateStaffParams{
		ID:           id,
		Email:        fmt.Sprintf("staff-%s@example.com", uuid.New().String()[:8]),
		Name:         "Test Staff",
		PasswordHash: "$2a$10$abcdefghijklmnopqrstuuABCDEFGHIJKLMNOPQRSTUVWX", // dummy hash
		Role:         "admin",
	})
	if err != nil {
		t.Fatalf("create staff fixture: %v", err)
	}
	return id
}

// --- Test Actor ---

// TestActor returns a staff actor suitable for audit-requiring service methods.
// NOTE: The actor ID is a random UUID not in the DB. Use TestActorFromStaff when
// a real staff FK reference is needed (e.g. wholesale approved_by).
func TestActor() app.Actor {
	id := uuid.New()
	return app.Actor{
		Type: domain.AuditActorTypeStaff,
		ID:   &id,
		Name: "Test Staff",
	}
}

// TestActorFromStaff returns an actor with the given staff ID (must exist in DB).
func TestActorFromStaff(staffID uuid.UUID) app.Actor {
	return app.Actor{
		Type: domain.AuditActorTypeStaff,
		ID:   &staffID,
		Name: "Test Staff",
	}
}

// --- Assertions ---

// AssertResolvedPrice checks that a resolved price matches the expected cents value.
func AssertResolvedPrice(t *testing.T, wantCents, gotCents int) {
	t.Helper()
	if wantCents != gotCents {
		t.Errorf("resolved price: want %d cents, got %d cents", wantCents, gotCents)
	}
}

// --- helpers ---

func int32PtrToIntPtr(p *int32) *int {
	if p == nil {
		return nil
	}
	v := int(*p)
	return &v
}
