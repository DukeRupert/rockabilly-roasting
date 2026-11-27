package admin

import (
	"bytes"
	"context"
	"html"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/dukerupert/hiri/internal/domain"
)

func welcomeDiscount() domain.Discount {
	return domain.Discount{
		ID: uuid.New(), Name: "Welcome 50", Type: domain.DiscountTypePercentage, Value: 50, Active: true,
		UsageLimit:  domain.CouponUsageOncePerCustomer,
		Eligibility: domain.DiscountEligibilityNewSubscribers,
		AppliesTo:   domain.DiscountAppliesToSubscriptionSignup,
		CreatedAt:   time.Now(),
	}
}

// The edit form always posts "Who can use it" and "Order type", so whatever it
// preselects is what a save writes back. Preselecting the wrong option would
// quietly turn WELCOME50 into a code for anyone at cart checkout the next time
// anyone fixed a typo in its name.
func TestDiscountEditForm_PreselectsTheSavedSettings(t *testing.T) {
	var b bytes.Buffer
	d := welcomeDiscount()
	if err := DiscountShowContent(DiscountShowProps{Discount: d, MerchantTZ: time.UTC}).Render(context.Background(), &b); err != nil {
		t.Fatal(err)
	}
	html := b.String()

	selected := func(name string) []string {
		sel := regexp.MustCompile(`(?s)<select[^>]*name="` + name + `".*?</select>`).FindString(html)
		var out []string
		for _, m := range regexp.MustCompile(`<option value="([^"]+)" selected`).FindAllStringSubmatch(sel, -1) {
			out = append(out, m[1])
		}
		return out
	}
	if got := selected("eligibility"); len(got) != 1 || got[0] != "new_subscribers" {
		t.Errorf("eligibility preselected %v, want [new_subscribers]", got)
	}
	if got := selected("applies_to"); len(got) != 1 || got[0] != "subscription_signup" {
		t.Errorf("applies_to preselected %v, want [subscription_signup]", got)
	}
}

// The list says which discounts are restricted; the common case (one-time,
// anyone) adds nothing.
func TestDiscountList_LabelsTheSettings(t *testing.T) {
	var b bytes.Buffer
	d := welcomeDiscount()
	props := DiscountListProps{Discounts: []domain.Discount{d}, Codes: map[uuid.UUID][]domain.CouponCode{}, Redemptions: map[uuid.UUID]int{}, MerchantTZ: time.UTC, Page: 1}
	if err := DiscountListContent(props).Render(context.Background(), &b); err != nil {
		t.Fatal(err)
	}
	row := regexp.MustCompile(`(?s)<tbody.*</tbody>`).FindString(b.String())
	for _, want := range []string{"New subscribers only", "First subscription order", "Once per customer"} {
		if !bytes.Contains([]byte(row), []byte(want)) {
			t.Errorf("list row is missing %q", want)
		}
	}
}

// The same guard for a GIFT20-shaped discount: members only, one-time orders.
// It is live, and a save that dropped "subscribers" would open it to anyone.
func TestDiscountEditForm_PreselectsMembersOnly(t *testing.T) {
	var b bytes.Buffer
	d := welcomeDiscount()
	d.Eligibility = domain.DiscountEligibilitySubscribers
	d.AppliesTo = domain.DiscountAppliesToOneTime
	if err := DiscountShowContent(DiscountShowProps{Discount: d, MerchantTZ: time.UTC}).Render(context.Background(), &b); err != nil {
		t.Fatal(err)
	}
	sel := regexp.MustCompile(`(?s)<select[^>]*name="eligibility".*?</select>`).FindString(b.String())
	got := regexp.MustCompile(`<option value="([^"]+)" selected`).FindAllStringSubmatch(sel, -1)
	if len(got) != 1 || got[0][1] != "subscribers" {
		t.Errorf("eligibility preselected %v, want subscribers", got)
	}
}

