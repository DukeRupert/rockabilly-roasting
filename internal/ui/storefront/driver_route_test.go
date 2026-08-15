package storefront_test

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/ui/storefront"
)

func driverProps() storefront.DriverRouteProps {
	return storefront.DriverRouteProps{
		Token: "test-token",
		Stops: []storefront.DriverStop{
			{
				ID: uuid.New(), Position: 1, CustomerName: "Bunker Coffee",
				Address: "100 First St, Kennewick, WA 99336", Notes: "Side door",
				Status: domain.RouteStopPending, Lat: 46.2087, Lng: -119.1372,
			},
			{
				ID: uuid.New(), Position: 2, CustomerName: "Jane Doe",
				Address: "200 Second St, Pasco, WA 99301",
				Status:  domain.RouteStopDelivered, Lat: 46.2396, Lng: -119.1006,
			},
			{
				ID: uuid.New(), Position: 3, CustomerName: "Cafe Rojo",
				Address: "300 Third St, Richland, WA 99352",
				Status:  domain.RouteStopSkipped, SkipReason: "nobody home",
				Lat: 46.2857, Lng: -119.2845,
			},
		},
		Progress:  domain.RouteProgress{Total: 3, Delivered: 1, Skipped: 1},
		OriginLat: 46.2087,
		OriginLng: -119.1372,
		Roundtrip: true,
	}
}

func TestDriverRoutePageRenders(t *testing.T) {
	var sb strings.Builder
	require.NoError(t, storefront.DriverRoutePage(driverProps()).Render(context.Background(), &sb))
	html := sb.String()

	assert.Contains(t, html, "Today's run")
	assert.Contains(t, html, "Bunker Coffee")
	// The pending stop is promoted to the top as "Next stop".
	assert.Contains(t, html, "Next stop")
	// Progress reads 2 of 3: delivered plus skipped are both resolved.
	assert.Contains(t, html, "2 of 3")
	assert.Contains(t, html, "nobody home")

	// Navigation must carry coordinates, never the address text — an address
	// handed to a maps app gets re-geocoded and can land on a different pin.
	assert.Contains(t, html, "destination=46.208700,-119.137200")
	assert.Contains(t, html, "maps.apple.com/?daddr=46.208700,-119.137200")
	assert.NotContains(t, html, "destination=100+First+St")

	// The token in the URL is a credential.
	assert.Contains(t, html, `name="robots" content="noindex, nofollow"`)

	// Actions post; a link prefetcher must not be able to resolve a delivery.
	assert.Contains(t, html, `hx-post="/routes/test-token/stops/`)
	assert.NotContains(t, html, `hx-get="/routes/test-token/stops/`)
}

func TestDriverRouteBodyCompleted(t *testing.T) {
	props := driverProps()
	props.Completed = true
	props.Progress = domain.RouteProgress{Total: 3, Delivered: 2, Skipped: 1}

	var sb strings.Builder
	require.NoError(t, storefront.DriverRouteBody(props).Render(context.Background(), &sb))
	html := sb.String()

	assert.Contains(t, html, "Run complete")
	// A finished run offers no further actions.
	assert.NotContains(t, html, "Next stop")
}

// An empty route must not divide by zero computing the progress bar.
func TestDriverRouteBodyEmpty(t *testing.T) {
	var sb strings.Builder
	err := storefront.DriverRouteBody(storefront.DriverRouteProps{Token: "t"}).Render(context.Background(), &sb)
	require.NoError(t, err)
	assert.Contains(t, sb.String(), "width: 0%")
}

func TestDriverRouteGonePageRenders(t *testing.T) {
	var sb strings.Builder
	require.NoError(t, storefront.DriverRouteGonePage().Render(context.Background(), &sb))
	html := sb.String()

	assert.Contains(t, html, "Route closed")
	// Deliberately says nothing about which failure it was.
	assert.NotContains(t, html, "expired")
	assert.NotContains(t, html, "draft")
}
