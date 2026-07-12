package web

import (
	"net/http"

	"github.com/dukerupert/hiri/internal/ui/storefront"
)

// handleQuizPage renders the "What's Your Roast?" coffee personality quiz.
// The quiz itself scores client-side; the handler's job is loading the live
// subscribable catalog so each archetype recommends a real coffee.
func (d *Deps) handleQuizPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	cards, _, err := d.loadSubscribableCards(ctx)
	if err != nil {
		Error(w, r, err)
		return
	}

	props := storefront.QuizProps{
		Cards:        cards,
		CartCount:    d.cartItemCountFromCookie(r),
		CanonicalURL: d.BaseURL + r.URL.Path,
	}
	if IsHTMX(r) {
		storefront.QuizContent(props).Render(ctx, w) //nolint:errcheck
		return
	}
	storefront.QuizPage(props).Render(ctx, w) //nolint:errcheck
}
