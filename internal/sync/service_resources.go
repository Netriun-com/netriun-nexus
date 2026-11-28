// SPDX-License-Identifier: AGPL-3.0-only

package sync

import (
	"encoding/json"
	"errors"

	"github.com/netriun/nexus/internal/cloud"
	"github.com/netriun/nexus/internal/providers"
	"github.com/netriun/nexus/internal/resources"
)

// serviceCatalog validates and maps a complete adapter result to the catalog.
func serviceCatalog(workspace, account int64, provider, service, region string, items []providers.Resource, relations []providers.Relation) (resources.Scope, map[string][]resources.Item, map[string][]resources.Relation, error) {
	scope := resources.Scope{WorkspaceID: workspace, AccountID: account, Provider: provider, Service: service, Region: region}
	catalog := map[string][]resources.Item{}
	edges := map[string][]resources.Relation{}
	switch service {
	case Desktops:
		scope.Kind = providers.Desktop
	case Users:
		scope.Kind = providers.DirectoryUser
	case Buckets:
		scope.Kind = providers.Bucket
	case SecurityGroups:
		scope.Kind = providers.SecurityGroup
	default:
		return scope, nil, nil, errors.New("unsupported snapshot service")
	}
	if provider != "alibaba" || (service == Buckets && region != AccountWide) || (service != Buckets && (region == "" || region == AccountWide || region == "all")) || (service == Users && region != cloud.EDSUserRegion(region)) {
		return scope, nil, nil, errors.New("invalid service snapshot scope")
	}
	for _, item := range items {
		if item.WorkspaceID != workspace || item.AccountID != account || item.Provider != provider || item.Service != service || item.Kind != scope.Kind || item.Region == "" || item.Region == AccountWide || (service != Buckets && item.Region != region) || item.Details == nil {
			return scope, nil, nil, errors.New("provider returned a resource outside the service snapshot scope")
		}
		details, err := json.Marshal(item.Details)
		if err != nil {
			return scope, nil, nil, err
		}
		tags, err := json.Marshal(item.Tags)
		if err != nil {
			return scope, nil, nil, err
		}
		summary, err := json.Marshal(item.Summary)
		if err != nil {
			return scope, nil, nil, err
		}
		catalog[item.Region] = append(catalog[item.Region], resources.Item{ProviderID: item.ProviderID, Name: item.Name, State: item.State, Tags: tags, Summary: summary, Details: details})
	}
	for _, edge := range relations {
		if edge.From.Service != service || edge.From.Kind != scope.Kind || (service != Buckets && edge.From.Region != region) {
			return scope, nil, nil, resources.ErrInvalid
		}
		edges[edge.From.Region] = append(edges[edge.From.Region], resources.Relation{From: resources.Reference(edge.From), To: resources.Reference(edge.To), Relation: edge.Relation})
	}
	return scope, catalog, edges, nil
}