func TestDiscountList_LabelsMembersOnly(t *testing.T) {
	var b bytes.Buffer
	d := welcomeDiscount()
	d.Eligibility = domain.DiscountEligibilitySubscribers
	props := DiscountListProps{Discounts: []domain.Discount{d}, Codes: map[uuid.UUID][]domain.CouponCode{}, Redemptions: map[uuid.UUID]int{}, MerchantTZ: time.UTC, Page: 1}
	if err := DiscountListContent(props).Render(context.Background(), &b); err != nil {
		t.Fatal(err)
	}
	row := regexp.MustCompile(`(?s)<tbody.*</tbody>`).FindString(b.String())
	if !strings.Contains(row, "Daily Grind members only") {
		t.Error(`list row is missing "Daily Grind members only"`)
	}
}

// Staff are warned, as they type, that a 100%-off code can make an order
// nobody can pay for. The showing and hiding is Alpine in the browser; what
// can be held here is that both forms carry the warning, bound to the shared
// condition, and that the edit form starts from the saved type and value so
// an existing 100% code shows it on load.
func TestDiscountForms_WarnAboutFullDiscount(t *testing.T) {
	var create, editPage bytes.Buffer
	if err := DiscountListContent(DiscountListProps{MerchantTZ: time.UTC, Page: 1}).Render(context.Background(), &create); err != nil {
		t.Fatal(err)
	}
	full := welcomeDiscount()
	full.Value = 100
	if err := DiscountShowContent(DiscountShowProps{Discount: full, MerchantTZ: time.UTC}).Render(context.Background(), &editPage); err != nil {
		t.Fatal(err)
	}
	for name, page := range map[string]string{"create": create.String(), "edit": editPage.String()} {
		if !strings.Contains(page, "100% off makes these orders free.") {
			t.Errorf("%s form has no full-discount warning", name)
		}
		if !strings.Contains(page, `x-show="`+html.EscapeString(fullDiscountWarningShow)+`"`) {
			t.Errorf("%s form's warning is not bound to the shared condition", name)
		}
		for _, model := range []string{`x-model="discountType"`, `x-model="discountValue"`} {
			if !strings.Contains(page, model) {
				t.Errorf("%s form is missing %s", name, model)
			}
		}
	}
	if !strings.Contains(editPage.String(), "discountType: &#39;percentage&#39;, discountValue: &#39;100&#39;") {
		t.Error("edit form does not start from the saved type and value")
	}
}

// The referral panel says what a discount lacks, the same rules
// SetReferralOffer applies, and its confirmation names the code that stops
// working. The edit form fixes the offer's order type.
func TestDiscountReferralPanel(t *testing.T) {
	render := func(d domain.Discount, codes []domain.CouponCode) string {
		var b bytes.Buffer
		if err := DiscountShowContent(DiscountShowProps{Discount: d, Codes: codes, MerchantTZ: time.UTC}).Render(context.Background(), &b); err != nil {
			t.Fatal(err)
		}
		return b.String()
	}

	cart := welcomeDiscount()
	cart.UsageLimit = domain.CouponUsageSingleUse
	cart.AppliesTo = domain.DiscountAppliesToOneTime
	page := render(cart, nil)
	for _, want := range []string{"Once per customer (set when created)", "For the first subscription order (Order type)"} {
		if !strings.Contains(page, want) {
			t.Errorf("panel does not list %q", want)
		}
	}
	if strings.Contains(page, "Use as the referral offer</button>") {
		t.Error("a discount that fails the rules is offered the button")
	}

	ok := welcomeDiscount()
	page = render(ok, []domain.CouponCode{{Code: "WELCOME50"}})
	if !strings.Contains(page, "Use as the referral offer") || !strings.Contains(page, "WELCOME50") {
		t.Error("an eligible discount's confirmation does not name its own code")
	}

	offer := welcomeDiscount()
	offer.IsReferral = true
	page = render(offer, nil)
	if strings.Contains(page, `name="applies_to"`) && !strings.Contains(page, `type="hidden" name="applies_to" value="subscription_signup"`) {
		t.Error("the offer's order type is editable")
	}
	if !strings.Contains(page, "Fixed while this is the referral offer.") {
		t.Error("the edit form does not say why the order type is fixed")
	}
}

