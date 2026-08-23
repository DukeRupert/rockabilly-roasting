package app_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dukerupert/hiri/internal/app"
	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/platform/audit"
	"github.com/dukerupert/hiri/internal/platform/metrics"
	"github.com/dukerupert/hiri/internal/store"
	"github.com/dukerupert/hiri/internal/testutil"
)

func newWhiteLabelService() *app.WhiteLabelService {
	return app.NewWhiteLabelService(
		newCatalogService(),
		newPricingService(),
		store.NewCatalogStore(),
		store.NewAttributeStore(),
		store.NewCustomerStore(),
		audit.NewAuditWriter(),
		metrics.NewRegistry(),
	)
}

// approvedWholesaleCustomer creates a wholesale customer and approves it.
func approvedWholesaleCustomer(t *testing.T, tx pgx.Tx) *domain.Customer {
	t.Helper()
	return approvedWholesaleCustomerNamed(t, tx, "Midnight Diner")
}

func approvedWholesaleCustomerNamed(t *testing.T, tx pgx.Tx, company string) *domain.Customer {
	t.Helper()
	ctx := context.Background()
	wsvc := newWholesaleService()
	staffID := testutil.CreateStaff(t, tx)
	actor := testutil.TestActorFromStaff(staffID)

	c, err := wsvc.SubmitApplication(ctx, tx, app.ApplyParams{
		Email:       fmt.Sprintf("wl-%s@example.com", uuid.New().String()[:8]),
		FirstName:   "Wanda",
		LastName:    "Label",
		CompanyName: company,
	})
	require.NoError(t, err)
	c, err = wsvc.ApproveApplication(ctx, tx, c.ID, actor)
	require.NoError(t, err)
	return c
}

// baseCoffee builds an active, public coffee with one wholesale variant (priced)
// and one retail-only variant (priced) — the retail-only one must NOT be cloned.
func baseCoffee(t *testing.T, tx pgx.Tx) (product *domain.Product, wsVariant, retailVariant *domain.Variant) {
	t.Helper()
	product = testutil.CreateProduct(t, tx,
		testutil.WithProductTitle("Chop Top"),
		testutil.WithProductStatus(domain.ProductStatusActive))
	wsVariant = testutil.CreateVariant(t, tx, product.ID,
		testutil.WithSKU("CHOP-12OZ"), testutil.WithChannelAvailability(true, true))
	retailVariant = testutil.CreateVariant(t, tx, product.ID,
		testutil.WithSKU("CHOP-1LB"), testutil.WithChannelAvailability(true, false))
	testutil.SetBasePriceForVariant(t, tx, wsVariant.ID, 2500, "USD")
	testutil.SetBasePriceForVariant(t, tx, retailVariant.ID, 4000, "USD")
	return product, wsVariant, retailVariant
}

func customerActor(c *domain.Customer) app.Actor {
	return app.Actor{Type: domain.AuditActorTypeCustomer, ID: &c.ID, Name: "white-label onboarding"}
}

