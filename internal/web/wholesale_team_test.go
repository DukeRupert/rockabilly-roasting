package web

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// Invited teammates are subscribed by default and opt out themselves. The
// checkbox therefore has to distinguish "deliberately unticked" from "field
// absent" — an unchecked box submits nothing at all, so without the hidden
// companion field an untick would be silently ignored.
func TestInviteWantsNotifications(t *testing.T) {
	post := func(form url.Values) *http.Request {
		r := httptest.NewRequest(http.MethodPost, "/wholesale/account/team/invite", strings.NewReader(form.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		return r
	}

	tests := []struct {
		name string
		form url.Values
		want bool
	}{
		{
			name: "checkbox ticked",
			form: url.Values{"notifications_submitted": {"1"}, "receives_notifications": {"1"}},
			want: true,
		},
		{
			// The box was rendered and deliberately unticked, so no
			// receives_notifications key is submitted at all.
			name: "checkbox unticked",
			form: url.Values{"notifications_submitted": {"1"}},
			want: false,
		},
		{
			// A form that never carried the field (an older cached page, a
			// scripted post) must not be read as an opt-out.
			name: "field absent entirely",
			form: url.Values{"email": {"a@b.com"}},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, inviteWantsNotifications(post(tt.form)))
		})
	}
}