// The banner card posts its saved line back on every save, so it must show
// what is saved, and say who will see it before staff publish to the wrong
// shoppers.
func TestDiscountBannerCard(t *testing.T) {
	render := func(d domain.Discount) string {
		var b bytes.Buffer
		if err := DiscountShowContent(DiscountShowProps{Discount: d, MerchantTZ: time.UTC}).Render(context.Background(), &b); err != nil {
			t.Fatal(err)
		}
		return html.UnescapeString(b.String())
	}

	d := welcomeDiscount()
	d.BannerText = `50% off your first box with "WELCOME50"`
	page := render(d)
	form := regexp.MustCompile(`(?s)<form[^>]*action="/admin/discounts/` + d.ID.String() + `/banner".*?</form>`).FindString(page)
	if form == "" {
		t.Fatal("no banner form")
	}
	if !strings.Contains(form, `name="banner_text"`) || !strings.Contains(form, `value="`+d.BannerText+`"`) {
		t.Errorf("the field does not hold the saved line: %s", form)
	}
	if !strings.Contains(form, `maxlength="120"`) {
		t.Error("the field does not stop at the column's limit")
	}
	if !strings.Contains(form, "except signed-in customers who have subscribed before") {
		t.Error("a new-subscriber offer does not say who is left out")
	}

	d.Eligibility = domain.DiscountEligibilitySubscribers
	d.AppliesTo = domain.DiscountAppliesToOneTime
	if !strings.Contains(render(d), "Seen by signed-in Daily Grind members only") {
		t.Error("a members-only offer does not say it is members only")
	}
}

// The card's "linking to" must match app.bannerHref for every setup staff can
// make: who sees it follows eligibility, where it links follows order type,
// and the two are chosen independently on the form.
func TestDiscountBannerAudience_EverySetup(t *testing.T) {
	for _, tc := range []struct {
		elig    domain.DiscountEligibility
		applies domain.DiscountAppliesTo
		want    string
	}{
		{domain.DiscountEligibilityAnyone, domain.DiscountAppliesToOneTime, "Seen by everyone, linking to the shop."},
		{domain.DiscountEligibilityAnyone, domain.DiscountAppliesToSubscriptionSignup, "Seen by everyone, linking to the Daily Grind page."},
		{domain.DiscountEligibilitySubscribers, domain.DiscountAppliesToOneTime, "Seen by signed-in Daily Grind members only, linking to the shop."},
		{domain.DiscountEligibilitySubscribers, domain.DiscountAppliesToSubscriptionSignup, "Seen by signed-in Daily Grind members only, linking to the Daily Grind page."},
		{domain.DiscountEligibilityNewSubscribers, domain.DiscountAppliesToOneTime, "Seen by everyone except signed-in customers who have subscribed before, linking to the shop."},
		{domain.DiscountEligibilityNewSubscribers, domain.DiscountAppliesToSubscriptionSignup, "Seen by everyone except signed-in customers who have subscribed before, linking to the Daily Grind page."},
	} {
		got := discountBannerAudience(domain.Discount{Eligibility: tc.elig, AppliesTo: tc.applies})
		if !strings.HasPrefix(got, tc.want) || !strings.Contains(got, "Include the code") {
			t.Errorf("%s + %s: got %q, want it to start %q and ask for the code", tc.elig, tc.applies, got, tc.want)
		}
	}

	ref := discountBannerAudience(domain.Discount{IsReferral: true, Eligibility: domain.DiscountEligibilityNewSubscribers, AppliesTo: domain.DiscountAppliesToSubscriptionSignup})
	if !strings.Contains(ref, "subscriptions page") || !strings.Contains(ref, "Don't put a code") || strings.Contains(ref, "Include the code") {
		t.Errorf("referral offer: %q", ref)
	}
}
