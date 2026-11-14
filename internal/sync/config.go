// SPDX-License-Identifier: AGPL-3.0-only

// Package sync schedules durable collection jobs and owns sync_state. Provider
// calls are supplied by the existing application collectors, never the read path.
package sync

import (
	"fmt"
	"os"
	"time"
)

const (
	Compute        = "compute"
	Desktops       = "eds.desktops"
	Users          = "eds.users"
	Buckets        = "oss.buckets"
	SecurityGroups = "ecs.security_groups"
	JobKind        = "scheduled.sync"
)

type Config struct {
	Compute, Desktops, Users, SecurityGroups, Buckets, Empty time.Duration
	Tick, Reconcile, Discovery, Retention                    time.Duration
}

func DefaultConfig() Config {
	return Config{Compute: 5 * time.Minute, Desktops: 5 * time.Minute, Users: 5 * time.Minute,
		SecurityGroups: 15 * time.Minute, Buckets: time.Hour, Empty: 6 * time.Hour,
		Tick: 10 * time.Second, Reconcile: time.Minute, Discovery: time.Hour, Retention: 7 * 24 * time.Hour}
}

func LoadConfig() (Config, error) {
	c := DefaultConfig()
	for name, target := range map[string]*time.Duration{
		"NEXUS_SYNC_COMPUTE_INTERVAL": &c.Compute, "NEXUS_SYNC_DESKTOPS_INTERVAL": &c.Desktops,
		"NEXUS_SYNC_USERS_INTERVAL": &c.Users, "NEXUS_SYNC_SECURITY_GROUPS_INTERVAL": &c.SecurityGroups,
		"NEXUS_SYNC_BUCKETS_INTERVAL": &c.Buckets, "NEXUS_SYNC_EMPTY_INTERVAL": &c.Empty,
		"NEXUS_SYNC_TICK_INTERVAL": &c.Tick, "NEXUS_SYNC_RECONCILE_INTERVAL": &c.Reconcile,
		"NEXUS_SYNC_DISCOVERY_INTERVAL": &c.Discovery,
		"NEXUS_JOB_RETENTION":           &c.Retention,
	} {
		if value := os.Getenv(name); value != "" {
			d, err := time.ParseDuration(value)
			if err != nil || d < time.Second {
				return c, fmt.Errorf("%s must be a duration of at least 1s", name)
			}
			*target = d
		}
	}
	return c, nil
}

func (c Config) Interval(service string, empty bool) time.Duration {
	if empty {
		return c.Empty
	}
	switch service {
	case Compute:
		return c.Compute
	case Desktops:
		return c.Desktops
	case Users:
		return c.Users
	case SecurityGroups:
		return c.SecurityGroups
	case Buckets:
		return c.Buckets
	default:
		return c.Compute
	}
}
