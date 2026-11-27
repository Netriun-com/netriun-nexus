// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"net/http"
	"sort"
	"time"

	"github.com/netriun/nexus/internal/resources"
)

type reportAccount struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	Provider  string `json:"provider"`
	Instances int    `json:"instances"`
	Running   int    `json:"running_instances"`
}

type reportDimension struct {
	Key          string `json:"key"`
	Accounts     int    `json:"accounts,omitempty"`
	Instances    int    `json:"instances"`
	Running      int    `json:"running_instances"`
	Desktops     int    `json:"desktops"`
	Buckets      int    `json:"buckets"`
	StorageBytes int64  `json:"storage_bytes"`
	Objects      int64  `json:"objects"`
}

// reportsOverview summarizes the latest healthy database snapshots. It does
// not estimate cloud spend: exact billing remains unavailable until a provider
// cost API is connected and its additional permissions are explicitly granted.
func (a *App) reportsOverview(w http.ResponseWriter, r *http.Request) {
	u := current(r)
	ids, err := a.Policy.AccessibleAccountIDs(r.Context(), u, CapabilityAccountView)
	if err != nil {
		dbError(w, err)
		return
	}
	ids, ok := a.requestedAccountIDs(w, r, ids)
	if !ok {
		return
	}
	accounts := []reportAccount{}
	providerStats := map[string]*reportDimension{}
	accountProviders := map[int64]string{}
	rows, err := resources.New(a.DB).ReportAccounts(r.Context(), u.WorkspaceID, ids)
	if err != nil {
		dbError(w, err)
		return
	}
	for rows.Next() {
		var account reportAccount
		if err = rows.Scan(&account.ID, &account.Name, &account.Provider, &account.Instances, &account.Running); err != nil {
			rows.Close()
			dbError(w, err)
			return
		}
		accounts = append(accounts, account)
		accountProviders[account.ID] = account.Provider
		stats := providerStats[account.Provider]
		if stats == nil {
			stats = &reportDimension{Key: account.Provider}
			providerStats[account.Provider] = stats
		}
		stats.Accounts++
		stats.Instances += account.Instances
		stats.Running += account.Running
	}
	if err = rows.Err(); err != nil {
		dbError(w, err)
		return
	}
	rows.Close()

	regionStats := map[string]*reportDimension{}
	stateStats := map[string]int{}
	var latestInstance *time.Time
	rows, err = resources.New(a.DB).ReportInstances(r.Context(), u.WorkspaceID, ids)
	if err != nil {
		dbError(w, err)
		return
	}
	for rows.Next() {
		var region, state string
		var count int
		var seen time.Time
		if err = rows.Scan(&region, &state, &count, &seen); err != nil {
			rows.Close()
			dbError(w, err)
			return
		}
		if region == "" {
			region = "unknown"
		}
		stats := regionStats[region]
		if stats == nil {
			stats = &reportDimension{Key: region}
			regionStats[region] = stats
		}
		stats.Instances += count
		if state == "running" {
			stats.Running += count
		}
		stateStats[state] += count
		if latestInstance == nil || seen.After(*latestInstance) {
			copy := seen
			latestInstance = &copy
		}
	}
	if err = rows.Err(); err != nil {
		dbError(w, err)
		return
	}
	rows.Close()

	var desktops, buckets int
	var storageBytes, objects int64
	chargeTypes := map[string]int{}
	latestService, err := resources.New(a.DB).ServiceFreshness(r.Context(), u.WorkspaceID, ids)
	if err != nil {
		dbError(w, err)
		return
	}
	rows, err = resources.New(a.DB).ReportServices(r.Context(), u.WorkspaceID, ids)
	if err != nil {
		dbError(w, err)
		return
	}
	for rows.Next() {
		var account int64
		var service, region, charge string
		var count int
		var used, numObjects int64
		var seen time.Time
		if err := rows.Scan(&account, &service, &region, &charge, &count, &used, &numObjects, &seen); err != nil {
			rows.Close()
			dbError(w, err)
			return
		}
		if latestService == nil || seen.After(*latestService) {
			copy := seen
			latestService = &copy
		}
		providerStat := providerStats[accountProviders[account]]
		regionStat := regionStats[region]
		if regionStat == nil {
			regionStat = &reportDimension{Key: region}
			regionStats[region] = regionStat
		}
		switch service {
		case "eds.desktops":
			desktops += count
			providerStat.Desktops += count
			regionStat.Desktops += count
			chargeTypes[charge] += count
		case "oss.buckets":
			buckets += count
			providerStat.Buckets += count
			regionStat.Buckets += count
			storageBytes += used
			objects += numObjects
			providerStat.StorageBytes += used
			providerStat.Objects += numObjects
			regionStat.StorageBytes += used
			regionStat.Objects += numObjects
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		dbError(w, err)
		return
	}
	rows.Close()

	providers := make([]reportDimension, 0, len(providerStats))
	for _, key := range []string{"aws", "alibaba", "azure", "gcp"} {
		if stats := providerStats[key]; stats != nil {
			providers = append(providers, *stats)
		}
	}
	regions := make([]reportDimension, 0, len(regionStats))
	for _, stats := range regionStats {
		regions = append(regions, *stats)
	}
	sort.Slice(regions, func(i, j int) bool { return regions[i].Key < regions[j].Key })
	states := make([]map[string]any, 0, len(stateStats))
	stateKeys := make([]string, 0, len(stateStats))
	for state := range stateStats {
		stateKeys = append(stateKeys, state)
	}
	sort.Strings(stateKeys)
	for _, state := range stateKeys {
		count := stateStats[state]
		states = append(states, map[string]any{"key": state, "count": count})
	}
	instances, running := 0, 0
	for _, account := range accounts {
		instances += account.Instances
		running += account.Running
	}
	write(w, 200, map[string]any{
		"generated_at": time.Now().UTC(),
		"accounts":     accounts,
		"resources": map[string]any{
			"total": instances + desktops + buckets, "instances": instances, "running_instances": running,
			"desktops": desktops, "buckets": buckets, "storage_bytes": storageBytes, "objects": objects,
		},
		"by_provider": providers,
		"by_region":   regions,
		"by_state":    states,
		"billing": map[string]any{
			"exact_spend_available": false,
			"eds_charge_types":      chargeTypes,
			"message":               "Exact charges are not shown until provider billing APIs are connected with separate cost-read permissions.",
		},
		"freshness": map[string]any{"instances": latestInstance, "services": latestService},
	})
}
