package app

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-pdf/fpdf"
	"github.com/jackc/pgx/v5"
	"github.com/netriun/nexus/internal/cloud"
	"github.com/xuri/excelize/v2"
)

var billingCyclePattern = regexp.MustCompile(`^\d{4}-(0[1-9]|1[0-2])$`)

type alibabaBillingRow struct {
	AccountID         int64     `json:"account_id"`
	AccountName       string    `json:"account_name"`
	Service           string    `json:"service"`
	ProductCode       string    `json:"product_code"`
	ProductName       string    `json:"product_name"`
	ProductDetail     string    `json:"product_detail"`
	InstanceID        string    `json:"instance_id"`
	InstanceName      string    `json:"instance_name"`
	Region            string    `json:"region"`
	SubscriptionType  string    `json:"subscription_type"`
	Currency          string    `json:"currency"`
	PretaxGrossAmount float64   `json:"pretax_gross_amount"`
	InvoiceDiscount   float64   `json:"invoice_discount"`
	PretaxAmount      float64   `json:"pretax_amount"`
	CashAmount        float64   `json:"cash_amount"`
	PaymentAmount     float64   `json:"payment_amount"`
	FetchedAt         time.Time `json:"fetched_at"`
}

type alibabaBillingTotal struct {
	Service  string  `json:"service"`
	Currency string  `json:"currency"`
	Amount   float64 `json:"amount"`
}

type alibabaBillingReport struct {
	BillingCycle  string                `json:"billing_cycle"`
	Services      []string              `json:"services"`
	ResourceQuery string                `json:"resource_query"`
	Totals        []alibabaBillingTotal `json:"totals"`
	Rows          []alibabaBillingRow   `json:"rows"`
	FetchedAt     *time.Time            `json:"fetched_at,omitempty"`
	RowCount      int                   `json:"row_count"`
}

func defaultBillingCycle() string { return time.Now().UTC().AddDate(0, -1, 0).Format("2006-01") }

func validBillingCycle(value string) bool {
	if !billingCyclePattern.MatchString(value) {
		return false
	}
	month, err := time.Parse("2006-01", value)
	if err != nil {
		return false
	}
	now := time.Now().UTC()
	current := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	return !month.After(current) && !month.Before(current.AddDate(0, -17, 0))
}

func requestedBillingServices(r *http.Request) ([]string, bool) {
	raw := strings.TrimSpace(r.URL.Query().Get("services"))
	if raw == "" {
		return []string{"ecs", "eds"}, true
	}
	seen := map[string]bool{}
	services := []string{}
	for _, service := range strings.Split(raw, ",") {
		service = strings.ToLower(strings.TrimSpace(service))
		if service != "ecs" && service != "eds" && service != "other" {
			return nil, false
		}
		if !seen[service] {
			seen[service] = true
			services = append(services, service)
		}
	}
	return services, len(services) > 0
}

func (a *App) alibabaBillingScope(w http.ResponseWriter, r *http.Request, capability Capability) ([]int64, string, []string, bool) {
	allowed, err := a.Policy.AccessibleAccountIDs(r.Context(), current(r), capability)
	if err != nil {
		dbError(w, err)
		return nil, "", nil, false
	}
	ids, ok := a.requestedAccountIDs(w, r, allowed)
	if !ok || len(ids) == 0 {
		if ok {
			problem(w, http.StatusBadRequest, "Select at least one Alibaba Cloud account")
		}
		return nil, "", nil, false
	}
	var count int
	if err = a.DB.QueryRow(r.Context(), `SELECT count(*) FROM cloud_accounts WHERE workspace_id=$1 AND id=ANY($2) AND provider='alibaba'`, current(r).WorkspaceID, ids).Scan(&count); err != nil {
		dbError(w, err)
		return nil, "", nil, false
	}
	if count != len(ids) {
		problem(w, http.StatusBadRequest, "Billing reports currently support Alibaba Cloud accounts only")
		return nil, "", nil, false
	}
	cycle := strings.TrimSpace(r.URL.Query().Get("billing_cycle"))
	if cycle == "" {
		cycle = defaultBillingCycle()
	}
	if !validBillingCycle(cycle) {
		problem(w, http.StatusBadRequest, "Billing cycle must be a month within the latest 18 months in YYYY-MM format")
		return nil, "", nil, false
	}
	services, ok := requestedBillingServices(r)
	if !ok {
		problem(w, http.StatusBadRequest, "Services must contain ecs, eds, or other")
		return nil, "", nil, false
	}
	return ids, cycle, services, true
}