func TestSubmitWhiteLabel_CreatesDraftPrivateClonedProduct(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	ctx := context.Background()
	svc := newWhiteLabelService()
	catalog := newCatalogService()
	pricing := newPricingService()

	base, wsVariant, _ := baseCoffee(t, tx)
	customer := approvedWholesaleCustomer(t, tx)

	product, err := svc.SubmitWhiteLabel(ctx, tx, customer.ID, app.WhiteLabelSubmission{
		BaseProductID: base.ID,
		Name:          "Midnight Diner Blend",
		LabelR2Key:    "white-label/label.png",
	}, customerActor(customer))
	require.NoError(t, err)

	// Draft + private + named after the client's label.
	assert.Equal(t, domain.ProductStatusDraft, product.Status)
	assert.Equal(t, domain.ProductVisibilityPrivate, product.Visibility)
	assert.Equal(t, "Midnight Diner Blend", product.Title)

	// Traceability metadata.
	assert.Equal(t, "white_label_onboarding", product.Metadata[app.WhiteLabelMetaSource])
	assert.Equal(t, base.ID.String(), product.Metadata[app.WhiteLabelMetaBaseID])
	assert.Equal(t, customer.ID.String(), product.Metadata[app.WhiteLabelMetaCustomerID])

	// Only the wholesale-available base variant is cloned, with a fresh SKU and the
	// base price copied across.
	variants, err := catalog.ListVariants(ctx, tx, product.ID)
	require.NoError(t, err)
	require.Len(t, variants, 1, "only the wholesale-available base variant should be cloned")
	clone := variants[0]
	assert.Contains(t, clone.SKU, "WL-")
	assert.Contains(t, clone.SKU, wsVariant.SKU)
	assert.False(t, clone.RetailAvailable, "white-label variants are wholesale-only")
	assert.True(t, clone.WholesaleAvailable)

	prices, err := pricing.ListBasePricesByProduct(ctx, tx, product.ID, "USD")
	require.NoError(t, err)
	assert.Equal(t, 2500, prices[clone.ID], "cloned variant keeps the base price")

	// Granted to exactly the submitting customer.
	granted, err := catalog.ListProductCustomerAccess(ctx, tx, product.ID)
	require.NoError(t, err)
	assert.Equal(t, []uuid.UUID{customer.ID}, granted)

	// Label image attached.
	media, err := catalog.ListProductMedia(ctx, tx, product.ID)
	require.NoError(t, err)
	require.Len(t, media, 1)
	assert.Equal(t, "white-label/label.png", media[0].R2Key)
}

func TestSubmitWhiteLabel_Validation(t *testing.T) {
	ctx := context.Background()
	svc := newWhiteLabelService()

	t.Run("name required", func(t *testing.T) {
		tx := testutil.NewTestTx(t, testPool)
		base, _, _ := baseCoffee(t, tx)
		c := approvedWholesaleCustomer(t, tx)
		_, err := svc.SubmitWhiteLabel(ctx, tx, c.ID, app.WhiteLabelSubmission{
			BaseProductID: base.ID, Name: "   ", LabelR2Key: "white-label/x.png",
		}, customerActor(c))
		assert.ErrorIs(t, err, app.ErrWhiteLabelNameRequired)
	})

	t.Run("label required", func(t *testing.T) {
		tx := testutil.NewTestTx(t, testPool)
		base, _, _ := baseCoffee(t, tx)
		c := approvedWholesaleCustomer(t, tx)
		_, err := svc.SubmitWhiteLabel(ctx, tx, c.ID, app.WhiteLabelSubmission{
			BaseProductID: base.ID, Name: "Blend", LabelR2Key: "",
		}, customerActor(c))
		assert.ErrorIs(t, err, app.ErrWhiteLabelLabelRequired)
	})

	t.Run("unknown base rejected", func(t *testing.T) {
		tx := testutil.NewTestTx(t, testPool)
		baseCoffee(t, tx)
		c := approvedWholesaleCustomer(t, tx)
		_, err := svc.SubmitWhiteLabel(ctx, tx, c.ID, app.WhiteLabelSubmission{
			BaseProductID: uuid.New(), Name: "Blend", LabelR2Key: "white-label/x.png",
		}, customerActor(c))
		assert.ErrorIs(t, err, app.ErrWhiteLabelBaseInvalid)
	})

	t.Run("non-approved customer rejected", func(t *testing.T) {
		tx := testutil.NewTestTx(t, testPool)
		base, _, _ := baseCoffee(t, tx)
		c := testutil.CreateCustomer(t, tx) // retail, not wholesale
		_, err := svc.SubmitWhiteLabel(ctx, tx, c.ID, app.WhiteLabelSubmission{
			BaseProductID: base.ID, Name: "Blend", LabelR2Key: "white-label/x.png",
		}, customerActor(c))
		assert.ErrorIs(t, err, app.ErrWholesaleNotApproved)
	})
}

