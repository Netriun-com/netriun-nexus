// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"fmt"
	"os"
	"strconv"
	"time"

	syncer "github.com/netriun/nexus/internal/sync"
	"github.com/netriun/nexus/internal/worker"
)

type config struct {
	role          string
	address       string
	worker        worker.Config
	accountLimit  int
	providerLimit int
	sync          syncer.Config
}

func loadConfig() (config, error) {
	c := config{role: os.Getenv("NEXUS_ROLE"), address: os.Getenv("HTTP_ADDR"), worker: worker.DefaultConfig(), accountLimit: 2, providerLimit: 4}
	var err error
	c.sync, err = syncer.LoadConfig()
	if err != nil {
		return c, err
	}
	if c.role == "" {
		c.role = "all"
	}
	if c.role != "serve" && c.role != "worker" && c.role != "all" {
		return c, fmt.Errorf("NEXUS_ROLE must be serve, worker, or all")
	}
	if c.address == "" {
		c.address = ":8080"
	}
	for name, target := range map[string]*int{
		"NEXUS_WORKERS":              &c.worker.Concurrency,
		"NEXUS_ACCOUNT_CONCURRENCY":  &c.accountLimit,
		"NEXUS_PROVIDER_CONCURRENCY": &c.providerLimit,
	} {
		if value := os.Getenv(name); value != "" {
			n, err := strconv.Atoi(value)
			if err != nil || n < 1 {
				return c, fmt.Errorf("%s must be a positive integer", name)
			}
			*target = n
		}
	}
	if value := os.Getenv("NEXUS_SHUTDOWN_TIMEOUT"); value != "" {
		d, err := time.ParseDuration(value)
		if err != nil || d <= 0 {
			return c, fmt.Errorf("NEXUS_SHUTDOWN_TIMEOUT must be a positive duration")
		}
		c.worker.ShutdownTimeout = d
	}
	return c, nil
}
