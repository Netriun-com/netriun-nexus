// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/netriun/nexus/internal/secure"
	"log/slog"
	"net"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/netriun/nexus/internal/cloud"
)

var securityGroupIDPattern = regexp.MustCompile(`^sg-[A-Za-z0-9-]{1,128}$`)
var securityGroupRuleIDPattern = regexp.MustCompile(`^sgr-[A-Za-z0-9-]{1,128}$`)

type securityGroupRefreshJob struct {
	ID            string     `json:"id"`
	WorkspaceID   int64      `json:"-"`
	RequestedBy   int64      `json:"-"`
	RequestedName string     `json:"-"`
	AccountID     int64      `json:"account_id"`
	Status        string     `json:"status"`
	CreatedAt     time.Time  `json:"created_at"`
	StartedAt     *time.Time `json:"started_at,omitempty"`
	CompletedAt   *time.Time `json:"completed_at,omitempty"`
	DurationMS    int64      `json:"duration_ms,omitempty"`
	Error         string     `json:"error,omitempty"`
}

func (a *App) securityGroupAccount(w http.ResponseWriter, r *http.Request, mode string) (int64, bool) {
	accountID, ok := pathID(w, r, "id")
	if !ok {
		return 0, false
	}
	if !a.accountAccess(r, accountID, mode) {
		problem(w, http.StatusForbidden, "Cloud account access required")
		return 0, false
	}
	var provider string
	if err := a.DB.QueryRow(r.Context(), `SELECT provider FROM cloud_accounts WHERE id=$1 AND workspace_id=$2`, accountID, current(r).WorkspaceID).Scan(&provider); err != nil {
		dbError(w, err)
		return 0, false
	}
	if provider != "alibaba" {
		problem(w, http.StatusBadRequest, "The selected account is not an Alibaba Cloud connection")
		return 0, false
	}
	return accountID, true
}

func (a *App) securityGroups(w http.ResponseWriter, r *http.Request) {
	accountID, ok := a.securityGroupAccount(w, r, "view")
	if !ok {
		return
	}
	payload, _, err := a.serviceSnapshot(r.Context(), current(r).WorkspaceID, accountID, "ecs.security_groups", "")
	if err != nil {
		dbError(w, err)
		return
	}
	write(w, http.StatusOK, payload)
}

func (a *App) instanceSecurityGroups(ctx context.Context, workspaceID, accountID int64, region string, raw json.RawMessage) []map[string]string {
	var instance struct {
		Details struct {
			SecurityGroupIDs []string `json:"security_group_ids"`
		} `json:"details"`
	}
	if json.Unmarshal(raw, &instance) != nil || len(instance.Details.SecurityGroupIDs) == 0 {
		return []map[string]string{}
	}
	byID := map[string]cloud.AlibabaSecurityGroup{}
	if payload, exists, err := a.serviceSnapshot(ctx, workspaceID, accountID, "ecs.security_groups", ""); err == nil && exists {
		encoded, _ := json.Marshal(payload["data"])
		var groups []cloud.AlibabaSecurityGroup
		if json.Unmarshal(encoded, &groups) == nil {
			for _, group := range groups {
				if group.Region == region {
					byID[group.ID] = group
				}
			}
		}
	}
	result := make([]map[string]string, 0, len(instance.Details.SecurityGroupIDs))
	for _, id := range instance.Details.SecurityGroupIDs {
		name := id
		if group, ok := byID[id]; ok && group.Name != "" {
			name = group.Name
		}
		result = append(result, map[string]string{"id": id, "name": name, "region": region})
	}
	return result
}

