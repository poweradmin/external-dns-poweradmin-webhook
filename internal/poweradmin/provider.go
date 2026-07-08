package poweradmin

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	log "github.com/sirupsen/logrus"
	"sigs.k8s.io/external-dns/endpoint"
	"sigs.k8s.io/external-dns/plan"
	"sigs.k8s.io/external-dns/provider"
)

const (
	// DefaultTTL is the default TTL for records
	DefaultTTL = 3600
)

// Provider implements the external-dns provider interface for PowerAdmin
type Provider struct {
	provider.BaseProvider
	client       *Client
	domainFilter *endpoint.DomainFilter
	dryRun       bool
	zoneCache    map[string]Zone // map[zoneName]Zone
}

// NewProvider creates a new PowerAdmin provider
func NewProvider(baseURL, apiKey string, apiVersion APIVersion, domainFilter *endpoint.DomainFilter, dryRun bool) (*Provider, error) {
	if baseURL == "" {
		return nil, fmt.Errorf("PowerAdmin base URL is required")
	}
	if apiKey == "" {
		return nil, fmt.Errorf("PowerAdmin API key is required")
	}

	client := NewClient(baseURL, apiKey, apiVersion)

	return &Provider{
		client:       client,
		domainFilter: domainFilter,
		dryRun:       dryRun,
		zoneCache:    make(map[string]Zone),
	}, nil
}

// Records returns all DNS records managed by this provider
func (p *Provider) Records(ctx context.Context) ([]*endpoint.Endpoint, error) {
	zones, err := p.client.ListZones(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list zones: %w", err)
	}

	var endpoints []*endpoint.Endpoint

	// external-dns expects one endpoint per record set: records sharing a DNS
	// name and type must be aggregated into a single multi-target endpoint,
	// otherwise the plan only ever sees one of them as current state.
	type endpointKey struct {
		dnsName    string
		recordType string
	}

	for _, zone := range zones {
		// Aggregation is scoped per zone so records shadowed across
		// overlapping zones keep their zone attribution.
		byKey := make(map[endpointKey]*endpoint.Endpoint)
		// Apply domain filter
		if !p.domainFilter.Match(zone.Name) {
			log.Debugf("Skipping zone %s: does not match domain filter", zone.Name)
			continue
		}

		// Cache zone for later use
		p.zoneCache[zone.Name] = zone

		records, err := p.client.ListRecords(ctx, zone.ID)
		if err != nil {
			log.Warnf("Failed to list records for zone %s: %v", zone.Name, err)
			continue
		}

		for _, record := range records {
			// Skip SOA and NS records at zone apex
			if record.Type == "SOA" || (record.Type == "NS" && record.Name == zone.Name) {
				continue
			}

			// Skip unsupported record types
			if !isSupportedRecordType(record.Type) {
				continue
			}

			// Build the full DNS name
			dnsName := record.Name
			if !zoneContains(zone.Name, dnsName) {
				if dnsName == "@" || dnsName == "" {
					dnsName = zone.Name
				} else {
					dnsName = fmt.Sprintf("%s.%s", dnsName, zone.Name)
				}
			}

			target := recordTarget(record)
			key := endpointKey{dnsName: dnsName, recordType: record.Type}
			if ep, ok := byKey[key]; ok {
				ep.Targets = append(ep.Targets, target)
				continue
			}

			ep := endpoint.NewEndpointWithTTL(dnsName, record.Type, endpoint.TTL(record.TTL), target)
			if ep == nil {
				log.Warnf("Skipping record %s %s in zone %s: invalid DNS name", record.Name, record.Type, zone.Name)
				continue
			}
			byKey[key] = ep
			endpoints = append(endpoints, ep)
		}
	}

	log.Infof("Found %d endpoints", len(endpoints))
	return endpoints, nil
}

