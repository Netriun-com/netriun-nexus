// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/netriun/nexus/internal/cloud"
)

// Azure and GCP expose subscription/project-wide compute inventory endpoints.
// Fetching once avoids duplicating every VM for each configured region.
func (a *App) collectGlobal(ctx context.Context, workspaceID, accountID int64, provider string, regions []string) error {
	requestCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	var items []cloud.Instance
	var err error
	switch provider {
	case "azure":
		var client *cloud.AzureClient
		client, err = a.azureClient(requestCtx, workspaceID, accountID)
		if err == nil {
			items, err = cloud.AzureInventory(requestCtx, client)
		}
	case "gcp":
		var client *cloud.GCPClient
		client, err = a.gcpClient(requestCtx, workspaceID, accountID)
		if err == nil {
			items, err = cloud.GCPInventory(requestCtx, client)
		}
	default:
		return fmt.Errorf("unsupported global provider %q", provider)
	}
	if err != nil {
		return err
	}
	if len(regions) > 0 {
		items = slices.DeleteFunc(items, func(item cloud.Instance) bool { return !slices.Contains(regions, item.Region) })
	}
	return a.Sync.ReconcileInventory(requestCtx, accountID, items, true, "")
}

func (a *App) collectRegion(ctx context.Context, workspaceID, accountID int64, provider, region string) error {
	regionCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	var items []cloud.Instance
	var err error
	if provider == "aws" {
		c, clientErr := a.awsClient(regionCtx, workspaceID, accountID, region)
		err = clientErr
		if err == nil {
			items, err = cloud.Inventory(regionCtx, c)
		}
	} else if provider == "alibaba" {
		c, clientErr := a.alibabaClient(regionCtx, workspaceID, accountID, region)
		err = clientErr
		if err == nil {
			items, err = cloud.AlibabaInventory(regionCtx, c, region)
		}
	} else {
		return fmt.Errorf("unsupported provider %q", provider)
	}
	if err != nil {
		return err
	}
	for index := range items {
		items[index].Region = region
	}
	return a.Sync.ReconcileInventory(regionCtx, accountID, items, false, region)
}
