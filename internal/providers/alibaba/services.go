// SPDX-License-Identifier: AGPL-3.0-only

package alibaba

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	oss "github.com/aliyun/alibabacloud-oss-go-sdk-v2/oss"
	"github.com/netriun/nexus/internal/cloud"
	"github.com/netriun/nexus/internal/providers"
)

type EDSClientFactory func(context.Context, providers.Account, string) (*cloud.EDSClients, error)

// OSSClientFactory receives "*" for account-wide enumeration, then the bucket's
// own region for detail/stat calls. The caller chooses the enumeration endpoint.
type OSSClientFactory func(context.Context, providers.Account, string) (*oss.Client, error)

type servicesAdapter struct {
	compute providers.Provider
	ecs     ClientFactory
	eds     EDSClientFactory
	oss     OSSClientFactory
}

// NewWithServices wraps the existing collectors for compute, desktops, directory
// users, buckets and security groups. New remains the compute-only constructor.
// ECS and OSS factories must return independent clients for concurrent details.
func NewWithServices(ecs ClientFactory, eds EDSClientFactory, oss OSSClientFactory) (providers.Provider, error) {
	compute, err := New(ecs)
	if err != nil {
		return nil, err
	}
	if eds == nil || oss == nil {
		return nil, errors.New("Alibaba EDS and OSS client factories are required")
	}
	return &servicesAdapter{compute: compute, ecs: ecs, eds: eds, oss: oss}, nil
}

func (a *servicesAdapter) Services() []providers.ServiceSpec {
	return append(a.compute.Services(),
		providers.ServiceSpec{Name: providers.EDSDesktops, Kinds: []string{providers.Desktop}, Regional: true, DefaultInterval: 5 * time.Minute},
		providers.ServiceSpec{Name: providers.EDSUsers, Kinds: []string{providers.DirectoryUser}, Regional: true, DefaultInterval: 5 * time.Minute},
		providers.ServiceSpec{Name: providers.OSSBuckets, Kinds: []string{providers.Bucket}, DefaultInterval: time.Hour},
		providers.ServiceSpec{Name: providers.SecurityGroups, Kinds: []string{providers.SecurityGroup}, Regional: true, DefaultInterval: 15 * time.Minute},
	)
}

func (a *servicesAdapter) List(ctx context.Context, account providers.Account, service, region string) ([]providers.Resource, []providers.Relation, error) {
	if service == providers.Compute {
		return a.compute.List(ctx, account, service, region)
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	if account.WorkspaceID <= 0 || account.ID <= 0 || account.Provider != "alibaba" {
		return nil, nil, errors.New("invalid Alibaba account identity")
	}
	if service != providers.OSSBuckets && (region == "" || region == "*" || region == "all") {
		return nil, nil, errors.New("Alibaba service requires a concrete region")
	}
	var items []providers.Resource
	var err error
	switch service {
	case providers.EDSDesktops, providers.EDSUsers:
		items, err = a.listEDS(ctx, account, service, region)
	case providers.OSSBuckets:
		if region != "" && region != "*" {
			return nil, nil, errors.New("OSS collection must cover the whole account")
		}
		items, err = a.listBuckets(ctx, account)
	case providers.SecurityGroups:
		items, err = a.listSecurityGroups(ctx, account, region)
	default:
		return nil, nil, fmt.Errorf("%w: service %q", providers.ErrUnsupported, service)
	}
	if err != nil {
		return nil, nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	seen := time.Now().UTC()
	for i := range items {
		items[i].WorkspaceID, items[i].AccountID, items[i].Provider, items[i].Service = account.WorkspaceID, account.ID, "alibaba", service
		items[i].SeenAt = seen
	}
	return items, []providers.Relation{}, nil
}

func (a *servicesAdapter) Get(ctx context.Context, account providers.Account, kind, id string) (providers.Resource, error) {
	if kind == providers.Instance {
		return a.compute.Get(ctx, account, kind, id)
	}
	service := ""
	for _, spec := range a.Services() {
		if spec.Kinds[0] == kind {
			service = spec.Name
			break
		}
	}
	if service == "" {
		return providers.Resource{}, fmt.Errorf("%w: kind %q", providers.ErrUnsupported, kind)
	}
	if id == "" {
		return providers.Resource{}, errors.New("resource ID is required")
	}
	region := account.Region
	if service == providers.OSSBuckets {
		region = "*"
	}
	items, _, err := a.List(ctx, account, service, region)
	if err != nil {
		return providers.Resource{}, err
	}
	for _, item := range items {
		if item.ProviderID == id {
			return item, nil
		}
	}
	return providers.Resource{}, fmt.Errorf("%w: %s %q", providers.ErrNotFound, kind, id)
}

// Keep the legacy item intact in details so the dual-write can emit precisely
// the same snapshot JSON. UseNumber preserves int64 counters and directory IDs.
func serviceResource(kind, region, id, name, state, nativeType string, value any, summary map[string]any) (providers.Resource, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return providers.Resource{}, err
	}
	var details map[string]any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&details); err != nil {
		return providers.Resource{}, err
	}
	if id == "" || region == "" || region == "*" || details == nil {
		return providers.Resource{}, errors.New("Alibaba returned a resource without identity or region")
	}
	summary["provider_type"] = nativeType
	return providers.Resource{Kind: kind, Region: region, ProviderID: id, Name: name, State: state, Tags: map[string]string{}, Summary: summary, Details: details}, nil
}

