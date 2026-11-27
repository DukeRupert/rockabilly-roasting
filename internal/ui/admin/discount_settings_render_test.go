package admin

import (
	"bytes"
	"context"
	"regexp"
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