func (a *App) collectSecurityGroups(ctx context.Context, workspaceID, accountID int64, region string) error {
	client, err := a.alibabaClient(ctx, workspaceID, accountID, region)
	if err != nil {
		return err
	}
	items, err := cloud.AlibabaSecurityGroups(ctx, client, region)
	if err != nil {
		return err
	}
	slots := make(chan struct{}, 8)
	failures := make(chan error, len(items))
	var wg sync.WaitGroup
	for index := range items {
		wg.Go(func() {
			select {
			case slots <- struct{}{}:
			case <-ctx.Done():
				failures <- ctx.Err()
				return
			}
			defer func() { <-slots }()
			rules, err := cloud.AlibabaSecurityGroupRules(ctx, client, region, items[index].ID)
			if err != nil {
				failures <- err
				return
			}
			items[index].Rules, items[index].RulesAvailable = rules, true
		})
	}
	wg.Wait()
	close(failures)
	var failed []error
	for err := range failures {
		failed = append(failed, err)
	}
	if err := errors.Join(failed...); err != nil {
		return err
	}

	sort.Slice(items, func(i, j int) bool { return items[i].Name < items[j].Name })
	return a.Sync.SaveServiceSnapshot(ctx, workspaceID, accountID, "ecs.security_groups", region, map[string]any{"data": items, "failed_regions": []string{}})
}

type securityGroupRuleRequest struct {
	Region      string `json:"region"`
	Direction   string `json:"direction"`
	Protocol    string `json:"protocol"`
	PortFrom    int    `json:"port_from"`
	PortTo      int    `json:"port_to"`
	CIDR        string `json:"cidr"`
	Policy      string `json:"policy"`
	Priority    int    `json:"priority"`
	Description string `json:"description"`
}

func validateSecurityGroupRule(in *securityGroupRuleRequest) (cloud.AlibabaSecurityGroupRuleInput, string) {
	in.Region = strings.TrimSpace(in.Region)
	in.Direction = strings.ToLower(strings.TrimSpace(in.Direction))
	in.Protocol = strings.ToLower(strings.TrimSpace(in.Protocol))
	in.CIDR = strings.TrimSpace(in.CIDR)
	in.Policy = strings.ToLower(strings.TrimSpace(in.Policy))
	in.Description = strings.TrimSpace(in.Description)
	if !regionPattern.MatchString(in.Region) {
		return cloud.AlibabaSecurityGroupRuleInput{}, "Select a valid Alibaba Cloud region"
	}
	if in.Direction != "ingress" && in.Direction != "egress" {
		return cloud.AlibabaSecurityGroupRuleInput{}, "Direction must be ingress or egress"
	}
	if in.Protocol != "tcp" && in.Protocol != "udp" && in.Protocol != "icmp" && in.Protocol != "all" {
		return cloud.AlibabaSecurityGroupRuleInput{}, "Protocol must be tcp, udp, icmp or all"
	}
	if _, _, err := net.ParseCIDR(in.CIDR); err != nil {
		return cloud.AlibabaSecurityGroupRuleInput{}, "Enter a valid IPv4 or IPv6 CIDR block"
	}
	if in.Policy != "accept" && in.Policy != "drop" {
		return cloud.AlibabaSecurityGroupRuleInput{}, "Policy must be accept or drop"
	}
	if in.Priority < 1 || in.Priority > 100 {
		return cloud.AlibabaSecurityGroupRuleInput{}, "Priority must be between 1 and 100"
	}
	if utf8.RuneCountInString(in.Description) > 512 {
		return cloud.AlibabaSecurityGroupRuleInput{}, "Description cannot exceed 512 characters"
	}
	portRange := "-1/-1"
	if in.Protocol == "tcp" || in.Protocol == "udp" {
		if in.PortFrom < 1 || in.PortFrom > 65535 || in.PortTo < in.PortFrom || in.PortTo > 65535 {
			return cloud.AlibabaSecurityGroupRuleInput{}, "Use a valid port range between 1 and 65535"
		}
		portRange = fmt.Sprintf("%d/%d", in.PortFrom, in.PortTo)
	}
	return cloud.AlibabaSecurityGroupRuleInput{Direction: in.Direction, Protocol: in.Protocol, PortRange: portRange, CIDR: in.CIDR, Policy: in.Policy, Priority: strconv.Itoa(in.Priority), Description: in.Description, ClientToken: secure.Token()}, ""
}

