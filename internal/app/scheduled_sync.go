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
	"github.com/redis/go-redis/v9"
)

// NewCollector initializes only the dependencies needed by the old collectors.
// The caller owns db and must close the returned Redis client. There is no HTTP
// bootstrap, SMTP initialization, or new collection implementation here.
func NewCollector(ctx context.Context, db *pgxpool.Pool, config syncer.Config) (*App, error) {
	vault, err := secure.NewVault(os.Getenv("ENCRYPTION_KEY"))
	if err != nil {
		return nil, err
	}
	options, err := redis.ParseURL(os.Getenv("REDIS_URL"))
	if err != nil {
		return nil, err
	}
	rc := redis.NewClient(options)
	if err := rc.Ping(ctx).Err(); err != nil {
		rc.Close()
		return nil, err
	}
	return &App{DB: db, Redis: rc, Vault: vault, Sync: syncer.New(db, config), regionSlots: make(chan struct{}, 6)}, nil
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

// CollectScheduled delegates to today's collectors. Compute uses durable lease
// fencing; the other services keep their existing Valkey locks. TODO(T14): replace the '*' service jobs with regional collectors;
// do not filter or rewrite whole-account snapshots in this bridge.
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
		err = a.withServiceCollectorLock(ctx, p.WorkspaceID, p.AccountID, "eds", func(locked context.Context) error {
			return a.collectEDSDesktops(locked, p.WorkspaceID, p.AccountID, p.Region, nil)
		})
	case syncer.Users:
		_, snapshotRegion, err = a.edsCredentials(ctx, p.WorkspaceID, p.AccountID)
		if err == nil {
			err = a.refreshEDSService(ctx, p.WorkspaceID, p.AccountID, "users", snapshotRegion)
		}
		// refreshEDSService may also update desktop snapshots. Only the users
		// sync_state row is completed by this job; desktops have their own jobs.
	case syncer.Buckets:
		snapshotRegion = ""
		err = a.withServiceCollectorLock(ctx, p.WorkspaceID, p.AccountID, "oss", func(locked context.Context) error { return a.collectOSSBuckets(locked, p.WorkspaceID, p.AccountID) })
	case syncer.SecurityGroups:
		snapshotRegion = ""
		err = a.withServiceCollectorLock(ctx, p.WorkspaceID, p.AccountID, "ecs-security-groups", func(locked context.Context) error { return a.collectSecurityGroups(locked, p.WorkspaceID, p.AccountID) })
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