// A submitted white-label product is invisible while draft, then visible only to
// its owner once published — never to another wholesale customer.
func TestSubmitWhiteLabel_VisibilityScoping(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	ctx := context.Background()
	svc := newWhiteLabelService()
	catalog := newCatalogService()
	wsvc := newWholesaleService()
	pricing := newPricingService()

	base, _, _ := baseCoffee(t, tx)
	owner := approvedWholesaleCustomer(t, tx)
	other := approvedWholesaleCustomer(t, tx)

	product, err := svc.SubmitWhiteLabel(ctx, tx, owner.ID, app.WhiteLabelSubmission{
		BaseProductID: base.ID, Name: "Owner Blend", LabelR2Key: "white-label/o.png",
	}, customerActor(owner))
	require.NoError(t, err)

	inCatalog := func(customerID uuid.UUID) bool {
		products, err := wsvc.QuickOrderCatalog(ctx, tx, customerID, pricing, "USD")
		require.NoError(t, err)
		for _, p := range products {
			if p.ID == product.ID {
				return true
			}
		}
		return false
	}

	// Draft: not orderable by anyone.
	assert.False(t, inCatalog(owner.ID), "draft white-label product must not appear yet")

	// Publish it.
	_, err = catalog.UpdateProductStatus(ctx, tx, product.ID, domain.ProductStatusActive, testutil.TestActor())
	require.NoError(t, err)

	assert.True(t, inCatalog(owner.ID), "owner sees their published white-label product")
	assert.False(t, inCatalog(other.ID), "another wholesale customer must not see it")
}

// A white-label invite token is scoped by purpose: it cannot be redeemed by the
// password-setup/magic-link path, and vice versa.
func TestWhiteLabelInviteToken_PurposeIsolation(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	ctx := context.Background()
	auth := newAuthService()
	customer := testutil.CreateCustomer(t, tx)

	raw, err := auth.CreateWhiteLabelInviteToken(ctx, tx, customer.ID)
	require.NoError(t, err)

	// Wrong path (default purpose) can't redeem an invite token.
	_, _, err = auth.RedeemMagicLink(ctx, tx, raw, nil, nil)
	assert.ErrorIs(t, err, app.ErrMagicLinkExpired)

	// Lookup (peek) works without consuming.
	gotID, err := auth.LookupWhiteLabelInvite(ctx, tx, raw)
	require.NoError(t, err)
	assert.Equal(t, customer.ID, gotID)

	// Correct path redeems once.
	gotID, err = auth.RedeemWhiteLabelInvite(ctx, tx, raw)
	require.NoError(t, err)
	assert.Equal(t, customer.ID, gotID)

	// Single-use: a second redeem fails.
	_, err = auth.RedeemWhiteLabelInvite(ctx, tx, raw)
	assert.ErrorIs(t, err, app.ErrWhiteLabelInviteInvalid)

	// A default-purpose setup token can't be redeemed as a white-label invite.
	setupRaw, err := auth.CreateSetupToken(ctx, tx, customer.ID)
	require.NoError(t, err)
	_, err = auth.RedeemWhiteLabelInvite(ctx, tx, setupRaw)
	assert.ErrorIs(t, err, app.ErrWhiteLabelInviteInvalid)
}

// TestWhiteLabelFilter_SelectsPendingSubmissions covers the admin review queue's
// backing filter: submissions still in draft must be findable without the staff
// notification email, and ordinary catalog products must never leak into it.
func TestWhiteLabelFilter_SelectsPendingSubmissions(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	ctx := context.Background()
	svc := newWhiteLabelService()
	catalog := newCatalogService()

	base, _, _ := baseCoffee(t, tx)
	customer := approvedWholesaleCustomer(t, tx)

	submitted, err := svc.SubmitWhiteLabel(ctx, tx, customer.ID, app.WhiteLabelSubmission{
		BaseProductID: base.ID,
		Name:          "Greaser Blend",
		LabelR2Key:    "white-label/greaser.png",
	}, customerActor(customer))
	require.NoError(t, err)

	yes, no := true, false
	draft := domain.ProductStatusDraft

	// The pending queue: white-label submissions still in draft.
	pending, err := catalog.ListProducts(ctx, tx, store.ProductFilter{
		Status:     &draft,
		WhiteLabel: &yes,
	})
	require.NoError(t, err)
	require.Len(t, pending, 1)
	assert.Equal(t, submitted.ID, pending[0].ID)

	count, err := catalog.CountProducts(ctx, tx, store.ProductFilter{Status: &draft, WhiteLabel: &yes})
	require.NoError(t, err)
	assert.Equal(t, 1, count)

	// The negative arm must keep products whose metadata has no source key at all
	// — every product predating the white-label flow looks like that.
	others, err := catalog.ListProducts(ctx, tx, store.ProductFilter{WhiteLabel: &no})
	require.NoError(t, err)
	ids := make([]uuid.UUID, len(others))
	for i, p := range others {
		ids[i] = p.ID
	}
	assert.Contains(t, ids, base.ID)
	assert.NotContains(t, ids, submitted.ID)

	// Publishing clears it from the queue — that's how "reviewed" is expressed.
	staffID := testutil.CreateStaff(t, tx)
	_, err = catalog.UpdateProductStatus(ctx, tx, submitted.ID, domain.ProductStatusActive, testutil.TestActorFromStaff(staffID))
	require.NoError(t, err)

	count, err = catalog.CountProducts(ctx, tx, store.ProductFilter{Status: &draft, WhiteLabel: &yes})
	require.NoError(t, err)
	assert.Equal(t, 0, count)
}

