// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"

	ecs "github.com/alibabacloud-go/ecs-20140526/v7/client"
	oss "github.com/aliyun/alibabacloud-oss-go-sdk-v2/oss"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/netriun/nexus/internal/cloud"
	"github.com/netriun/nexus/internal/providers"
	"github.com/netriun/nexus/internal/providers/alibaba"
	"github.com/netriun/nexus/internal/providers/aws"
	"github.com/netriun/nexus/internal/providers/azure"
	"github.com/netriun/nexus/internal/providers/gcp"
)

// newProviderRegistry runs once at worker startup. Factories resolve the same
// encrypted account credentials as existing actions; construction makes no calls.
func (a *App) newProviderRegistry() (*providers.Registry, error) {
	awsAdapter, err := aws.New(func(ctx context.Context, account providers.Account, region string) (*ec2.Client, error) {
		return a.awsClient(ctx, account.WorkspaceID, account.ID, region)
	})
	if err != nil {
		return nil, err
	}
	alibabaAdapter, err := alibaba.NewWithServices(func(ctx context.Context, account providers.Account, region string) (*ecs.Client, error) {
		return a.alibabaClient(ctx, account.WorkspaceID, account.ID, region)
	}, func(ctx context.Context, account providers.Account, region string) (*cloud.EDSClients, error) {
		credentials, err := a.credentials(ctx, account.WorkspaceID, account.ID, "alibaba")
		if err != nil {
			return nil, err
		}
		return cloud.EDSClient(region, credentials.AccessKey, credentials.SecretKey, credentials.SessionToken)
	}, func(ctx context.Context, account providers.Account, region string) (*oss.Client, error) {
		credentials, err := a.credentials(ctx, account.WorkspaceID, account.ID, "alibaba")
		if err != nil {
			return nil, err
		}
		if region == "*" {
			// Preserve the existing account-selected endpoint for ListBuckets.
			if err := a.DB.QueryRow(ctx, `SELECT COALESCE(NULLIF(regions[1],''),'cn-hangzhou') FROM cloud_accounts WHERE id=$1 AND workspace_id=$2`, account.ID, account.WorkspaceID).Scan(&region); err != nil {
				return nil, err
			}
		}
		return cloud.OSSClient(region, credentials.AccessKey, credentials.SecretKey, credentials.SessionToken), nil
	})
	if err != nil {
		return nil, err
	}
	awsAdapter, err = aws.WithNetwork(awsAdapter, func(ctx context.Context, account providers.Account, region string) (aws.NetworkClients, error) {
		client, err := a.awsClient(ctx, account.WorkspaceID, account.ID, region)
		if err != nil {
			return aws.NetworkClients{}, err
		}
		return aws.NetworkClientsFor(client), nil
	})
	if err != nil {
		return nil, err
	}
	alibabaAdapter, err = alibaba.WithNetwork(alibabaAdapter, func(ctx context.Context, account providers.Account, region string) (alibaba.NetworkClients, error) {
		c, err := a.credentials(ctx, account.WorkspaceID, account.ID, "alibaba")
		if err != nil {
			return alibaba.NetworkClients{}, err
		}
		return alibaba.NewNetworkClients(region, c.AccessKey, c.SecretKey, c.SessionToken)
	})
	if err != nil {
		return nil, err
	}
	azureAdapter, err := azure.New(func(ctx context.Context, account providers.Account) (*cloud.AzureClient, error) {
		return a.azureClient(ctx, account.WorkspaceID, account.ID)
	})
	if err != nil {
		return nil, err
	}
	gcpAdapter, err := gcp.New(func(ctx context.Context, account providers.Account) (*cloud.GCPClient, error) {
		return a.gcpClient(ctx, account.WorkspaceID, account.ID)
	})
	if err != nil {
		return nil, err
	}
	return providers.NewRegistry(map[string]providers.Provider{"aws": awsAdapter, "alibaba": alibabaAdapter, "azure": azureAdapter, "gcp": gcpAdapter})
}
