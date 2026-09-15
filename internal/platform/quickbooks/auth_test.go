package quickbooks

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRevokeToken(t *testing.T) {
	t.Run("posts token with basic auth and succeeds on 200", func(t *testing.T) {
		var gotBody map[string]string
		var gotUser, gotPass string
		var gotContentType string

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotUser, gotPass, _ = r.BasicAuth()
			gotContentType = r.Header.Get("Content-Type")
			b, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(b, &gotBody)
			w.WriteHeader(http.StatusOK)
		}))
		defer srv.Close()

		err := revokeTokenAt(context.Background(), srv.URL, srv.Client(), "client-id", "client-secret", "refresh-tok")
		require.NoError(t, err)

		assert.Equal(t, "refresh-tok", gotBody["token"])
		assert.Equal(t, "client-id", gotUser)
		assert.Equal(t, "client-secret", gotPass)
		assert.Equal(t, "application/json", gotContentType)
	})

	t.Run("returns error on non-200", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"invalid_token"}`))
		}))
		defer srv.Close()

		err := revokeTokenAt(context.Background(), srv.URL, srv.Client(), "id", "secret", "tok")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "400")
		assert.Contains(t, err.Error(), "invalid_token")
	})
}

func TestDefaultRedirectURI(t *testing.T) {
	tests := []struct {
		name    string
		baseURL string
		want    string
	}{
		{"plain", "https://shop.example.com", "https://shop.example.com/admin/settings/integrations/quickbooks/callback"},
		{"trailing slash", "https://shop.example.com/", "https://shop.example.com/admin/settings/integrations/quickbooks/callback"},
		{"surrounding space", "  https://shop.example.com  ", "https://shop.example.com/admin/settings/integrations/quickbooks/callback"},
		{"local dev port", "http://localhost:8099", "http://localhost:8099/admin/settings/integrations/quickbooks/callback"},
		{"unset", "", ""},
		{"blank", "   ", ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, DefaultRedirectURI(tc.baseURL))
		})
	}
}
