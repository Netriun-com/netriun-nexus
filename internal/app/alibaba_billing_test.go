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
