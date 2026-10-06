// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
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

type reportSnapshot struct {
	AccountID int64
	Service   string
	Region    string
	Payload   []byte
	FetchedAt time.Time
}

func reportString(item map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := item[key].(string); ok {
			return value
		}
	}
	return ""
}

func reportInt64(item map[string]any, key string) int64 {
	switch value := item[key].(type) {
	case float64:
		return int64(value)
	case json.Number:
		result, _ := value.Int64()
		return result
	default:
		return 0
	}
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
	rows, err := a.DB.Query(r.Context(), `SELECT a.id,a.name,a.provider,count(i.id),count(i.id) FILTER(WHERE lower(i.state)='running') FROM cloud_accounts a LEFT JOIN instances i ON i.account_id=a.id WHERE a.workspace_id=$1 AND a.id=ANY($2) GROUP BY a.id,a.name,a.provider ORDER BY a.provider,a.name`, u.WorkspaceID, ids)
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
	rows, err = a.DB.Query(r.Context(), `SELECT i.region,lower(i.state),count(*),max(i.seen_at) FROM instances i JOIN cloud_accounts a ON a.id=i.account_id WHERE a.workspace_id=$1 AND a.id=ANY($2) GROUP BY i.region,lower(i.state) ORDER BY i.region,lower(i.state)`, u.WorkspaceID, ids)
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

	snapshots := []reportSnapshot{}
	rows, err = a.DB.Query(r.Context(), `SELECT account_id,service_key,region,payload,fetched_at FROM service_snapshots WHERE workspace_id=$1 AND account_id=ANY($2) AND service_key IN ('eds.desktops','oss.buckets') ORDER BY account_id,service_key,region`, u.WorkspaceID, ids)
	if err != nil {
		dbError(w, err)
		return
	}
	edsAll := map[int64]bool{}
	for rows.Next() {
		var snapshot reportSnapshot
		if err = rows.Scan(&snapshot.AccountID, &snapshot.Service, &snapshot.Region, &snapshot.Payload, &snapshot.FetchedAt); err != nil {
			rows.Close()
			dbError(w, err)
			return
		}
		snapshots = append(snapshots, snapshot)
		if snapshot.Service == "eds.desktops" && snapshot.Region == "all" {
			edsAll[snapshot.AccountID] = true
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
	seenDesktops := map[string]bool{}
	var latestService *time.Time
	for _, snapshot := range snapshots {
		if snapshot.Service == "eds.desktops" && edsAll[snapshot.AccountID] && snapshot.Region != "all" {
			continue
		}
		var envelope struct {
			Data []map[string]any `json:"data"`
		}
		decoder := json.NewDecoder(strings.NewReader(string(snapshot.Payload)))
		decoder.UseNumber()
		if decoder.Decode(&envelope) != nil {
			continue
		}
		if latestService == nil || snapshot.FetchedAt.After(*latestService) {
			copy := snapshot.FetchedAt
			latestService = &copy
		}
		provider := accountProviders[snapshot.AccountID]
		providerStat := providerStats[provider]
		for _, item := range envelope.Data {
			region := reportString(item, "RegionId", "region", "location")
			if region == "" {
				region = snapshot.Region
			}
			if region == "" || region == "all" {
				region = "unknown"
			}
			regionStat := regionStats[region]
			if regionStat == nil {
				regionStat = &reportDimension{Key: region}
				regionStats[region] = regionStat
			}
			switch snapshot.Service {
			case "eds.desktops":
				id := reportString(item, "DesktopId")
				key := strconv.FormatInt(snapshot.AccountID, 10) + "\x00" + id
				if id != "" && seenDesktops[key] {
					continue
				}
				seenDesktops[key] = true
				desktops++
				providerStat.Desktops++
				regionStat.Desktops++
				charge := reportString(item, "ChargeType")
				if charge == "" {
					charge = "Unknown"
				}
				chargeTypes[charge]++
			case "oss.buckets":
				buckets++
				providerStat.Buckets++
				regionStat.Buckets++
				used, count := reportInt64(item, "storage_bytes"), reportInt64(item, "object_count")
				storageBytes += used
				objects += count
				providerStat.StorageBytes += used
				providerStat.Objects += count
				regionStat.StorageBytes += used
				regionStat.Objects += count
			}
		}
	}

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
