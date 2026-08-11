package app

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/emailtemplates"
	"github.com/dukerupert/hiri/internal/platform/audit"
	"github.com/dukerupert/hiri/internal/platform/email"
	"github.com/dukerupert/hiri/internal/platform/metrics"
	"github.com/dukerupert/hiri/internal/store"
)

// White-label products are stamped with these metadata keys so staff and the
// admin UI can recognise an onboarding submission and trace it back to its base
// coffee and the customer who owns it. The values live in domain — store filters
// on the stamp and the admin UI reads it, and neither may import app.
const (
	WhiteLabelMetaSource     = domain.ProductMetaSource
	WhiteLabelSourceValue    = domain.ProductSourceWhiteLabel
	WhiteLabelMetaBaseID     = domain.ProductMetaWhiteLabelBaseID
	WhiteLabelMetaCustomerID = domain.ProductMetaWhiteLabelCustomer
)

// WhiteLabelService owns the wholesale white-label onboarding flow: minting
// invite links, listing the coffees a client may base a label on, and turning a
// submission into a draft, private product cloned from the chosen coffee.
type WhiteLabelService struct {
	catalog      *CatalogService
	pricing      *PricingService
	catalogStore *store.CatalogStore
	customers    *store.CustomerStore
	audit        *audit.AuditWriter
	metrics      *metrics.Registry
	auth         *AuthService // populated via WithEmail; mints invite tokens
	email        EmailEnv     // populated via WithEmail; required for Send* methods
}

// NewWhiteLabelService creates a new WhiteLabelService.
func NewWhiteLabelService(
	catalog *CatalogService,
	pricing *PricingService,
	catalogStore *store.CatalogStore,
	customers *store.CustomerStore,
	audit *audit.AuditWriter,
	metrics *metrics.Registry,
) *WhiteLabelService {
	return &WhiteLabelService{
		catalog:      catalog,
		pricing:      pricing,
		catalogStore: catalogStore,
		customers:    customers,
		audit:        audit,
		metrics:      metrics,
	}
}

// WithEmail wires the email environment and auth service needed by the Send*
// methods and invite-token minting. Mirrors WholesaleService.WithEmail.
func (s *WhiteLabelService) WithEmail(env EmailEnv, auth *AuthService) *WhiteLabelService {
	s.email = env
	s.auth = auth
	return s
}

// WhiteLabelBaseChoice is one coffee a client may base a white-label product on.
type WhiteLabelBaseChoice struct {
	ProductID uuid.UUID
	Title     string
}

// BaseCoffeeChoices returns the active coffees a wholesale client may base a
// white-label label on: products visible to the wholesale tier (public or
// wholesale visibility — never another client's private product) that have at
// least one orderable wholesale variant.
func (s *WhiteLabelService) BaseCoffeeChoices(ctx context.Context, tx pgx.Tx) ([]WhiteLabelBaseChoice, error) {
	products, err := s.catalog.ListProducts(ctx, tx, store.ProductFilter{
		Status: ptrTo(domain.ProductStatusActive),
		// A wholesale viewer with no customer/group identity sees exactly the
		// public + wholesale tiers — restricted and private are excluded.
		Visibility: &store.VisibilityContext{IsWholesale: true},
	})
	if err != nil {
		return nil, fmt.Errorf("list base coffees: %w", err)
	}

	var choices []WhiteLabelBaseChoice
	for _, p := range products {
		if p.Visibility == domain.ProductVisibilityPrivate {
			continue // defence in depth; the filter already excludes these
		}
		variants, err := s.catalog.ListVariants(ctx, tx, p.ID)
		if err != nil {
			return nil, fmt.Errorf("list variants for base coffee %s: %w", p.ID, err)
		}
		if !hasOrderableWholesaleVariant(variants) {
			continue
		}
		choices = append(choices, WhiteLabelBaseChoice{ProductID: p.ID, Title: p.Title})
	}
	return choices, nil
}

func hasOrderableWholesaleVariant(variants []domain.Variant) bool {
	for _, v := range variants {
		if v.ArchivedAt == nil && v.WholesaleAvailable {
			return true
		}
	}
	return false
}

// WhiteLabelSubmission is a client's onboarding form input.
type WhiteLabelSubmission struct {
	BaseProductID uuid.UUID
	Name          string
	LabelR2Key    string // R2 object key of the uploaded label image
}