// namedBaseCoffee builds an active coffee with a single wholesale variant under
// the given SKU, so a test can use more than one base without the bases
// themselves colliding on SKU.
func namedBaseCoffee(t *testing.T, tx pgx.Tx, title, sku string) *domain.Product {
	t.Helper()
	p := testutil.CreateProduct(t, tx,
		testutil.WithProductTitle(title),
		testutil.WithProductStatus(domain.ProductStatusActive))
	v := testutil.CreateVariant(t, tx, p.ID,
		testutil.WithSKU(sku), testutil.WithChannelAvailability(true, true))
	testutil.SetBasePriceForVariant(t, tx, v.ID, 2500, "USD")
	return p
}

func submitLabel(t *testing.T, tx pgx.Tx, svc *app.WhiteLabelService, c *domain.Customer, baseID uuid.UUID, name string) *domain.Product {
	t.Helper()
	p, err := svc.SubmitWhiteLabel(context.Background(), tx, c.ID, app.WhiteLabelSubmission{
		BaseProductID: baseID,
		Name:          name,
		LabelR2Key:    "white-label/label.png",
	}, customerActor(c))
	require.NoError(t, err)
	return p
}

func soleSKU(t *testing.T, tx pgx.Tx, catalog *app.CatalogService, productID uuid.UUID) string {
	t.Helper()
	variants, err := catalog.ListVariants(context.Background(), tx, productID)
	require.NoError(t, err)
	require.Len(t, variants, 1)
	return variants[0].SKU
}

// TestWhiteLabelSKU_NamesTheClient covers the SKU format: staff should be able to
// read a white-label SKU and know whose label it is without a lookup.
func TestWhiteLabelSKU_NamesTheClient(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	svc := newWhiteLabelService()
	catalog := newCatalogService()

	base, wsVariant, _ := baseCoffee(t, tx)
	customer := approvedWholesaleCustomerNamed(t, tx, "Midnight Diner")

	product := submitLabel(t, tx, svc, customer, base.ID, "Midnight Diner Blend")

	// Company name, uppercased and stripped to alphanumerics (capped at 12), then
	// the base variant's SKU verbatim. No random component.
	assert.Equal(t, "WL-MIDNIGHTDINE-"+wsVariant.SKU, soleSKU(t, tx, catalog, product.ID))
}

// TestWhiteLabelSKU_SuffixesOnlyOnClash covers the disambiguator: the invite link
// is reusable, so one client can submit two labels off the same coffee. Those must
// stay unique, but a second label on a *different* coffee must not pick up a
// suffix it doesn't need.
func TestWhiteLabelSKU_SuffixesOnlyOnClash(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	svc := newWhiteLabelService()
	catalog := newCatalogService()

	base, wsVariant, _ := baseCoffee(t, tx)
	other := namedBaseCoffee(t, tx, "Greaser", "GREASE-12OZ")
	customer := approvedWholesaleCustomerNamed(t, tx, "The Bunker")

	first := submitLabel(t, tx, svc, customer, base.ID, "Bunker House")
	assert.Equal(t, "WL-THEBUNKER-"+wsVariant.SKU, soleSKU(t, tx, catalog, first.ID))

	// Same client, same base coffee — would collide, so it takes a suffix.
	second := submitLabel(t, tx, svc, customer, base.ID, "Bunker Dark")
	assert.Equal(t, "WL-THEBUNKER-2-"+wsVariant.SKU, soleSKU(t, tx, catalog, second.ID))

	// Same client, different base coffee — no collision, so no suffix.
	third := submitLabel(t, tx, svc, customer, other.ID, "Bunker Light")
	assert.Equal(t, "WL-THEBUNKER-GREASE-12OZ", soleSKU(t, tx, catalog, third.ID))
}