func (a *App) syncAlibabaBilling(w http.ResponseWriter, r *http.Request) {
	ids, cycle, _, ok := a.alibabaBillingScope(w, r, CapabilityBillingSync)
	if !ok {
		return
	}
	if len(ids) > 10 {
		problem(w, http.StatusBadRequest, "Synchronize at most 10 accounts at a time")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Minute)
	defer cancel()
	type result struct {
		accountID int64
		items     []cloud.AlibabaBillItem
	}
	results := make([]result, 0, len(ids))
	for _, accountID := range ids {
		credentials, err := a.credentials(ctx, current(r).WorkspaceID, accountID, "alibaba")
		if err != nil {
			dbError(w, err)
			return
		}
		client, err := cloud.AlibabaBillingClient(credentials.AccessKey, credentials.SecretKey, credentials.SessionToken)
		if err != nil {
			problem(w, http.StatusBadGateway, "Could not initialize Alibaba billing client")
			return
		}
		items, err := cloud.AlibabaInstanceBills(ctx, client, cycle)
		if err != nil {
			problem(w, http.StatusBadGateway, "Alibaba Billing rejected the request; add bssapi:DescribeInstanceBill and verify the account billing scope")
			return
		}
		results = append(results, result{accountID: accountID, items: items})
	}
	tx, err := a.DB.Begin(ctx)
	if err != nil {
		dbError(w, err)
		return
	}
	defer tx.Rollback(ctx)
	for _, result := range results {
		if _, err = tx.Exec(ctx, `DELETE FROM alibaba_billing_items WHERE workspace_id=$1 AND account_id=$2 AND billing_cycle=$3`, current(r).WorkspaceID, result.accountID, cycle); err != nil {
			dbError(w, err)
			return
		}
		rows := make([][]any, 0, len(result.items))
		for _, item := range result.items {
			rows = append(rows, []any{current(r).WorkspaceID, result.accountID, cycle, item.ServiceKey, item.ProductCode, item.ProductName, item.ProductDetail, item.InstanceID, item.InstanceName, item.Region, item.SubscriptionType, item.Currency, item.PretaxGrossAmount, item.InvoiceDiscount, item.PretaxAmount, item.CashAmount, item.PaymentAmount, item.Raw})
		}
		if len(rows) > 0 {
			_, err = tx.CopyFrom(ctx, pgx.Identifier{"alibaba_billing_items"}, []string{"workspace_id", "account_id", "billing_cycle", "service_key", "product_code", "product_name", "product_detail", "instance_id", "instance_name", "region", "subscription_type", "currency", "pretax_gross_amount", "invoice_discount", "pretax_amount", "cash_amount", "payment_amount", "raw"}, pgx.CopyFromRows(rows))
			if err != nil {
				dbError(w, err)
				return
			}
		}
	}
	if err = tx.Commit(ctx); err != nil {
		dbError(w, err)
		return
	}
	count := 0
	for _, result := range results {
		count += len(result.items)
	}
	a.audit(r, "billing.alibaba_synced", cycle, map[string]any{"account_ids": ids, "rows": count})
	write(w, http.StatusOK, map[string]any{"billing_cycle": cycle, "accounts": len(ids), "rows": count, "synced_at": time.Now().UTC()})
}