func (a *App) createSecurityGroupRule(w http.ResponseWriter, r *http.Request) {
	accountID, ok := a.securityGroupAccount(w, r, "manage")
	if !ok {
		return
	}
	groupID := r.PathValue("groupID")
	if !securityGroupIDPattern.MatchString(groupID) {
		problem(w, http.StatusBadRequest, "Invalid security group ID")
		return
	}
	var in securityGroupRuleRequest
	if !decode(w, r, &in) {
		return
	}
	rule, message := validateSecurityGroupRule(&in)
	if message != "" {
		problem(w, http.StatusBadRequest, message)
		return
	}
	u := current(r)
	if err := a.record(r.Context(), u.WorkspaceID, u.ID, u.Username, "ecs.security_group_rule.create_requested", groupID, map[string]any{"account_id": accountID, "region": in.Region, "direction": in.Direction, "protocol": in.Protocol, "port_range": rule.PortRange, "cidr": in.CIDR, "policy": in.Policy, "priority": in.Priority}); err != nil {
		problem(w, http.StatusServiceUnavailable, "Rule was not submitted because audit storage is unavailable")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	client, err := a.alibabaClient(ctx, u.WorkspaceID, accountID, in.Region)
	if err == nil {
		err = cloud.AlibabaAuthorizeSecurityGroupRule(ctx, client, in.Region, groupID, rule)
	}
	if err != nil {
		slog.Warn("Alibaba security group rule creation failed", "account_id", accountID, "region", in.Region, "security_group_id", groupID, "error", err)
		a.audit(r, "ecs.security_group_rule.create_failed", groupID, map[string]any{"account_id": accountID, "region": in.Region})
		problem(w, http.StatusBadGateway, "Alibaba ECS rejected the rule; check RAM permissions, CIDR, ports, and security group state")
		return
	}
	a.audit(r, "ecs.security_group_rule.created", groupID, map[string]any{"account_id": accountID, "region": in.Region})
	write(w, http.StatusCreated, map[string]string{"status": "created"})
}

func (a *App) deleteSecurityGroupRule(w http.ResponseWriter, r *http.Request) {
	accountID, ok := a.securityGroupAccount(w, r, "manage")
	if !ok {
		return
	}
	groupID, ruleID := r.PathValue("groupID"), r.PathValue("ruleID")
	region, direction := strings.TrimSpace(r.URL.Query().Get("region")), strings.ToLower(strings.TrimSpace(r.URL.Query().Get("direction")))
	if !securityGroupIDPattern.MatchString(groupID) || !securityGroupRuleIDPattern.MatchString(ruleID) {
		problem(w, http.StatusBadRequest, "Invalid security group or rule ID")
		return
	}
	if !regionPattern.MatchString(region) || (direction != "ingress" && direction != "egress") {
		problem(w, http.StatusBadRequest, "A valid region and rule direction are required")
		return
	}
	u := current(r)
	if err := a.record(r.Context(), u.WorkspaceID, u.ID, u.Username, "ecs.security_group_rule.delete_requested", ruleID, map[string]any{"account_id": accountID, "security_group_id": groupID, "region": region, "direction": direction}); err != nil {
		problem(w, http.StatusServiceUnavailable, "Rule was not deleted because audit storage is unavailable")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	client, err := a.alibabaClient(ctx, u.WorkspaceID, accountID, region)
	if err == nil {
		err = cloud.AlibabaRevokeSecurityGroupRule(ctx, client, region, groupID, ruleID, direction, secure.Token())
	}
	if err != nil {
		slog.Warn("Alibaba security group rule deletion failed", "account_id", accountID, "region", region, "security_group_id", groupID, "rule_id", ruleID, "error", err)
		a.audit(r, "ecs.security_group_rule.delete_failed", ruleID, map[string]any{"account_id": accountID, "security_group_id": groupID, "region": region})
		problem(w, http.StatusBadGateway, "Alibaba ECS rejected the deletion; check RAM permissions and rule state")
		return
	}
	a.audit(r, "ecs.security_group_rule.deleted", ruleID, map[string]any{"account_id": accountID, "security_group_id": groupID, "region": region})
	w.WriteHeader(http.StatusNoContent)
}