// TestWhiteLabelSKU_DistinctClientsDistinctSKUs is the whole point of the change:
// two clients basing labels on the same coffee are told apart by the SKU alone.
func TestWhiteLabelSKU_DistinctClientsDistinctSKUs(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	svc := newWhiteLabelService()
	catalog := newCatalogService()

	base, wsVariant, _ := baseCoffee(t, tx)
	bunker := approvedWholesaleCustomerNamed(t, tx, "The Bunker")
	diner := approvedWholesaleCustomerNamed(t, tx, "Midnight Diner")

	a := submitLabel(t, tx, svc, bunker, base.ID, "Bunker House")
	b := submitLabel(t, tx, svc, diner, base.ID, "Diner House")

	assert.Equal(t, "WL-THEBUNKER-"+wsVariant.SKU, soleSKU(t, tx, catalog, a.ID))
	assert.Equal(t, "WL-MIDNIGHTDINE-"+wsVariant.SKU, soleSKU(t, tx, catalog, b.ID))
}

// TestWhiteLabelSKU_FallsBackWhenNoCompany covers a wholesale account with no
// company on file — the SKU still has to be generated, so the token falls back to
// the contact's last name.
func TestWhiteLabelSKU_FallsBackWhenNoCompany(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	svc := newWhiteLabelService()
	catalog := newCatalogService()

	base, wsVariant, _ := baseCoffee(t, tx)
	customer := approvedWholesaleCustomerNamed(t, tx, "")

	product := submitLabel(t, tx, svc, customer, base.ID, "No Company Blend")
	assert.Equal(t, "WL-LABEL-"+wsVariant.SKU, soleSKU(t, tx, catalog, product.ID))
}

func TestSubmitWhiteLabel_ClonesAttributes(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	ctx := context.Background()
	svc := newWhiteLabelService()
	attrs := app.NewAttributeService(store.NewAttributeStore(), audit.NewAuditWriter(), metrics.NewRegistry())

	staffActor := testutil.TestActorFromStaff(testutil.CreateStaff(t, tx))
	base, _, _ := baseCoffee(t, tx)

	// A set with one single-value key and one multi-value key, both filled in on
	// the base coffee.
	set, err := attrs.CreateAttributeSet(ctx, tx, store.CreateAttributeSetParams{
		Name: "Coffee", Slug: "coffee-" + uuid.New().String()[:8],
	}, staffActor)
	require.NoError(t, err)
	roast, err := attrs.CreateAttributeKey(ctx, tx, store.CreateAttributeKeyParams{
		AttributeSetID: set.ID, Name: "Roast level", Slug: "roast-level",
		ValueType: domain.AttributeValueTypeText,
	}, staffActor)
	require.NoError(t, err)
	notes, err := attrs.CreateAttributeKey(ctx, tx, store.CreateAttributeKeyParams{
		AttributeSetID: set.ID, Name: "Tasting notes", Slug: "tasting-notes",
		ValueType: domain.AttributeValueTypeMultiText,
	}, staffActor)
	require.NoError(t, err)
	require.NoError(t, attrs.AssignAttributeSetToProduct(ctx, tx, base.ID, set.ID))
	dark := "Dark"
	require.NoError(t, attrs.SaveProductAttributes(ctx, tx, base.ID, map[uuid.UUID]store.AttributeValueInput{
		roast.ID: {Value: &dark},
		notes.ID: {Values: []string{"cocoa", "molasses"}},
	}, staffActor))

	customer := approvedWholesaleCustomer(t, tx)
	product, err := svc.SubmitWhiteLabel(ctx, tx, customer.ID, app.WhiteLabelSubmission{
		BaseProductID: base.ID,
		Name:          "Midnight Diner Blend",
		LabelR2Key:    "white-label/label.png",
	}, customerActor(customer))
	require.NoError(t, err)

	// The set assignment carries over — without it the admin edit page renders no
	// attribute fields and the values would be invisible.
	sets, err := attrs.ListProductAttributeSets(ctx, tx, product.ID)
	require.NoError(t, err)
	require.Len(t, sets, 1)
	assert.Equal(t, set.ID, sets[0].ID)

	values, err := attrs.ListProductAttributeValues(ctx, tx, product.ID)
	require.NoError(t, err)
	require.Len(t, values, 2)
	byKey := map[uuid.UUID]domain.ProductAttributeValue{}
	for _, v := range values {
		byKey[v.KeyID] = v
	}
	require.NotNil(t, byKey[roast.ID].Value)
	assert.Equal(t, "Dark", *byKey[roast.ID].Value)
	assert.Equal(t, []string{"cocoa", "molasses"}, byKey[notes.ID].Values)
}