// ApplyChanges applies the given changes to DNS records
func (p *Provider) ApplyChanges(ctx context.Context, changes *plan.Changes) error {
	if !changes.HasChanges() {
		log.Debug("No changes to apply")
		return nil
	}

	// Refresh zone cache
	zones, err := p.client.ListZones(ctx)
	if err != nil {
		return fmt.Errorf("failed to list zones: %w", err)
	}
	for _, zone := range zones {
		p.zoneCache[zone.Name] = zone
	}

	// Process creates
	for _, ep := range changes.Create {
		if err := p.createRecord(ctx, ep); err != nil {
			return fmt.Errorf("failed to create record %s: %w", ep.DNSName, err)
		}
	}

	// Process updates
	for i := range changes.UpdateNew {
		oldEp := changes.UpdateOld[i]
		newEp := changes.UpdateNew[i]
		if err := p.updateRecord(ctx, oldEp, newEp); err != nil {
			return fmt.Errorf("failed to update record %s: %w", newEp.DNSName, err)
		}
	}

	// Process deletes
	for _, ep := range changes.Delete {
		if err := p.deleteRecord(ctx, ep); err != nil {
			return fmt.Errorf("failed to delete record %s: %w", ep.DNSName, err)
		}
	}

	return nil
}

// AdjustEndpoints modifies endpoints before they are applied
func (p *Provider) AdjustEndpoints(endpoints []*endpoint.Endpoint) ([]*endpoint.Endpoint, error) {
	// No adjustments needed for PowerAdmin
	return endpoints, nil
}

// GetDomainFilter returns the domain filter for this provider
func (p *Provider) GetDomainFilter() endpoint.DomainFilterInterface {
	return p.domainFilter
}

// createRecord creates DNS records for all targets of an endpoint
func (p *Provider) createRecord(ctx context.Context, ep *endpoint.Endpoint) error {
	dnsName := normalizeDNSName(ep.DNSName)
	zone, err := p.findZoneForName(dnsName)
	if err != nil {
		return err
	}

	recordName := extractRecordName(dnsName, zone.Name)
	ttl := endpointTTL(ep)

	for _, target := range ep.Targets {
		if err := p.createOne(ctx, zone, recordName, ep.RecordType, target, ttl); err != nil {
			return err
		}
	}

	return nil
}

// updateRecord reconciles the record set for an endpoint: existing records are
// rewritten in place, surplus desired targets are created, and records for
// targets that are no longer desired are deleted. This keeps the zone correct
// even when the number of targets grows or shrinks between old and new.
func (p *Provider) updateRecord(ctx context.Context, oldEp, newEp *endpoint.Endpoint) error {
	dnsName := normalizeDNSName(newEp.DNSName)
	zone, err := p.findZoneForName(dnsName)
	if err != nil {
		return err
	}

	// Get existing records to find record IDs
	records, err := p.client.ListRecords(ctx, zone.ID)
	if err != nil {
		return fmt.Errorf("failed to list records for zone %s: %w", zone.Name, err)
	}

	existing := claimRecords(records, normalizeDNSName(oldEp.DNSName), oldEp.RecordType, oldEp.Targets)
	ttl := endpointTTL(newEp)
	recordName := extractRecordName(dnsName, zone.Name)

	// Pair desired targets with records that already match so unchanged
	// records are left alone.
	used := make([]bool, len(existing))
	var pending []string
	for _, target := range newEp.Targets {
		matched := false
		for i, record := range existing {
			if !used[i] && record.TTL == ttl && recordMatchesTarget(record, target) {
				used[i] = true
				matched = true
				break
			}
		}
		if !matched {
			pending = append(pending, target)
		}
	}
	var leftover []Record
	for i, record := range existing {
		if !used[i] {
			leftover = append(leftover, record)
		}
	}

	// Rewrite leftover records with the remaining desired targets; once
	// records run out, create the surplus targets.
	for i, target := range pending {
		if i >= len(leftover) {
			if err := p.createOne(ctx, zone, recordName, newEp.RecordType, target, ttl); err != nil {
				return err
			}
			continue
		}

		if err := p.updateOne(ctx, zone, leftover[i], recordName, newEp.RecordType, target, ttl); err != nil {
			return err
		}
	}

	// Records claimed by old targets but not reused are no longer desired.
	for i := len(pending); i < len(leftover); i++ {
		if err := p.deleteOne(ctx, zone, leftover[i]); err != nil {
			return err
		}
	}

	return nil
}