func (a *App) buildAlibabaBillingReport(ctx context.Context, workspaceID int64, ids []int64, cycle string, services []string, resourceQuery string) (alibabaBillingReport, error) {
	rows, err := a.DB.Query(ctx, `SELECT b.account_id,a.name,b.service_key,b.product_code,b.product_name,b.product_detail,b.instance_id,b.instance_name,b.region,b.subscription_type,b.currency,b.pretax_gross_amount,b.invoice_discount,b.pretax_amount,b.cash_amount,b.payment_amount,b.fetched_at FROM alibaba_billing_items b JOIN cloud_accounts a ON a.id=b.account_id AND a.workspace_id=b.workspace_id WHERE b.workspace_id=$1 AND b.account_id=ANY($2) AND b.billing_cycle=$3 AND b.service_key=ANY($4) AND ($5='' OR b.instance_name ILIKE '%'||$5||'%' OR b.instance_id ILIKE '%'||$5||'%') ORDER BY b.service_key,b.instance_name,b.instance_id,b.product_detail`, workspaceID, ids, cycle, services, resourceQuery)
	if err != nil {
		return alibabaBillingReport{}, err
	}
	defer rows.Close()
	report := alibabaBillingReport{BillingCycle: cycle, Services: services, ResourceQuery: resourceQuery, Rows: []alibabaBillingRow{}, Totals: []alibabaBillingTotal{}}
	totals := map[string]float64{}
	for rows.Next() {
		var item alibabaBillingRow
		if err = rows.Scan(&item.AccountID, &item.AccountName, &item.Service, &item.ProductCode, &item.ProductName, &item.ProductDetail, &item.InstanceID, &item.InstanceName, &item.Region, &item.SubscriptionType, &item.Currency, &item.PretaxGrossAmount, &item.InvoiceDiscount, &item.PretaxAmount, &item.CashAmount, &item.PaymentAmount, &item.FetchedAt); err != nil {
			return report, err
		}
		report.Rows = append(report.Rows, item)
		key := item.Service + "\x00" + item.Currency
		totals[key] += item.PretaxAmount
		if report.FetchedAt == nil || item.FetchedAt.After(*report.FetchedAt) {
			value := item.FetchedAt
			report.FetchedAt = &value
		}
	}
	if err = rows.Err(); err != nil {
		return report, err
	}
	keys := make([]string, 0, len(totals))
	for key := range totals {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		parts := strings.SplitN(key, "\x00", 2)
		report.Totals = append(report.Totals, alibabaBillingTotal{Service: parts[0], Currency: parts[1], Amount: totals[key]})
	}
	report.RowCount = len(report.Rows)
	return report, nil
}

func (a *App) alibabaBillingReport(w http.ResponseWriter, r *http.Request) {
	ids, cycle, services, ok := a.alibabaBillingScope(w, r, CapabilityBillingView)
	if !ok {
		return
	}
	report, err := a.buildAlibabaBillingReport(r.Context(), current(r).WorkspaceID, ids, cycle, services, strings.TrimSpace(r.URL.Query().Get("resource_query")))
	if err != nil {
		dbError(w, err)
		return
	}
	write(w, http.StatusOK, report)
}

func billingExportName(cycle, extension string) string {
	return "netriun-nexus-alibaba-billing-" + cycle + "." + extension
}

