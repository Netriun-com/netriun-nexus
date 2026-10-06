// SPDX-License-Identifier: AGPL-3.0-only

package cloud

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	bss "github.com/alibabacloud-go/bssopenapi-20171214/v6/client"
	openapi "github.com/alibabacloud-go/darabonba-openapi/v2/utils"
	"github.com/alibabacloud-go/tea/dara"
)

// AlibabaBillItem is one exact provider-reported monthly instance bill row.
// Amounts are kept separate so the portal never substitutes an estimate for
// the payable pre-tax value returned by Alibaba BSS OpenAPI.
type AlibabaBillItem struct {
	ServiceKey        string
	ProductCode       string
	ProductName       string
	ProductDetail     string
	InstanceID        string
	InstanceName      string
	Region            string
	SubscriptionType  string
	Currency          string
	PretaxGrossAmount float64
	InvoiceDiscount   float64
	PretaxAmount      float64
	CashAmount        float64
	PaymentAmount     float64
	Raw               []byte
}

func AlibabaBillingClient(key, secret, token string) (*bss.Client, error) {
	config := new(openapi.Config).
		SetRegionId("cn-hangzhou").
		SetEndpoint("business.aliyuncs.com").
		SetProtocol("HTTPS").
		SetAccessKeyId(key).
		SetAccessKeySecret(secret)
	if token != "" {
		config.SetSecurityToken(token)
	}
	return bss.NewClient(config)
}

// AlibabaInstanceBills reads every page for a single monthly billing cycle.
// Service classification is deliberately based on both stable product codes
// and display names because WUYING product codes vary between EDS offerings.
func AlibabaInstanceBills(ctx context.Context, client *bss.Client, billingCycle string) ([]AlibabaBillItem, error) {
	items := []AlibabaBillItem{}
	next := ""
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		request := new(bss.DescribeInstanceBillRequest).
			SetBillingCycle(billingCycle).
			SetGranularity("MONTHLY").
			SetIsBillingItem(false).
			SetIsHideZeroCharge(false).
			SetMaxResults(300)
		if next != "" {
			request.SetNextToken(next)
		}
		response, err := client.DescribeInstanceBillWithContext(ctx, request, alibabaRuntime())
		if err != nil {
			return nil, err
		}
		if response == nil || response.Body == nil || response.Body.Data == nil {
			return nil, errors.New("Alibaba BSS returned an empty response")
		}
		if response.Body.Success != nil && !dara.BoolValue(response.Body.Success) {
			return nil, errors.New("Alibaba BSS rejected the request: " + dara.StringValue(response.Body.Code) + " " + dara.StringValue(response.Body.Message))
		}
		for _, row := range response.Body.Data.Items {
			if row == nil {
				continue
			}
			raw, _ := json.Marshal(row)
			items = append(items, AlibabaBillItem{
				ServiceKey:  classifyAlibabaBillService(dara.StringValue(row.ProductCode), dara.StringValue(row.ProductName), dara.StringValue(row.ProductDetail)),
				ProductCode: dara.StringValue(row.ProductCode), ProductName: dara.StringValue(row.ProductName), ProductDetail: dara.StringValue(row.ProductDetail),
				InstanceID: dara.StringValue(row.InstanceID), InstanceName: dara.StringValue(row.NickName), Region: dara.StringValue(row.Region),
				SubscriptionType: dara.StringValue(row.SubscriptionType), Currency: dara.StringValue(row.Currency),
				PretaxGrossAmount: float64(dara.Float32Value(row.PretaxGrossAmount)), InvoiceDiscount: float64(dara.Float32Value(row.InvoiceDiscount)),
				PretaxAmount: float64(dara.Float32Value(row.PretaxAmount)), CashAmount: float64(dara.Float32Value(row.CashAmount)),
				PaymentAmount: float64(dara.Float32Value(row.PaymentAmount)), Raw: raw,
			})
		}
		next = dara.StringValue(response.Body.Data.NextToken)
		if next == "" {
			break
		}
	}
	return items, nil
}

func classifyAlibabaBillService(values ...string) string {
	joined := strings.ToLower(strings.Join(values, " "))
	code := strings.ToLower(strings.TrimSpace(values[0]))
	if code == "ecs" || strings.Contains(joined, "elastic compute service") {
		return "ecs"
	}
	if code == "eds" || code == "ecd" || code == "gws" || strings.Contains(joined, "elastic desktop") || strings.Contains(joined, "cloud desktop") || strings.Contains(joined, "wuying") {
		return "eds"
	}
	return "other"
}