// SubmitWhiteLabel turns a client's submission into a draft, private product
// cloned from the chosen base coffee and granted only to that client. It runs in
// the caller's transaction; the caller is responsible for redeeming the invite
// token and enqueueing the staff-notification job in the same tx so the whole
// submission is atomic.
//
// The product lands in draft status — staff review the name, label art, and
// pricing before publishing it to active.
func (s *WhiteLabelService) SubmitWhiteLabel(ctx context.Context, tx pgx.Tx, customerID uuid.UUID, sub WhiteLabelSubmission, actor Actor) (*domain.Product, error) {
	name := strings.TrimSpace(sub.Name)
	if name == "" {
		return nil, ErrWhiteLabelNameRequired
	}
	if strings.TrimSpace(sub.LabelR2Key) == "" {
		return nil, ErrWhiteLabelLabelRequired
	}

	customer, err := s.customers.GetByID(ctx, tx, customerID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrCustomerNotFound
		}
		return nil, fmt.Errorf("get customer: %w", err)
	}
	if !customer.IsApprovedWholesale() {
		return nil, ErrWholesaleNotApproved
	}

	// Re-validate the chosen base server-side — never trust the posted ID.
	base, err := s.validateBase(ctx, tx, sub.BaseProductID)
	if err != nil {
		return nil, err
	}

	slug, err := s.uniqueSlug(ctx, tx, name)
	if err != nil {
		return nil, err
	}

	product, err := s.catalog.CreateProduct(ctx, tx, store.CreateProductParams{
		Slug:          slug,
		Title:         name,
		Description:   base.Description,
		Status:        domain.ProductStatusDraft,
		ProductTypeID: base.ProductTypeID,
		TaxonID:       base.TaxonID,
		Metadata: map[string]any{
			WhiteLabelMetaSource:     WhiteLabelSourceValue,
			WhiteLabelMetaBaseID:     base.ID.String(),
			WhiteLabelMetaCustomerID: customerID.String(),
		},
	}, actor)
	if err != nil {
		return nil, fmt.Errorf("create white-label product: %w", err)
	}

	if err := s.cloneVariants(ctx, tx, base, product.ID, actor); err != nil {
		return nil, err
	}

	product, err = s.catalog.UpdateProductVisibility(ctx, tx, product.ID, domain.ProductVisibilityPrivate, actor)
	if err != nil {
		return nil, fmt.Errorf("set white-label visibility: %w", err)
	}
	if err := s.catalog.SetProductCustomerAccess(ctx, tx, product.ID, []uuid.UUID{customerID}, actor); err != nil {
		return nil, fmt.Errorf("grant white-label access: %w", err)
	}

	if _, err := s.catalog.CreateProductMedia(ctx, tx, store.CreateProductMediaParams{
		ProductID: product.ID,
		R2Key:     sub.LabelR2Key,
		AltText:   name + " label",
		Position:  0,
		MediaType: domain.MediaTypeImage,
	}, actor); err != nil {
		return nil, fmt.Errorf("attach white-label image: %w", err)
	}

	if err := s.audit.Record(ctx, tx, audit.AuditEntry{
		ActorType:    actor.Type,
		ActorID:      actor.ID,
		ActorName:    actor.Name,
		Action:       audit.AuditWhiteLabelSubmitted,
		ResourceType: "product",
		ResourceID:   product.ID,
		After:        product,
		Metadata: map[string]any{
			"base_product_id": base.ID.String(),
			"customer_id":     customerID.String(),
		},
	}); err != nil {
		return nil, fmt.Errorf("audit white-label submitted: %w", err)
	}

	return product, nil
}

// validateBase confirms the posted base product is one of the allowed choices and
// returns it.
func (s *WhiteLabelService) validateBase(ctx context.Context, tx pgx.Tx, baseProductID uuid.UUID) (*domain.Product, error) {
	choices, err := s.BaseCoffeeChoices(ctx, tx)
	if err != nil {
		return nil, err
	}
	allowed := false
	for _, c := range choices {
		if c.ProductID == baseProductID {
			allowed = true
			break
		}
	}
	if !allowed {
		return nil, ErrWhiteLabelBaseInvalid
	}
	base, err := s.catalog.GetProduct(ctx, tx, baseProductID)
	if err != nil {
		return nil, fmt.Errorf("get base coffee: %w", err)
	}
	return base, nil
}