func (a *servicesAdapter) listEDS(ctx context.Context, account providers.Account, service, region string) ([]providers.Resource, error) {
	if service == providers.EDSUsers {
		region = cloud.EDSUserRegion(region)
	}
	client, err := a.eds(ctx, account, region)
	if err != nil {
		return nil, err
	}
	if client == nil {
		return nil, errors.New("Alibaba EDS client factory returned nil")
	}
	result := []providers.Resource{}
	if service == providers.EDSDesktops {
		if client.Desktop == nil {
			return nil, errors.New("Alibaba desktop client is missing")
		}
		items, err := cloud.EDSDesktops(ctx, client.Desktop, region)
		if err != nil {
			return nil, err
		}
		for _, item := range items {
			r, err := serviceResource(providers.Desktop, region, stringValue(item.DesktopId), stringValue(item.DesktopName), strings.ToLower(stringValue(item.DesktopStatus)), "WUYING desktop", item, map[string]any{"desktop_type": stringValue(item.DesktopType), "office_site_id": stringValue(item.OfficeSiteId), "charge_type": stringValue(item.ChargeType)})
			if err != nil {
				return nil, err
			}
			for _, tag := range item.Tags {
				if tag != nil {
					r.Tags[stringValue(tag.Key)] = stringValue(tag.Value)
				}
			}
			result = append(result, r)
		}
	} else {
		if client.User == nil {
			return nil, errors.New("Alibaba directory client is missing")
		}
		items, err := cloud.EDSUsers(ctx, client.User)
		if err != nil {
			return nil, err
		}
		for _, item := range items {
			if item == nil {
				return nil, errors.New("Alibaba returned an empty directory user")
			}
			name := stringValue(item.RealNickName)
			if name == "" {
				name = stringValue(item.EndUserId)
			}
			state := "unknown"
			if item.Status != nil {
				switch *item.Status {
				case 0:
					state = "normal"
				case 9:
					state = "locked"
				default:
					state = strconv.FormatInt(int64(*item.Status), 10)
				}
			}
			r, err := serviceResource(providers.DirectoryUser, region, stringValue(item.EndUserId), name, state, "WUYING directory user", item, map[string]any{"email": stringValue(item.Email)})
			if err != nil {
				return nil, err
			}
			result = append(result, r)
		}
	}
	return result, nil
}

func (a *servicesAdapter) listBuckets(ctx context.Context, account providers.Account) ([]providers.Resource, error) {
	client, err := a.oss(ctx, account, "*")
	if err != nil {
		return nil, err
	}
	if client == nil {
		return nil, errors.New("Alibaba OSS client factory returned nil")
	}
	items, err := cloud.OSSListBuckets(ctx, client)
	if err != nil {
		return nil, err
	}
	// Same bounded detail/stat fan-out as the previous snapshot collector. A
	// single failed bucket rejects the entire account-wide result.
	err = completeDetails(ctx, len(items), 6, func(i int) error {
		client, err := a.oss(ctx, account, items[i].Region)
		if err != nil {
			return err
		}
		if client == nil {
			return errors.New("Alibaba OSS client factory returned nil")
		}
		if err := cloud.OSSBucketDetails(ctx, client, &items[i]); err != nil {
			return err
		}
		if err := cloud.OSSBucketStats(ctx, client, &items[i]); err != nil {
			return err
		}
		items[i].Status = "available"
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Name < items[j].Name })
	result := make([]providers.Resource, 0, len(items))
	for _, item := range items {
		r, err := serviceResource(providers.Bucket, item.Region, item.Name, item.Name, item.Status, "OSS bucket", item, map[string]any{"storage_class": item.StorageClass, "storage_bytes": item.StorageBytes, "object_count": item.ObjectCount, "acl": item.ACL})
		if err != nil {
			return nil, err
		}
		result = append(result, r)
	}
	return result, nil
}

func (a *servicesAdapter) listSecurityGroups(ctx context.Context, account providers.Account, region string) ([]providers.Resource, error) {
	client, err := a.ecs(ctx, account, region)
	if err != nil {
		return nil, err
	}
	if client == nil {
		return nil, errors.New("Alibaba ECS client factory returned nil")
	}
	items, err := cloud.AlibabaSecurityGroups(ctx, client, region)
	if err != nil {
		return nil, err
	}
	err = completeDetails(ctx, len(items), 8, func(i int) error {
		// The ECS SDK mutates its RPC headers even on reads. Do not share the
		// listing client across concurrent rule requests.
		client, err := a.ecs(ctx, account, region)
		if err != nil {
			return err
		}
		if client == nil {
			return errors.New("Alibaba ECS client factory returned nil")
		}
		rules, err := cloud.AlibabaSecurityGroupRules(ctx, client, region, items[i].ID)
		if err != nil {
			return err
		}
		items[i].Rules, items[i].RulesAvailable = rules, true
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Name < items[j].Name })
	result := make([]providers.Resource, 0, len(items))
	for _, item := range items {
		r, err := serviceResource(providers.SecurityGroup, region, item.ID, item.Name, "available", "ECS security group", item, map[string]any{"vpc_id": item.VPCID, "type": item.Type, "instance_count": item.InstanceCount, "rule_count": item.RuleCount})
		if err != nil {
			return nil, err
		}
		result = append(result, r)
	}
	return result, nil
}

func completeDetails(ctx context.Context, count, limit int, collect func(int) error) error {
	slots := make(chan struct{}, limit)
	failures := make(chan error, count)
	var wg sync.WaitGroup
	for i := range count {
		wg.Go(func() {
			select {
			case slots <- struct{}{}:
			case <-ctx.Done():
				failures <- ctx.Err()
				return
			}
			defer func() { <-slots }()
			if err := collect(i); err != nil {
				failures <- err
			}
		})
	}
	wg.Wait()
	close(failures)
	var failed []error
	for err := range failures {
		failed = append(failed, err)
	}
	return errors.Join(failed...)
}

func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
