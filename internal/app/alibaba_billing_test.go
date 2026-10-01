package app

import (
	"net/http/httptest"
	"reflect"
	"testing"
	"time"
)

func TestRequestedBillingServices(t *testing.T) {
	tests := []struct {
		query string
		want  []string
		ok    bool
	}{
		{"", []string{"ecs", "eds"}, true},
		{"ecs,eds,ecs", []string{"ecs", "eds"}, true},
		{"other", []string{"other"}, true},
		{"ecs,oss", nil, false},
		{"ecs,", nil, false},
	}
	for _, test := range tests {
		r := httptest.NewRequest("GET", "/?services="+test.query, nil)
		got, ok := requestedBillingServices(r)
		if ok != test.ok || !reflect.DeepEqual(got, test.want) {
			t.Fatalf("services %q = (%v, %v), want (%v, %v)", test.query, got, ok, test.want, test.ok)
		}
	}
}

func TestValidBillingCycle(t *testing.T) {
	now := time.Now().UTC()
	if !validBillingCycle(now.AddDate(0, -1, 0).Format("2006-01")) {
		t.Fatal("previous month must be valid")
	}
	if validBillingCycle(now.AddDate(0, 1, 0).Format("2006-01")) {
		t.Fatal("future month must be invalid")
	}
	if validBillingCycle(now.AddDate(0, -18, 0).Format("2006-01")) {
		t.Fatal("a month older than the latest 18 cycles must be invalid")
	}
	if validBillingCycle("2026-13") || validBillingCycle("not-a-month") {
		t.Fatal("malformed billing cycles must be invalid")
	}
}

func TestAlibabaBillingExports(t *testing.T) {
	report := alibabaBillingReport{
		BillingCycle: "2026-09",
		Services:     []string{"ecs", "eds"},
		Totals:       []alibabaBillingTotal{{Service: "ecs", Currency: "USD", Amount: 12.5}},
		Rows:         []alibabaBillingRow{{AccountName: "Production", Service: "ecs", ProductName: "Elastic Compute Service", InstanceID: "i-example", InstanceName: "api-01", Region: "cn-hangzhou", Currency: "USD", PretaxAmount: 12.5}},
	}
	xlsx, err := alibabaBillingXLSX(report)
	if err != nil || len(xlsx) < 4 || string(xlsx[:2]) != "PK" {
		t.Fatalf("xlsx export failed: bytes=%d err=%v", len(xlsx), err)
	}
	pdf, err := alibabaBillingPDF(report)
	if err != nil || len(pdf) < 5 || string(pdf[:4]) != "%PDF" {
		t.Fatalf("pdf export failed: bytes=%d err=%v", len(pdf), err)
	}
}