// submitLabelFor is the common setup for the base-reassignment tests: a fresh
// wholesale client plus one white-label product off the given base coffee.
func submitLabelFor(t *testing.T, tx pgx.Tx, base *domain.Product, company string) (*domain.Product, *domain.Customer) {
	t.Helper()
	svc := newWhiteLabelService()
	customer := approvedWholesaleCustomerNamed(t, tx, company)
	product, err := svc.SubmitWhiteLabel(context.Background(), tx, customer.ID, app.WhiteLabelSubmission{
		BaseProductID: base.ID,
		Name:          company + " Blend",
		LabelR2Key:    "white-label/label.png",
	}, customerActor(customer))
	require.NoError(t, err)
	return product, customer
}

func TestArchiveBaseCoffee_BlockedByWhiteLabelChildren(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	ctx := context.Background()
	catalog := newCatalogService()
	staffActor := testutil.TestActorFromStaff(testutil.CreateStaff(t, tx))

	base, _, _ := baseCoffee(t, tx)
	child, _ := submitLabelFor(t, tx, base, "Midnight Diner")

	_, err := catalog.UpdateProductStatus(ctx, tx, base.ID, domain.ProductStatusArchived, staffActor)
	require.Error(t, err)
	assert.ErrorIs(t, err, app.ErrProductHasWhiteLabelChildren)
	// The message has to name the label — it is the only place staff can see which
	// products a coffee backs.
	assert.Contains(t, err.Error(), child.Title)

	// The base is untouched: a refused archive must not half-apply.
	unchanged, err := catalog.GetProduct(ctx, tx, base.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.ProductStatusActive, unchanged.Status)

	// Other status transitions are unaffected — only archiving is gated.
	_, err = catalog.UpdateProductStatus(ctx, tx, base.ID, domain.ProductStatusDraft, staffActor)
	require.NoError(t, err)
}

func TestArchiveBaseCoffee_AllowedOnceChildrenReassigned(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	ctx := context.Background()
	svc := newWhiteLabelService()
	catalog := newCatalogService()
	staffActor := testutil.TestActorFromStaff(testutil.CreateStaff(t, tx))

	base, _, _ := baseCoffee(t, tx)
	child, _ := submitLabelFor(t, tx, base, "Midnight Diner")

	// A second coffee to move the label onto.
	replacement := testutil.CreateProduct(t, tx,
		testutil.WithProductTitle("Switchblade"),
		testutil.WithProductStatus(domain.ProductStatusActive))
	rv := testutil.CreateVariant(t, tx, replacement.ID,
		testutil.WithSKU("SWITCH-12OZ"), testutil.WithChannelAvailability(true, true))
	testutil.SetBasePriceForVariant(t, tx, rv.ID, 2600, "USD")

	reassigned, err := svc.ReassignBase(ctx, tx, child.ID, replacement.ID, staffActor)
	require.NoError(t, err)
	assert.Equal(t, replacement.ID.String(), reassigned.Metadata[app.WhiteLabelMetaBaseID])
	// Repointing must not disturb the other stamps or the product's own identity.
	assert.Equal(t, "white_label_onboarding", reassigned.Metadata[app.WhiteLabelMetaSource])
	assert.Equal(t, child.Title, reassigned.Title)
	assert.Equal(t, domain.ProductVisibilityPrivate, reassigned.Visibility)

	// The old base is now free to archive; the new one is not.
	_, err = catalog.UpdateProductStatus(ctx, tx, base.ID, domain.ProductStatusArchived, staffActor)
	require.NoError(t, err)

	_, err = catalog.UpdateProductStatus(ctx, tx, replacement.ID, domain.ProductStatusArchived, staffActor)
	assert.ErrorIs(t, err, app.ErrProductHasWhiteLabelChildren)
}