func (a *App) exportAlibabaBilling(w http.ResponseWriter, r *http.Request) {
	ids, cycle, services, ok := a.alibabaBillingScope(w, r, CapabilityBillingView)
	if !ok {
		return
	}
	report, err := a.buildAlibabaBillingReport(r.Context(), current(r).WorkspaceID, ids, cycle, services, strings.TrimSpace(r.URL.Query().Get("resource_query")))
	if err != nil {
		dbError(w, err)
		return
	}
	format := strings.ToLower(r.URL.Query().Get("format"))
	var body []byte
	switch format {
	case "xlsx":
		body, err = alibabaBillingXLSX(report)
		w.Header().Set("Content-Type", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
	case "pdf":
		body, err = alibabaBillingPDF(report)
		w.Header().Set("Content-Type", "application/pdf")
	default:
		problem(w, http.StatusBadRequest, "Export format must be xlsx or pdf")
		return
	}
	if err != nil {
		problem(w, http.StatusInternalServerError, "Could not generate billing export")
		return
	}
	w.Header().Set("Content-Disposition", `attachment; filename="`+billingExportName(cycle, format)+`"`)
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

func alibabaBillingXLSX(report alibabaBillingReport) ([]byte, error) {
	book := excelize.NewFile()
	defer book.Close()
	sheet := "Billing detail"
	_ = book.SetSheetName("Sheet1", sheet)
	headers := []string{"Account", "Service", "Product code", "Product", "Resource name", "Resource ID", "Region", "Billing method", "Currency", "Gross amount", "Discount", "Pretax amount", "Cash amount", "Payment amount"}
	for index, value := range headers {
		cell, _ := excelize.CoordinatesToCellName(index+1, 1)
		_ = book.SetCellValue(sheet, cell, value)
	}
	for row, item := range report.Rows {
		values := []any{item.AccountName, strings.ToUpper(item.Service), item.ProductCode, item.ProductName, item.InstanceName, item.InstanceID, item.Region, item.SubscriptionType, item.Currency, item.PretaxGrossAmount, item.InvoiceDiscount, item.PretaxAmount, item.CashAmount, item.PaymentAmount}
		for column, value := range values {
			cell, _ := excelize.CoordinatesToCellName(column+1, row+2)
			_ = book.SetCellValue(sheet, cell, value)
		}
	}
	headerStyle, _ := book.NewStyle(&excelize.Style{Font: &excelize.Font{Bold: true, Color: "FFFFFF"}, Fill: excelize.Fill{Type: "pattern", Color: []string{"193047"}, Pattern: 1}})
	_ = book.SetCellStyle(sheet, "A1", "N1", headerStyle)
	_ = book.SetPanes(sheet, &excelize.Panes{Freeze: true, YSplit: 1})
	_ = book.SetColWidth(sheet, "A", "N", 18)
	_ = book.SetColWidth(sheet, "D", "F", 26)
	summary := "Summary"
	_, _ = book.NewSheet(summary)
	_ = book.SetCellValue(summary, "A1", "Alibaba Cloud billing report")
	_ = book.SetCellValue(summary, "A2", "Billing cycle")
	_ = book.SetCellValue(summary, "B2", report.BillingCycle)
	_ = book.SetCellValue(summary, "A3", "Resource filter")
	_ = book.SetCellValue(summary, "B3", report.ResourceQuery)
	_ = book.SetCellValue(summary, "A5", "Service")
	_ = book.SetCellValue(summary, "B5", "Currency")
	_ = book.SetCellValue(summary, "C5", "Pretax amount")
	for index, total := range report.Totals {
		row := index + 6
		_ = book.SetCellValue(summary, fmt.Sprintf("A%d", row), strings.ToUpper(total.Service))
		_ = book.SetCellValue(summary, fmt.Sprintf("B%d", row), total.Currency)
		_ = book.SetCellValue(summary, fmt.Sprintf("C%d", row), total.Amount)
	}
	_ = book.SetCellStyle(summary, "A5", "C5", headerStyle)
	book.SetActiveSheet(1)
	buffer, err := book.WriteToBuffer()
	if err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}

func alibabaBillingPDF(report alibabaBillingReport) ([]byte, error) {
	pdf := fpdf.New("L", "mm", "A4", "")
	pdf.SetTitle("Netriun Nexus Alibaba billing report", false)
	pdf.AddPage()
	pdf.SetFont("Helvetica", "B", 16)
	pdf.Cell(0, 10, "Netriun Nexus - Alibaba Cloud billing report")
	pdf.Ln(12)
	pdf.SetFont("Helvetica", "", 10)
	pdf.Cell(0, 6, "Billing cycle: "+report.BillingCycle+"    Services: "+strings.ToUpper(strings.Join(report.Services, ", ")))
	pdf.Ln(8)
	for _, total := range report.Totals {
		pdf.SetFont("Helvetica", "B", 11)
		pdf.Cell(0, 7, fmt.Sprintf("%s total: %.2f %s", strings.ToUpper(total.Service), total.Amount, total.Currency))
		pdf.Ln(7)
	}
	pdf.Ln(3)
	widths := []float64{30, 15, 35, 42, 42, 30, 24, 24}
	headers := []string{"Account", "Service", "Product", "Resource name", "Resource ID", "Region", "Currency", "Pretax"}
	pdf.SetFont("Helvetica", "B", 8)
	for i, h := range headers {
		pdf.CellFormat(widths[i], 7, h, "1", 0, "L", false, 0, "")
	}
	pdf.Ln(-1)
	pdf.SetFont("Helvetica", "", 7)
	clean := func(value string) string {
		var b strings.Builder
		for _, r := range value {
			if r >= 32 && r <= 126 {
				b.WriteRune(r)
			} else {
				b.WriteByte('?')
			}
		}
		return b.String()
	}
	for _, item := range report.Rows {
		values := []string{item.AccountName, strings.ToUpper(item.Service), item.ProductName, item.InstanceName, item.InstanceID, item.Region, item.Currency, fmt.Sprintf("%.4f", item.PretaxAmount)}
		for i, value := range values {
			if len(value) > 32 {
				value = value[:29] + "..."
			}
			pdf.CellFormat(widths[i], 6, clean(value), "1", 0, "L", false, 0, "")
		}
		pdf.Ln(-1)
	}
	var buffer bytes.Buffer
	err := pdf.Output(&buffer)
	return buffer.Bytes(), err
}

func alibabaBillingExportURL(accountIDs []int64, cycle string, services []string, resourceQuery, format string) string {
	values := url.Values{}
	ids := make([]string, len(accountIDs))
	for i, id := range accountIDs {
		ids[i] = strconv.FormatInt(id, 10)
	}
	values.Set("account_ids", strings.Join(ids, ","))
	values.Set("billing_cycle", cycle)
	values.Set("services", strings.Join(services, ","))
	values.Set("resource_query", resourceQuery)
	values.Set("format", format)
	return values.Encode()
}
