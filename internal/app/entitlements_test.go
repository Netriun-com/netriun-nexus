package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/netriun/nexus/internal/entitlements"
)

func TestEntitlementProblemHasStableMachineFields(t *testing.T) {
	w := httptest.NewRecorder()
	entitlementProblem(w, &entitlements.DecisionError{
		Code:    "limit_exceeded",
		Message: "This change exceeds the current edition limit",
		Limit:   entitlements.LimitCloudAccounts,
		Current: 6,
		Allowed: 5,
	})
	if w.Code != http.StatusPaymentRequired {
		t.Fatalf("expected 402, got %d", w.Code)
	}
	var response map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response["code"] != "limit_exceeded" || response["limit"] != "cloud_accounts" || response["current"] != float64(6) || response["allowed"] != float64(5) {
		t.Fatalf("unstable entitlement response: %s", w.Body.String())
	}
}
