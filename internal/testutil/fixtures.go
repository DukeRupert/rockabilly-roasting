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

type CustomerOption func(*sqlcgen.CreateCustomerParams)

func WithEmail(email string) CustomerOption {
	return func(p *sqlcgen.CreateCustomerParams) { p.Email = email }
}

func WithGuest(isGuest bool) CustomerOption {
	return func(p *sqlcgen.CreateCustomerParams) { p.IsGuest = isGuest }
}

func WithCustomerName(first, last string) CustomerOption {
	return func(p *sqlcgen.CreateCustomerParams) {
		p.FirstName = first
		p.LastName = last
	}
}

func CreateCustomer(t *testing.T, tx pgx.Tx, opts ...CustomerOption) *domain.Customer {
	t.Helper()
	p := sqlcgen.CreateCustomerParams{
		ID:        uuid.New(),
		Email:     fmt.Sprintf("test-%s@example.com", uuid.New().String()[:8]),
		FirstName: "Test",
		LastName:  "Customer",
		IsGuest:   false,
	}
	for _, o := range opts {
		o(&p)
	}
	row, err := sqlcgen.New(tx).CreateCustomer(context.Background(), p)
	if err != nil {
		t.Fatalf("create customer fixture: %v", err)
	}
	return &domain.Customer{
		ID:            row.ID,
		Email:         row.Email,
		EmailVerified: row.EmailVerified,
		PasswordHash:  row.PasswordHash,
		FirstName:     row.FirstName,
		LastName:      row.LastName,
		Phone:         row.Phone,
		IsGuest:       row.IsGuest,
		TaxExempt:     row.TaxExempt,
		CreatedAt:     row.CreatedAt,
		UpdatedAt:     row.UpdatedAt,
	}
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

type ProductOption func(*sqlcgen.CreateProductParams)

func WithProductSlug(slug string) ProductOption {
	return func(p *sqlcgen.CreateProductParams) { p.Slug = slug }
}

func WithProductTitle(title string) ProductOption {
	return func(p *sqlcgen.CreateProductParams) { p.Title = title }
}

func WithProductStatus(status domain.ProductStatus) ProductOption {
	return func(p *sqlcgen.CreateProductParams) { p.Status = string(status) }
}

func WithProductTaxonID(taxonID uuid.UUID) ProductOption {
	return func(p *sqlcgen.CreateProductParams) { p.TaxonID = &taxonID }
}

func CreateProduct(t *testing.T, tx pgx.Tx, opts ...ProductOption) *domain.Product {
	t.Helper()
	slug := fmt.Sprintf("product-%s", uuid.New().String()[:8])
	p := sqlcgen.CreateProductParams{
		ID:          uuid.New(),
		Slug:        slug,
		Title:       "Test Product",
		Description: "A test product",
		Status:      string(domain.ProductStatusActive),
		Metadata:    json.RawMessage(`{}`),
	}
	for _, o := range opts {
		o(&p)
	}
	// If no taxon set, create one.
	if p.TaxonID == nil {
		taxon := CreateTaxon(t, tx)
		p.TaxonID = &taxon.ID
	}
	row, err := sqlcgen.New(tx).CreateProduct(context.Background(), p)
	if err != nil {
		t.Fatalf("create product fixture: %v", err)
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
		TaxonID:     taxonID,
		CreatedAt:   row.CreatedAt,
		UpdatedAt:   row.UpdatedAt,
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

// --- helpers ---

func int32PtrToIntPtr(p *int32) *int {
	if p == nil {
		return nil
	}
	v := int(*p)
	return &v
}
