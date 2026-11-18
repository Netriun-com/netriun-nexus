// SPDX-License-Identifier: AGPL-3.0-only

package aws_test

import (
	"context"
	"errors"
	"net/http"
	"testing"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/netriun/nexus/internal/cloud"
	"github.com/netriun/nexus/internal/providers"
	"github.com/netriun/nexus/internal/providers/aws"
	"github.com/netriun/nexus/internal/providers/internal/testutil"
)

func TestRecordedInventoryParity(t *testing.T) {
	ctx := context.Background()
	account := providers.Account{WorkspaceID: 17, ID: 42, Provider: "aws", Region: "us-east-1"}
	replay := &testutil.Replay{T: t, Provider: "aws"}
	client := ec2.NewFromConfig(sdkaws.Config{Region: account.Region, Credentials: credentials.NewStaticCredentialsProvider("fixture", "fixture", ""), RetryMaxAttempts: 1, HTTPClient: &http.Client{Transport: replay}})
	adapter, err := aws.New(func(ctx context.Context, got providers.Account, region string) (*ec2.Client, error) {
		if got != account || region != account.Region {
			t.Fatalf("wrong client scope: %+v", got)
		}
		return client, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	testutil.Check(t, replay, adapter, account, "EC2 instance", func() ([]cloud.Instance, error) { return cloud.Inventory(ctx, client) })
}

func TestFactoryFailures(t *testing.T) {
	if _, err := aws.New(nil); err == nil {
		t.Fatal("nil factory accepted")
	}
	account := providers.Account{WorkspaceID: 17, ID: 42, Provider: "aws", Region: "us-east-1"}
	failure := errors.New("factory unavailable")
	for _, expected := range []error{failure, nil} {
		adapter, err := aws.New(func(context.Context, providers.Account, string) (*ec2.Client, error) { return nil, expected })
		if err != nil {
			t.Fatal(err)
		}
		items, relations, err := adapter.List(context.Background(), account, providers.Compute, account.Region)
		if err == nil || items != nil || relations != nil {
			t.Fatalf("factory failure: %v %v %v", items, relations, err)
		}
		if expected != nil && !errors.Is(err, expected) {
			t.Fatalf("factory error lost: %v", err)
		}
	}
}