// cloneVariants copies the base coffee's orderable wholesale variants (sizes,
// weight, MOQ) and their base prices onto the new product. Cloned variants are
// wholesale-only — the white-label product is never sold retail. New SKUs are
// prefixed with the new product's short ID to stay unique across submissions.
func (s *WhiteLabelService) cloneVariants(ctx context.Context, tx pgx.Tx, base *domain.Product, productID uuid.UUID, actor Actor) error {
	baseVariants, err := s.catalog.ListVariants(ctx, tx, base.ID)
	if err != nil {
		return fmt.Errorf("list base variants: %w", err)
	}
	basePrices, err := s.pricing.ListBasePricesByProduct(ctx, tx, base.ID, "USD")
	if err != nil {
		return fmt.Errorf("list base prices: %w", err)
	}

	skuPrefix := "WL-" + shortID(productID)
	for _, v := range baseVariants {
		if v.ArchivedAt != nil || !v.WholesaleAvailable {
			continue
		}
		newVariant, err := s.catalog.CreateVariant(ctx, tx, store.CreateVariantParams{
			ProductID:          productID,
			SKU:                fmt.Sprintf("%s-%s", skuPrefix, v.SKU),
			Position:           v.Position,
			IsDefault:          v.IsDefault,
			WeightGrams:        v.WeightGrams,
			RetailAvailable:    false,
			WholesaleAvailable: true,
		}, actor)
		if err != nil {
			return fmt.Errorf("clone variant %s: %w", v.SKU, err)
		}

		if v.WholesaleMinQty != nil || v.WholesaleMultiple != nil {
			if _, err := s.catalogStore.UpdateVariantWholesale(ctx, tx, newVariant.ID, v.WholesaleMinQty, v.WholesaleMultiple); err != nil {
				return fmt.Errorf("clone variant MOQ %s: %w", v.SKU, err)
			}
		}

		if cents, ok := basePrices[v.ID]; ok {
			if _, err := s.pricing.SetBasePrice(ctx, tx, newVariant.ID, cents, "USD"); err != nil {
				return fmt.Errorf("clone variant price %s: %w", v.SKU, err)
			}
		}
	}
	return nil
}

// uniqueSlug derives a URL slug from the product name, appending a numeric suffix
// until it is free. Two clients can pick the same name, so collisions are normal.
func (s *WhiteLabelService) uniqueSlug(ctx context.Context, tx pgx.Tx, name string) (string, error) {
	base := slugifyName(name)
	if base == "" {
		base = "white-label"
	}
	candidate := base
	for i := 2; i < 1000; i++ {
		_, err := s.catalog.GetProductBySlug(ctx, tx, candidate)
		if errors.Is(err, ErrProductNotFound) {
			return candidate, nil
		}
		if err != nil {
			return "", fmt.Errorf("check slug uniqueness: %w", err)
		}
		candidate = fmt.Sprintf("%s-%d", base, i)
	}
	return "", fmt.Errorf("could not find a free slug for %q", name)
}

// shortID returns the first 8 hex chars of a UUID — enough to disambiguate SKUs.
func shortID(id uuid.UUID) string {
	return strings.ReplaceAll(id.String(), "-", "")[:8]
}

// slugifyName lowercases and hyphenates a name into a URL-safe slug. Kept local to
// the app package (web has its own slugify for admin forms).
func slugifyName(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	prevHyphen := false
	for _, r := range s {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
			prevHyphen = false
		case r == ' ' || r == '-' || r == '_':
			if !prevHyphen && b.Len() > 0 {
				b.WriteRune('-')
				prevHyphen = true
			}
		}
	}
	return strings.Trim(b.String(), "-")
}

// SendInviteEmail mints a single-use white-label invite token for an approved
// wholesale customer and emails them the onboarding link. Token creation is in
// tx 1, the send is outside any tx, the audit is in tx 2 — the same shape as
// WholesaleService.SendApprovalEmail.
func (s *WhiteLabelService) SendInviteEmail(ctx context.Context, pool *pgxpool.Pool, customerID uuid.UUID) error {
	var (
		customer *domain.Customer
		rawToken string
	)
	if err := store.Tx(ctx, pool, func(tx pgx.Tx) error {
		c, err := s.customers.GetByID(ctx, tx, customerID)
		if err != nil {
			return fmt.Errorf("get customer %s: %w", customerID, err)
		}
		if !c.IsApprovedWholesale() {
			return ErrWholesaleNotApproved
		}
		customer = c
		token, err := s.auth.CreateWhiteLabelInviteToken(ctx, tx, customerID)
		if err != nil {
			return fmt.Errorf("create white-label invite token: %w", err)
		}
		rawToken = token
		return nil
	}); err != nil {
		return err
	}

	companyName := "there"
	if customer.CompanyName != nil {
		companyName = *customer.CompanyName
	}

	inviteURL := fmt.Sprintf("%s/wholesale/white-label?token=%s", s.email.BaseURL, rawToken)
	html, text, err := s.email.Renderer.Render("white_label_invite", emailtemplates.WhiteLabelInviteData{
		CompanyName: companyName,
		InviteURL:   inviteURL,
		StoreName:   s.email.StoreName,
		StoreURL:    s.email.BaseURL,
	})
	if err != nil {
		s.metrics.EmailsSent.WithLabelValues("white_label_invite", "failed").Inc()
		return fmt.Errorf("render white-label invite template: %w", err)
	}

	if _, err := s.email.Mailer.Send(ctx, email.Message{
		From:    s.email.FromAddr,
		To:      customer.Email,
		Subject: "Set up your custom-label coffee",
		HTML:    html,
		Text:    text,
		Tag:     "white-label-invite",
	}); err != nil {
		s.metrics.EmailsSent.WithLabelValues("white_label_invite", "failed").Inc()
		return fmt.Errorf("send white-label invite email: %w", err)
	}

	if err := store.Tx(ctx, pool, func(tx pgx.Tx) error {
		return s.audit.Record(ctx, tx, audit.AuditEntry{
			ActorType:    domain.AuditActorTypeSystem,
			ActorName:    "white_label_invite_worker",
			Action:       audit.AuditEmailWhiteLabelInvite,
			ResourceType: "customer",
			ResourceID:   customer.ID,
			Metadata:     map[string]any{"company": companyName},
		})
	}); err != nil {
		return fmt.Errorf("audit white-label invite email: %w", err)
	}

	s.metrics.EmailsSent.WithLabelValues("white_label_invite", "sent").Inc()
	return nil
}