func TestArchiveBaseCoffee_IgnoresArchivedChildren(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	ctx := context.Background()
	catalog := newCatalogService()
	staffActor := testutil.TestActorFromStaff(testutil.CreateStaff(t, tx))

	base, _, _ := baseCoffee(t, tx)
	child, _ := submitLabelFor(t, tx, base, "Midnight Diner")

	// A retired label doesn't block anything — nobody is going to fill its bag.
	_, err := catalog.UpdateProductStatus(ctx, tx, child.ID, domain.ProductStatusArchived, staffActor)
	require.NoError(t, err)

	_, err = catalog.UpdateProductStatus(ctx, tx, base.ID, domain.ProductStatusArchived, staffActor)
	require.NoError(t, err)
}

func TestReassignBase_Validation(t *testing.T) {
	ctx := context.Background()
	svc := newWhiteLabelService()

	t.Run("not a white-label product", func(t *testing.T) {
		tx := testutil.NewTestTx(t, testPool)
		base, _, _ := baseCoffee(t, tx)
		other := testutil.CreateProduct(t, tx, testutil.WithProductStatus(domain.ProductStatusActive))
		staffActor := testutil.TestActorFromStaff(testutil.CreateStaff(t, tx))

		_, err := svc.ReassignBase(ctx, tx, other.ID, base.ID, staffActor)
		assert.ErrorIs(t, err, app.ErrNotWhiteLabelProduct)
	})

	t.Run("new base must be an allowed choice", func(t *testing.T) {
		tx := testutil.NewTestTx(t, testPool)
		base, _, _ := baseCoffee(t, tx)
		child, _ := submitLabelFor(t, tx, base, "Midnight Diner")
		staffActor := testutil.TestActorFromStaff(testutil.CreateStaff(t, tx))

		// Draft, so it never appears in BaseCoffeeChoices.
		draft := testutil.CreateProduct(t, tx, testutil.WithProductStatus(domain.ProductStatusDraft))
		_, err := svc.ReassignBase(ctx, tx, child.ID, draft.ID, staffActor)
		assert.ErrorIs(t, err, app.ErrWhiteLabelBaseInvalid)
	})

	t.Run("cannot base a label on itself", func(t *testing.T) {
		tx := testutil.NewTestTx(t, testPool)
		base, _, _ := baseCoffee(t, tx)
		child, _ := submitLabelFor(t, tx, base, "Midnight Diner")
		staffActor := testutil.TestActorFromStaff(testutil.CreateStaff(t, tx))

		_, err := svc.ReassignBase(ctx, tx, child.ID, child.ID, staffActor)
		assert.ErrorIs(t, err, app.ErrWhiteLabelBaseInvalid)
	})
}

func TestDeleteBaseCoffee_BlockedByWhiteLabelChildren(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	ctx := context.Background()
	catalog := newCatalogService()
	staffActor := testutil.TestActorFromStaff(testutil.CreateStaff(t, tx))

	base, _, _ := baseCoffee(t, tx)
	child, _ := submitLabelFor(t, tx, base, "Midnight Diner")

	// Deleting is worse than archiving — the base ID stops resolving entirely — so
	// it is gated the same way.
	err := catalog.DeleteProduct(ctx, tx, base.ID, staffActor)
	require.Error(t, err)
	assert.ErrorIs(t, err, app.ErrProductHasWhiteLabelChildren)
	assert.Contains(t, err.Error(), child.Title)

	still, err := catalog.GetProduct(ctx, tx, base.ID)
	require.NoError(t, err)
	assert.Equal(t, base.ID, still.ID)
}