// deleteRecord deletes the records claimed by an endpoint's targets
func (p *Provider) deleteRecord(ctx context.Context, ep *endpoint.Endpoint) error {
	dnsName := normalizeDNSName(ep.DNSName)
	zone, err := p.findZoneForName(dnsName)
	if err != nil {
		return err
	}

	// Get existing records to find record IDs
	records, err := p.client.ListRecords(ctx, zone.ID)
	if err != nil {
		return fmt.Errorf("failed to list records for zone %s: %w", zone.Name, err)
	}

	for _, record := range claimRecords(records, dnsName, ep.RecordType, ep.Targets) {
		if err := p.deleteOne(ctx, zone, record); err != nil {
			return err
		}
	}

	return nil
}

// createOne creates a single DNS record for one endpoint target
func (p *Provider) createOne(ctx context.Context, zone *Zone, recordName, recordType, target string, ttl int) error {
	content, priority, err := parseTarget(recordType, target)
	if err != nil {
		return err
	}
	req := CreateRecordRequest{
		Name:     recordName,
		Type:     recordType,
		Content:  content,
		TTL:      ttl,
		Priority: priority,
		Disabled: false,
	}

	log.Infof("Creating record: %s %s %s in zone %s", recordName, recordType, content, zone.Name)
	if p.dryRun {
		log.Info("Dry run: skipping actual creation")
		return nil
	}

	_, err = p.client.CreateRecord(ctx, zone.ID, req)
	return err
}

// updateOne rewrites a single DNS record to a new endpoint target
func (p *Provider) updateOne(ctx context.Context, zone *Zone, record Record, recordName, recordType, target string, ttl int) error {
	content, priority, err := parseTarget(recordType, target)
	if err != nil {
		return err
	}
	req := UpdateRecordRequest{
		Name:     recordName,
		Type:     recordType,
		Content:  content,
		TTL:      ttl,
		Priority: priority,
		Disabled: false,
	}

	log.Infof("Updating record %d: %s -> %s", record.ID, record.Content, content)
	if p.dryRun {
		log.Info("Dry run: skipping actual update")
		return nil
	}

	_, err = p.client.UpdateRecord(ctx, zone.ID, record.ID, req)
	return err
}

// deleteOne deletes a single DNS record
func (p *Provider) deleteOne(ctx context.Context, zone *Zone, record Record) error {
	log.Infof("Deleting record %d: %s %s %s", record.ID, record.Name, record.Type, record.Content)
	if p.dryRun {
		log.Info("Dry run: skipping actual deletion")
		return nil
	}
	return p.client.DeleteRecord(ctx, zone.ID, record.ID)
}

// claimRecords returns the zone records currently owned by an endpoint: same
// name and type, with content matching one of the endpoint's targets. Targets
// are consumed as a multiset so duplicate targets claim distinct records.
// dnsName must be canonical.
func claimRecords(records []Record, dnsName, recordType string, targets endpoint.Targets) []Record {
	remaining := make([]string, len(targets))
	copy(remaining, targets)

	var claimed []Record
	for _, record := range records {
		if record.Name != dnsName || record.Type != recordType {
			continue
		}
		for i, target := range remaining {
			if recordMatchesTarget(record, target) {
				claimed = append(claimed, record)
				remaining = append(remaining[:i], remaining[i+1:]...)
				break
			}
		}
	}
	return claimed
}

// findZoneForName finds the zone that contains the given canonical DNS name,
// preferring the longest match so nested zones win over their parents
func (p *Provider) findZoneForName(dnsName string) (*Zone, error) {
	var matchedZone *Zone
	var maxLength int

	for zoneName, zone := range p.zoneCache {
		if zoneContains(zoneName, dnsName) && len(zoneName) > maxLength {
			z := zone
			matchedZone = &z
			maxLength = len(zoneName)
		}
	}

	if matchedZone == nil {
		return nil, fmt.Errorf("no zone found for endpoint %s", dnsName)
	}

	return matchedZone, nil
}

// zoneContains reports whether dnsName is the zone apex or a name within the
// zone. A plain suffix check is not enough: "notexample.com" must not match
// zone "example.com". Both arguments must already be canonical.
func zoneContains(zoneName, dnsName string) bool {
	return dnsName == zoneName || strings.HasSuffix(dnsName, "."+zoneName)
}

