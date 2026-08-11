package domain

import "context"

// AdminBadges carries the "needs attention" counts painted onto the admin
// sidebar. It rides in the request context rather than in every page's props
// because the sidebar renders on all thirty-odd admin pages — threading a count
// through each of their prop structs would couple every handler to work it has
// nothing to do with.
//
// It lives in domain so both web (which fills it) and ui (which reads it in the
// admin layout) can reach it; ui may not import web or app.
type AdminBadges struct {
	// WhiteLabelPending is the number of wholesale white-label submissions still
	// sitting in draft, awaiting staff review.
	WhiteLabelPending int
}

type adminBadgesKey struct{}

// WithAdminBadges returns a context carrying the given admin sidebar counts.
func WithAdminBadges(ctx context.Context, b AdminBadges) context.Context {
	return context.WithValue(ctx, adminBadgesKey{}, b)
}

// AdminBadgesFrom returns the admin sidebar counts carried by ctx. The zero value
// is returned when none are present, so a page rendered outside the admin
// middleware (or in a test) simply shows no badges.
func AdminBadgesFrom(ctx context.Context) AdminBadges {
	b, _ := ctx.Value(adminBadgesKey{}).(AdminBadges)
	return b
}
