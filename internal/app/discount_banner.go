package app

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/platform/audit"
)

// SetBannerText sets the line that promotes a discount in the storefront's
// offer strip. Empty stops promoting it. Whitespace is trimmed and runs of it
// collapsed, so a pasted line break cannot split the strip.
func (s *DiscountService) SetBannerText(ctx context.Context, tx pgx.Tx, id uuid.UUID, text string, actor Actor) error {
	text = strings.Join(strings.Fields(text), " ")
	if utf8.RuneCountInString(text) > domain.MaxBannerTextLen {
		return ErrBannerTextTooLong
	}
	d, err := s.GetDiscount(ctx, tx, id)
	if err != nil {
		return err
	}
	if d.BannerText == text {
		return nil
	}
	if err := s.discounts.SetBannerText(ctx, tx, id, text); err != nil {
		return err
	}
	if err := s.audit.Record(ctx, tx, audit.AuditEntry{
		ActorType:    actor.Type,
		ActorID:      actor.ID,
		ActorName:    actor.Name,
		Action:       audit.AuditDiscountBanner,
		ResourceType: "discount",
		ResourceID:   id,
		Metadata:     map[string]any{"from": d.BannerText, "to": text},
	}); err != nil {
		return fmt.Errorf("audit discount banner: %w", err)
	}
	return nil
}

// StorefrontBanners returns the offer strip for one shopper: each live
// discount with banner copy that this shopper could use. customer is the
// signed-in customer, nil for a guest.
//
// The audience follows the discount's own eligibility rules, so the strip does
// not advertise a code to someone it is not meant for. (It cannot know about
// per-order limits — a single-use code already spent, a once-per-customer
// offer this shopper has used, a minimum order — and checkout still refuses
// those.)
//   - the referral offer goes to active members, who share it, not to the
//     friends it discounts — they arrive with a member's code;
//   - subscribers-only goes to signed-in active members;
//   - new-subscribers-only goes to everyone except a signed-in customer who has
//     ever subscribed;
//   - anyone goes to everyone.
//
// Wholesale accounts see nothing: storefront discounts are retail offers.
func (s *DiscountService) StorefrontBanners(ctx context.Context, tx pgx.Tx, customer *domain.Customer) ([]domain.PromoBanner, error) {
	if customer != nil && customer.AccountType == domain.AccountTypeWholesale {
		return nil, nil
	}
	discounts, err := s.discounts.ListBannerDiscounts(ctx, tx)
	if err != nil || len(discounts) == 0 {
		return nil, err
	}

	// Asked at most once each, and only when a discount needs the answer.
	var member, everSubscribed *bool
	isMember := func() (bool, error) {
		if customer == nil {
			return false, nil
		}
		if member == nil {
			ok, err := s.discounts.CustomerIsSubscriber(ctx, tx, customer.ID)
			if err != nil {
				return false, err
			}
			member = &ok
		}
		return *member, nil
	}
	hasSubscribed := func() (bool, error) {
		if customer == nil {
			return false, nil
		}
		if everSubscribed == nil {
			ok, err := s.discounts.CustomerHasEverSubscribed(ctx, tx, customer.ID)
			if err != nil {
				return false, err
			}
			everSubscribed = &ok
		}
		return *everSubscribed, nil
	}

	var out []domain.PromoBanner
	for _, bd := range discounts {
		d := bd.Discount
		var show bool
		switch {
		case d.IsReferral, d.Eligibility == domain.DiscountEligibilitySubscribers:
			show, err = isMember()
		case d.Eligibility == domain.DiscountEligibilityNewSubscribers:
			var subscribed bool
			subscribed, err = hasSubscribed()
			show = !subscribed
		default:
			show = true
		}
		if err != nil {
			return nil, err
		}
		if show {
			out = append(out, domain.PromoBanner{Text: d.BannerText, Href: bannerHref(d, bd.PromoCode)})
		}
	}
	return out, nil
}

// bannerHref is where a banner line leads: a member's referral code is on
// their subscriptions page, a signup code is used from the Daily Grind page,
// and anything else is spent in the shop. A link with a code carries it as a
// promo link (web/promo_code.go), so checkout applies it; the referral offer
// never does, as each member shares their own.
func bannerHref(d domain.Discount, code string) string {
	var path string
	switch {
	case d.IsReferral:
		return "/account/subscriptions"
	case d.AppliesTo == domain.DiscountAppliesToSubscriptionSignup:
		path = "/subscriptions"
	default:
		path = "/catalog"
	}
	if code == "" || domain.NormalizePromoCode(code) != code {
		return path
	}
	return path + "?promo=" + url.QueryEscape(code)
}