// extractRecordName extracts the record name from the full DNS name. Both
// arguments must already be canonical.
func extractRecordName(dnsName, zoneName string) string {
	if dnsName == zoneName {
		return "@"
	}
	return strings.TrimSuffix(dnsName, "."+zoneName)
}

// endpointTTL returns the endpoint's configured TTL, or DefaultTTL if unset
func endpointTTL(ep *endpoint.Endpoint) int {
	if ep.RecordTTL.IsConfigured() {
		return int(ep.RecordTTL)
	}
	return DefaultTTL
}

// parseTarget splits an external-dns target into the content and priority the
// PowerAdmin API stores separately. MX targets are "<priority> <host>" and SRV
// targets are "<priority> <weight> <port> <host>"; both must carry a numeric
// priority or the record cannot be represented faithfully.
func parseTarget(recordType, target string) (content string, priority *int, err error) {
	switch recordType {
	case "MX":
		if prioStr, host, ok := strings.Cut(target, " "); ok && host != "" {
			if prio, err := strconv.Atoi(prioStr); err == nil {
				return host, &prio, nil
			}
		}
		return "", nil, fmt.Errorf("invalid MX target %q: expected \"priority host\"", target)
	case "SRV":
		parts := strings.SplitN(target, " ", 4)
		if len(parts) == 4 && parts[3] != "" {
			prio, errPrio := strconv.Atoi(parts[0])
			_, errWeight := strconv.Atoi(parts[1])
			_, errPort := strconv.Atoi(parts[2])
			if errPrio == nil && errWeight == nil && errPort == nil {
				return strings.Join(parts[1:], " "), &prio, nil
			}
		}
		return "", nil, fmt.Errorf("invalid SRV target %q: expected \"priority weight port target\"", target)
	case "TXT":
		// Ensure TXT records are quoted for the PowerAdmin API
		return fmt.Sprintf("\"%s\"", unquoteTXT(target)), nil, nil
	}

	return target, nil, nil
}

// unquoteTXT strips one pair of surrounding quotes from TXT content. A blanket
// Trim would also eat quotes that are part of the value itself.
func unquoteTXT(content string) string {
	if len(content) >= 2 && strings.HasPrefix(content, "\"") && strings.HasSuffix(content, "\"") {
		return content[1 : len(content)-1]
	}
	return content
}

// recordTarget converts a PowerAdmin record to its external-dns target
// representation: MX/SRV priority is folded into the target string, TXT
// quotes are stripped, and hostname-valued content loses its trailing dot so
// the exposed target agrees with what recordMatchesTarget considers equal.
func recordTarget(record Record) string {
	switch record.Type {
	case "TXT":
		return unquoteTXT(record.Content)
	case "MX", "SRV":
		// A missing priority is exposed as 0 so the target always carries the
		// numeric prefix parseTarget requires on the way back in.
		priority := 0
		if record.Priority != nil {
			priority = *record.Priority
		}
		return fmt.Sprintf("%d %s", priority, strings.TrimSuffix(record.Content, "."))
	case "CNAME", "NS", "PTR":
		return strings.TrimSuffix(record.Content, ".")
	}
	return record.Content
}

// normalizeTarget canonicalizes a target for comparison. The API may return
// TXT content quoted or unquoted, and hostname-valued content may differ in
// case or carry a trailing dot without being a different target.
func normalizeTarget(recordType, target string) string {
	switch recordType {
	case "TXT":
		return unquoteTXT(target)
	case "CNAME", "MX", "NS", "PTR", "SRV":
		return normalizeDNSName(target)
	}
	return target
}

// recordMatchesTarget reports whether a PowerAdmin record represents the given
// external-dns target.
func recordMatchesTarget(record Record, target string) bool {
	return normalizeTarget(record.Type, recordTarget(record)) == normalizeTarget(record.Type, target)
}

// isSupportedRecordType checks if the record type is supported
func isSupportedRecordType(recordType string) bool {
	supported := map[string]bool{
		"A":     true,
		"AAAA":  true,
		"CNAME": true,
		"TXT":   true,
		"MX":    true,
		"NS":    true,
		"SRV":   true,
		"PTR":   true,
		"CAA":   true,
		"LUA":   true,
	}
	return supported[recordType]
}
