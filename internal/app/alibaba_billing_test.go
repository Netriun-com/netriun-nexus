// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"bytes"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"github.com/xuri/excelize/v2"
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

func TestDefaultBillingCycleAt(t *testing.T) {
	tests := []struct {
		now, want string
	}{
		{"2026-10-31T12:00:00Z", "2026-09"},
		{"2026-03-31T12:00:00Z", "2026-02"},
		{"2026-01-01T00:00:00Z", "2025-12"},
		{"2024-03-31T12:00:00Z", "2024-02"},
		// UTC, rather than the reference's local date, determines the month.
		{"2026-11-01T00:30:00+02:00", "2026-09"},
		{"2026-10-31T23:30:00-02:00", "2026-10"},
	}
	for _, tt := range tests {
		t.Run(tt.now, func(t *testing.T) {
			now, err := time.Parse(time.RFC3339, tt.now)
			if err != nil {
				t.Fatal(err)
			}
			if got := defaultBillingCycleAt(now); got != tt.want {
				t.Errorf("defaultBillingCycleAt(%s) = %q, want %q", tt.now, got, tt.want)
			}
		})
	}
}

// billingCycleTestMonth supplies calendar-month fixtures to TestValidBillingCycle.
func billingCycleTestMonth(now time.Time, offset int) string {
	now = now.UTC()
	current := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	return current.AddDate(0, offset, 0).Format("2006-01")
}

func TestBillingCycleTestMonth(t *testing.T) {
	tests := []struct {
		reference, previous, current, next, oldest, expired string
	}{
		{"2026-10-31T12:00:00Z", "2026-09", "2026-10", "2026-11", "2025-05", "2025-04"},
		{"2026-03-31T12:00:00Z", "2026-02", "2026-03", "2026-04", "2024-10", "2024-09"},
		{"2026-01-31T12:00:00Z", "2025-12", "2026-01", "2026-02", "2024-08", "2024-07"},
		// The reference month is UTC even when the supplied date has another offset.
		{"2026-10-31T23:30:00-02:00", "2026-10", "2026-11", "2026-12", "2025-06", "2025-05"},
	}
	for _, tt := range tests {
		t.Run(tt.reference, func(t *testing.T) {
			now, err := time.Parse(time.RFC3339, tt.reference)
			if err != nil {
				t.Fatal(err)
			}
			for _, month := range []struct {
				offset int
				want   string
			}{{-1, tt.previous}, {0, tt.current}, {1, tt.next}, {-17, tt.oldest}, {-18, tt.expired}} {
				if got := billingCycleTestMonth(now, month.offset); got != month.want {
					t.Errorf("offset %d: got %s, want %s", month.offset, got, month.want)
				}
			}
		})
	}
}

func TestValidBillingCycle(t *testing.T) {
	now := time.Now().UTC()
	for _, offset := range []int{0, -17} {
		if !validBillingCycle(billingCycleTestMonth(now, offset)) {
			t.Errorf("billing month at offset %d must be valid", offset)
		}
	}
	if !validBillingCycle(billingCycleTestMonth(now, -1)) {
		t.Fatal("previous month must be valid")
	}
	if validBillingCycle(billingCycleTestMonth(now, 1)) {
		t.Fatal("future month must be invalid")
	}
	if validBillingCycle(billingCycleTestMonth(now, -18)) {
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
		Rows: []alibabaBillingRow{{
			AccountName: "Production & 测试", Service: "ecs", ProductCode: "ecs",
			ProductName: "Elastic Compute Service", InstanceID: "i-example", InstanceName: "api-01",
			Region: "cn-hangzhou", SubscriptionType: "PayAsYouGo", Currency: "USD",
			PretaxGrossAmount: 15, InvoiceDiscount: 2.5, PretaxAmount: 12.5, CashAmount: 10, PaymentAmount: 12.5,
		}},
	}
	xlsx, err := alibabaBillingXLSX(report)
	if err != nil || len(xlsx) < 4 || string(xlsx[:2]) != "PK" {
		t.Fatalf("xlsx export failed: bytes=%d err=%v", len(xlsx), err)
	}
	book, err := excelize.OpenReader(bytes.NewReader(xlsx))
	if err != nil {
		t.Fatalf("open exported workbook: %v", err)
	}
	t.Cleanup(func() {
		if err := book.Close(); err != nil {
			t.Errorf("close exported workbook: %v", err)
		}
	})
	if got, want := book.GetSheetList(), []string{"Billing detail", "Summary"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("sheets = %v, want %v", got, want)
	}
	rows, err := book.GetRows("Billing detail")
	if err != nil {
		t.Fatalf("read billing detail: %v", err)
	}
	wantRows := [][]string{
		{"Account", "Service", "Product code", "Product", "Resource name", "Resource ID", "Region", "Billing method", "Currency", "Gross amount", "Discount", "Pretax amount", "Cash amount", "Payment amount"},
		{"Production & 测试", "ECS", "ecs", "Elastic Compute Service", "api-01", "i-example", "cn-hangzhou", "PayAsYouGo", "USD", "15", "2.5", "12.5", "10", "12.5"},
	}
	if !reflect.DeepEqual(rows, wantRows) {
		t.Errorf("billing rows = %v, want %v", rows, wantRows)
	}
	for _, cell := range []string{"J2", "K2", "L2", "M2", "N2"} {
		// XLSX numeric cells may omit the type attribute (CellTypeUnset).
		if cellType, err := book.GetCellType("Billing detail", cell); err != nil || (cellType != excelize.CellTypeNumber && cellType != excelize.CellTypeUnset) {
			t.Errorf("%s type = %v, err = %v; want a numeric amount", cell, cellType, err)
		}
	}
	for _, cell := range []string{"A1", "N1"} {
		styleID, err := book.GetCellStyle("Billing detail", cell)
		if err != nil {
			t.Fatalf("read %s style: %v", cell, err)
		}
		style, err := book.GetStyle(styleID)
		if err != nil {
			t.Fatalf("read header style: %v", err)
		}
		if style.Font == nil || !style.Font.Bold || style.Font.Color != "FFFFFF" || style.Fill.Pattern != 1 || !reflect.DeepEqual(style.Fill.Color, []string{"193047"}) {
			t.Errorf("%s header style = %+v, want bold white text on dark fill", cell, style)
		}
	}
	panes, err := book.GetPanes("Billing detail")
	if err != nil || !panes.Freeze || panes.YSplit != 1 {
		t.Errorf("billing panes = %+v, err = %v; want frozen first row", panes, err)
	}
	summary, err := book.GetRows("Summary")
	if err != nil {
		t.Fatalf("read summary: %v", err)
	}
	if len(summary) != 6 {
		t.Fatalf("summary has %d rows, want 6", len(summary))
	}
	for row, want := range map[int][]string{
		0: {"Alibaba Cloud billing report"},
		1: {"Billing cycle", "2026-09"},
		4: {"Service", "Currency", "Pretax amount"},
		5: {"ECS", "USD", "12.5"},
	} {
		if !reflect.DeepEqual(summary[row], want) {
			t.Errorf("summary row %d = %v, want %v", row+1, summary[row], want)
		}
	}
	pdf, err := alibabaBillingPDF(report)
	if err != nil || len(pdf) < 5 || string(pdf[:4]) != "%PDF" {
		t.Fatalf("pdf export failed: bytes=%d err=%v", len(pdf), err)
	}
}