// SendSubmissionNotice emails staff when a client submits a white-label product,
// linking to the draft for review. Recipient is EmailEnv.StaffEmail.
func (s *WhiteLabelService) SendSubmissionNotice(ctx context.Context, pool *pgxpool.Pool, productID uuid.UUID) error {
	var (
		product  *domain.Product
		customer *domain.Customer
		baseName string
	)
	if err := store.Tx(ctx, pool, func(tx pgx.Tx) error {
		p, err := s.catalog.GetProduct(ctx, tx, productID)
		if err != nil {
			return fmt.Errorf("get product %s: %w", productID, err)
		}
		product = p

		if cid, ok := metaUUID(p.Metadata, WhiteLabelMetaCustomerID); ok {
			if c, err := s.customers.GetByID(ctx, tx, cid); err == nil {
				customer = c
			}
		}
		if bid, ok := metaUUID(p.Metadata, WhiteLabelMetaBaseID); ok {
			if b, err := s.catalog.GetProduct(ctx, tx, bid); err == nil {
				baseName = b.Title
			}
		}
		return nil
	}); err != nil {
		return err
	}

	companyName := "A wholesale client"
	customerEmail := ""
	if customer != nil {
		customerEmail = customer.Email
		if customer.CompanyName != nil {
			companyName = *customer.CompanyName
		}
	}

	reviewURL := fmt.Sprintf("%s/admin/catalog/%s", s.email.BaseURL, product.ID)
	html, text, err := s.email.Renderer.Render("white_label_submitted", emailtemplates.WhiteLabelSubmittedData{
		CompanyName:   companyName,
		CustomerEmail: customerEmail,
		ProductName:   product.Title,
		BaseCoffee:    baseName,
		ReviewURL:     reviewURL,
		StoreName:     s.email.StoreName,
	})
	if err != nil {
		s.metrics.EmailsSent.WithLabelValues("white_label_submitted", "failed").Inc()
		return fmt.Errorf("render white-label submitted template: %w", err)
	}

	if _, err := s.email.Mailer.Send(ctx, email.Message{
		From:    s.email.FromAddr,
		To:      s.email.StaffEmail,
		Subject: fmt.Sprintf("White-label submission: %s (%s)", product.Title, companyName),
		HTML:    html,
		Text:    text,
		Tag:     "white-label-submitted",
	}); err != nil {
		s.metrics.EmailsSent.WithLabelValues("white_label_submitted", "failed").Inc()
		return fmt.Errorf("send white-label submitted email: %w", err)
	}

	if err := store.Tx(ctx, pool, func(tx pgx.Tx) error {
		return s.audit.Record(ctx, tx, audit.AuditEntry{
			ActorType:    domain.AuditActorTypeSystem,
			ActorName:    "white_label_submitted_worker",
			Action:       audit.AuditEmailWhiteLabelSubmitted,
			ResourceType: "product",
			ResourceID:   product.ID,
			Metadata:     map[string]any{"company": companyName},
		})
	}); err != nil {
		return fmt.Errorf("audit white-label submitted email: %w", err)
	}

	s.metrics.EmailsSent.WithLabelValues("white_label_submitted", "sent").Inc()
	return nil
}

// metaUUID reads a UUID stored as a string in a product's metadata map.
func metaUUID(meta map[string]any, key string) (uuid.UUID, bool) {
	raw, ok := meta[key]
	if !ok {
		return uuid.Nil, false
	}
	s, ok := raw.(string)
	if !ok {
		return uuid.Nil, false
	}
	id, err := uuid.Parse(s)
	if err != nil {
		return uuid.Nil, false
	}
	return id, true
}
