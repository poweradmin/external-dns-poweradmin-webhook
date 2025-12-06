package poweradmin

import (
	"context"
	"fmt"
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
	domainFilter endpoint.DomainFilter
	dryRun       bool
	zoneCache    map[string]Zone // map[zoneName]Zone
}

// NewProvider creates a new PowerAdmin provider
func NewProvider(baseURL, apiKey string, domainFilter endpoint.DomainFilter, dryRun bool) (*Provider, error) {
	if baseURL == "" {
		return nil, fmt.Errorf("PowerAdmin base URL is required")
	}
	if apiKey == "" {
		return nil, fmt.Errorf("PowerAdmin API key is required")
	}

	client := NewClient(baseURL, apiKey)

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

	for _, zone := range zones {
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
			if !strings.HasSuffix(dnsName, zone.Name) {
				if dnsName == "@" || dnsName == "" {
					dnsName = zone.Name
				} else {
					dnsName = fmt.Sprintf("%s.%s", dnsName, zone.Name)
				}
			}

			// Handle MX records with priority
			target := record.Content
			if record.Type == "MX" && record.Priority != nil {
				target = fmt.Sprintf("%d %s", *record.Priority, record.Content)
			}

			ep := endpoint.NewEndpointWithTTL(dnsName, record.Type, endpoint.TTL(record.TTL), target)
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
	return &p.domainFilter
}

// createRecord creates a new DNS record
func (p *Provider) createRecord(ctx context.Context, ep *endpoint.Endpoint) error {
	zone, err := p.findZoneForEndpoint(ep)
	if err != nil {
		return err
	}

	for _, target := range ep.Targets {
		recordName := extractRecordName(ep.DNSName, zone.Name)
		content, priority := parseTarget(ep.RecordType, target)

		ttl := DefaultTTL
		if ep.RecordTTL.IsConfigured() {
			ttl = int(ep.RecordTTL)
		}

		req := CreateRecordRequest{
			Name:     recordName,
			Type:     ep.RecordType,
			Content:  content,
			TTL:      ttl,
			Priority: priority,
			Disabled: false,
		}

		log.Infof("Creating record: %s %s %s in zone %s", recordName, ep.RecordType, content, zone.Name)

		if p.dryRun {
			log.Info("Dry run: skipping actual creation")
			continue
		}

		if _, err := p.client.CreateRecord(ctx, zone.ID, req); err != nil {
			return err
		}
	}

	return nil
}

// updateRecord updates an existing DNS record
func (p *Provider) updateRecord(ctx context.Context, oldEp, newEp *endpoint.Endpoint) error {
	zone, err := p.findZoneForEndpoint(newEp)
	if err != nil {
		return err
	}

	// Get existing records to find record IDs
	records, err := p.client.ListRecords(ctx, zone.ID)
	if err != nil {
		return fmt.Errorf("failed to list records for zone %s: %w", zone.Name, err)
	}

	recordName := extractRecordName(oldEp.DNSName, zone.Name)

	// Track which record IDs have already been updated to handle duplicate targets
	updatedRecordIDs := make(map[int]bool)

	// Process updates by index to preserve multiplicity for duplicate targets
	for i, target := range oldEp.Targets {
		if i >= len(newEp.Targets) {
			continue
		}
		newTarget := newEp.Targets[i]
		content, _ := parseTarget(oldEp.RecordType, target)

		for _, record := range records {
			// Skip if already updated this record
			if updatedRecordIDs[record.ID] {
				continue
			}

			if record.Name == recordName && record.Type == oldEp.RecordType && record.Content == content {
				// Found matching record, update it with corresponding new value
				newContent, newPriority := parseTarget(newEp.RecordType, newTarget)

				ttl := DefaultTTL
				if newEp.RecordTTL.IsConfigured() {
					ttl = int(newEp.RecordTTL)
				}

				req := UpdateRecordRequest{
					Name:     extractRecordName(newEp.DNSName, zone.Name),
					Type:     newEp.RecordType,
					Content:  newContent,
					TTL:      ttl,
					Priority: newPriority,
					Disabled: false,
				}

				log.Infof("Updating record %d: %s -> %s", record.ID, content, newContent)

				// Mark this record as updated before making the API call
				updatedRecordIDs[record.ID] = true

				if p.dryRun {
					log.Info("Dry run: skipping actual update")
					break // Move to next target
				}

				if _, err := p.client.UpdateRecord(ctx, zone.ID, record.ID, req); err != nil {
					return err
				}
				break // Found and updated matching record, move to next target
			}
		}
	}

	return nil
}

// deleteRecord deletes a DNS record
func (p *Provider) deleteRecord(ctx context.Context, ep *endpoint.Endpoint) error {
	zone, err := p.findZoneForEndpoint(ep)
	if err != nil {
		return err
	}

	// Get existing records to find record IDs
	records, err := p.client.ListRecords(ctx, zone.ID)
	if err != nil {
		return fmt.Errorf("failed to list records for zone %s: %w", zone.Name, err)
	}

	recordName := extractRecordName(ep.DNSName, zone.Name)

	for _, target := range ep.Targets {
		content, _ := parseTarget(ep.RecordType, target)

		for _, record := range records {
			if record.Name == recordName && record.Type == ep.RecordType && record.Content == content {
				log.Infof("Deleting record %d: %s %s %s", record.ID, recordName, ep.RecordType, content)

				if p.dryRun {
					log.Info("Dry run: skipping actual deletion")
					continue
				}

				if err := p.client.DeleteRecord(ctx, zone.ID, record.ID); err != nil {
					return err
				}
			}
		}
	}

	return nil
}

// findZoneForEndpoint finds the zone that contains the given endpoint
func (p *Provider) findZoneForEndpoint(ep *endpoint.Endpoint) (*Zone, error) {
	var matchedZone *Zone
	var maxLength int

	for zoneName, zone := range p.zoneCache {
		if strings.HasSuffix(ep.DNSName, zoneName) && len(zoneName) > maxLength {
			z := zone
			matchedZone = &z
			maxLength = len(zoneName)
		}
	}

	if matchedZone == nil {
		return nil, fmt.Errorf("no zone found for endpoint %s", ep.DNSName)
	}

	return matchedZone, nil
}

// extractRecordName extracts the record name from the full DNS name
func extractRecordName(dnsName, zoneName string) string {
	if dnsName == zoneName {
		return "@"
	}
	return strings.TrimSuffix(dnsName, "."+zoneName)
}

// parseTarget parses the target value for special record types like MX
func parseTarget(recordType, target string) (content string, priority *int) {
	if recordType == "MX" {
		parts := strings.SplitN(target, " ", 2)
		if len(parts) == 2 {
			var p int
			if _, err := fmt.Sscanf(parts[0], "%d", &p); err == nil {
				return parts[1], &p
			}
		}
	}

	// Strip surrounding quotes from TXT records
	if recordType == "TXT" {
		target = strings.Trim(target, "\"")
	}

	return target, nil
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
	}
	return supported[recordType]
}
