package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func testDashboardRequest(t *testing.T, auth *portalAuth, user *portalUser, form url.Values) *http.Request {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/admin/power", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.AddCookie(&http.Cookie{Name: "pp_session", Value: "test-session-token"})
	if user != nil {
		request = request.WithContext(context.WithValue(request.Context(), portalUserKey{}, user))
	}
	return request
}

func TestPowerAdminOnly(t *testing.T) {
	auth := &portalAuth{}
	invoked := false
	app := &dashboardApp{auth: auth, schedulePower: func(_ context.Context, _ string) error {
		invoked = true
		return nil
	}}
	request := testDashboardRequest(t, auth, &portalUser{ID: 2, IsAdmin: false}, url.Values{"action": {"reboot"}})
	response := httptest.NewRecorder()
	app.power(response, request)
	if response.Code != http.StatusForbidden || invoked {
		t.Fatalf("non-admin: status=%d invoked=%v", response.Code, invoked)
	}
}

func TestPowerRequiresCSRF(t *testing.T) {
	auth := &portalAuth{}
	invoked := false
	app := &dashboardApp{auth: auth, schedulePower: func(_ context.Context, _ string) error {
		invoked = true
		return nil
	}}
	request := testDashboardRequest(t, auth, &portalUser{ID: 1, IsAdmin: true}, url.Values{
		"action": {"reboot"}, "confirm": {"yes"}, "csrf_token": {"wrong"},
	})
	response := httptest.NewRecorder()
	app.power(response, request)
	if response.Code != http.StatusForbidden || invoked {
		t.Fatalf("bad CSRF: status=%d invoked=%v", response.Code, invoked)
	}
}

func TestPowerWhitelistAndAcceptedResponse(t *testing.T) {
	auth := &portalAuth{}
	actions := make([]string, 0, 1)
	app := &dashboardApp{auth: auth, schedulePower: func(_ context.Context, action string) error {
		actions = append(actions, action)
		return nil
	}}
	for _, test := range []struct {
		action string
		status int
	}{{"anything", http.StatusBadRequest}, {"reboot", http.StatusAccepted}, {"poweroff", http.StatusAccepted}} {
		request := testDashboardRequest(t, auth, &portalUser{ID: 1, IsAdmin: true}, url.Values{
			"action": {test.action}, "confirm": {"yes"},
		})
		// Build the same request with a valid CSRF token, derived from its session cookie.
		csrf := auth.csrf(request)
		request = testDashboardRequest(t, auth, &portalUser{ID: 1, IsAdmin: true}, url.Values{
			"action": {test.action}, "confirm": {"yes"}, "csrf_token": {csrf},
		})
		response := httptest.NewRecorder()
		app.power(response, request)
		if response.Code != test.status {
			t.Fatalf("action %q: got %d, expected %d: %s", test.action, response.Code, test.status, response.Body.String())
		}
	}
	if len(actions) != 2 || actions[0] != "reboot" || actions[1] != "poweroff" {
		t.Fatalf("unexpected actions: %v", actions)
	}
}

func TestDashboardPageAdminControls(t *testing.T) {
	auth := &portalAuth{}
	app := &dashboardApp{auth: auth}
	for _, test := range []struct {
		admin     bool
		hasButton bool
	}{{false, false}, {true, true}} {
		request := httptest.NewRequest(http.MethodGet, "/", nil)
		request.AddCookie(&http.Cookie{Name: "pp_session", Value: "test-session-token"})
		request = request.WithContext(context.WithValue(request.Context(), portalUserKey{}, &portalUser{ID: 1, DisplayName: "Тест", IsAdmin: test.admin}))
		response := httptest.NewRecorder()
		app.page(response, request)
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "Панель мониторинга") {
			t.Fatalf("page bad: status=%d body=%s", response.Code, response.Body.String())
		}
		if strings.Contains(response.Body.String(), "Выключить сервер") != test.hasButton {
			t.Fatalf("admin controls: admin=%v, body=%s", test.admin, response.Body.String())
		}
	}
}
