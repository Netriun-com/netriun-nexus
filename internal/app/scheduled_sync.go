// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/netriun/nexus/internal/cloud"
	"github.com/netriun/nexus/internal/secure"
	syncer "github.com/netriun/nexus/internal/sync"
)

// NewCollector initializes the worker vault and store; it needs no Valkey.
// The caller owns db. There is no HTTP
// bootstrap, SMTP initialization, or new collection implementation here.
func NewCollector(ctx context.Context, db *pgxpool.Pool, config syncer.Config) (*App, error) {
	vault, err := secure.NewVault(os.Getenv("ENCRYPTION_KEY"))
	if err != nil {
		return nil, err
	}
	a := &App{DB: db, Vault: vault, Sync: syncer.New(db, config)}
	a.Providers, err = a.newProviderRegistry()
	if err != nil {
		return nil, err
	}
	return a, nil
}

// DiscoverSyncRegions uses existing region APIs only for an all-regions account.
// Failed services are omitted so reconciliation preserves their previous scopes.
func (a *App) DiscoverSyncRegions(ctx context.Context, account syncer.Account) map[string][]string {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	done := make(chan map[string][]string, 1)
	go func() {
		found := map[string][]string{}
		var regions []string
		var err error
		if account.Provider == "aws" {
			client, e := a.awsClient(ctx, account.WorkspaceID, account.ID, "us-east-1")
			err = e
			if err == nil {
				regions, err = cloud.Regions(ctx, client)
			}
		} else {
			client, e := a.alibabaClient(ctx, account.WorkspaceID, account.ID, "cn-hangzhou")
			err = e
			if err == nil {
				regions, err = cloud.AlibabaRegions(ctx, client)
			}
		}
		if err == nil && len(regions) > 0 {
			found[syncer.Compute] = regions
			found[syncer.SecurityGroups] = regions
		} else {
			slog.Warn("compute region discovery failed", "account_id", account.ID)
		}
		if account.Provider == "alibaba" && ctx.Err() == nil {
			credentials, seed, e := a.edsCredentials(ctx, account.WorkspaceID, account.ID)
			if e == nil {
				client, clientErr := cloud.EDSClient(seed, credentials.AccessKey, credentials.SecretKey, credentials.SessionToken)
				if clientErr == nil {
					listed, listErr := cloud.EDSRegions(ctx, client.Desktop)
					if listErr == nil && len(listed) > 0 {
						for _, region := range listed {
							found[syncer.Desktops] = append(found[syncer.Desktops], region.ID)
						}
					}
				}
			}
			if _, ok := found[syncer.Desktops]; !ok {
				slog.Warn("desktop region discovery failed", "account_id", account.ID)
			}
		}
		done <- found
	}()
	select {
	case <-ctx.Done():
		return nil
	case found := <-done:
		return found
	}
}

// CollectScheduled runs existing provider APIs with durable snapshot fencing.
func (a *App) CollectScheduled(ctx context.Context, p syncer.Payload, account syncer.Account) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	snapshotRegion := p.Region
	var err error
	switch p.Service {
	case syncer.Compute:
		if p.Region == syncer.AccountWide {
			err = a.collectGlobal(ctx, p.WorkspaceID, p.AccountID, account.Provider, account.Regions)
		} else {
			err = a.collectRegion(ctx, p.WorkspaceID, p.AccountID, account.Provider, p.Region)
		}
	case syncer.Desktops:
		err = a.collectEDSDesktops(ctx, p.WorkspaceID, p.AccountID, p.Region)
	case syncer.Users:
		err = a.collectEDSUsers(ctx, p.WorkspaceID, p.AccountID, p.Region)
	case syncer.Buckets:
		err = a.collectOSSBuckets(ctx, p.WorkspaceID, p.AccountID)
	case syncer.SecurityGroups:
		err = a.collectSecurityGroups(ctx, p.WorkspaceID, p.AccountID, p.Region)
	default:
		return false, errors.New("Unsupported scheduled service")
	}
	if err != nil {
		return false, err
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	return a.Sync.Result(ctx, p, snapshotRegion)
}
