package emailauth

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/publicsuffix"
)

const (
	dmarcLookupTimeout = 2 * time.Second
	dmarcCacheTTL      = 5 * time.Minute
	dmarcCacheLimit    = 1024
	dmarcMaxQueries    = 8
)

var errInvalidDMARCRecord = errors.New("invalid DMARC record")

type TXTResolver interface {
	LookupTXT(ctx context.Context, name string) ([]string, error)
}

type netTXTResolver struct{ resolver *net.Resolver }

func (r netTXTResolver) LookupTXT(ctx context.Context, name string) ([]string, error) {
	return r.resolver.LookupTXT(ctx, name)
}

type dmarcRecord struct {
	Policy          DMARCPolicy
	SubdomainPolicy *DMARCPolicy
	SPFStrict       bool
	DKIMStrict      bool
	PSD             string
}

type dmarcWalkEntry struct {
	domain string
	record *dmarcRecord
}

type dmarcWalk struct {
	entries []dmarcWalkEntry
	err     error
	status  Status
}

// dmarcDiscovery is the policy selected for an Author Domain. domain is the
// policy domain; organizationalDomain is used for relaxed alignment.
type dmarcDiscovery struct {
	record               *dmarcRecord
	domain               string
	organizationalDomain string
	err                  error
	status               Status
}

type dmarcCacheEntry struct {
	walk    dmarcWalk
	expires time.Time
}

type dmarcEvaluator struct {
	resolver TXTResolver
	mu       sync.Mutex
	cache    map[string]dmarcCacheEntry
	order    []string
	now      func() time.Time
}

func newDMARCEvaluator(resolver TXTResolver) *dmarcEvaluator {
	return &dmarcEvaluator{resolver: resolver, cache: make(map[string]dmarcCacheEntry), now: time.Now}
}

func evaluateDMARC(ctx context.Context, resolver TXTResolver, headerFromDomain string, spf SPFResult, dkim []DKIMResult) DMARCResult {
	return newDMARCEvaluator(resolver).evaluate(ctx, headerFromDomain, spf, dkim)
}

func (e *dmarcEvaluator) evaluate(ctx context.Context, headerFromDomain string, spf SPFResult, dkim []DKIMResult) DMARCResult {
	headerFromDomain = normDomain(headerFromDomain)
	result := DMARCResult{Status: StatusNone, AlignedBy: []AlignmentMechanism{}}
	if !validDomainName(headerFromDomain) {
		result.Detail = "no valid From-header domain"
		return result
	}
	if isPublicSuffixDomain(headerFromDomain) {
		result.Detail = "From-header domain is a public suffix"
		return result
	}
	result.Domain = stringPtr(headerFromDomain)

	discovery := e.discover(ctx, headerFromDomain)
	if discovery.err != nil {
		result.Status, result.Detail = discovery.status, discovery.err.Error()
		return result
	}
	if discovery.record == nil {
		result.Detail = "no DMARC policy record"
		return result
	}

	policy := discovery.record.Policy
	if discovery.domain != headerFromDomain && discovery.record.SubdomainPolicy != nil {
		policy = *discovery.record.SubdomainPolicy
	}
	result.Policy = &policy

	var alignmentErr *dmarcWalk
	if spf.Status == StatusPass && spf.Domain != nil {
		aligned, walkErr := e.domainsAlign(ctx, *spf.Domain, headerFromDomain, discovery.record.SPFStrict, discovery.organizationalDomain)
		if aligned {
			result.AlignedBy = append(result.AlignedBy, AlignedBySPF)
		} else if walkErr != nil {
			alignmentErr = walkErr
		}
	}
	for _, signature := range dkim {
		if signature.Status != StatusPass || signature.Domain == nil {
			continue
		}
		aligned, walkErr := e.domainsAlign(ctx, *signature.Domain, headerFromDomain, discovery.record.DKIMStrict, discovery.organizationalDomain)
		if aligned {
			result.AlignedBy = appendUniqueAlignment(result.AlignedBy, AlignedByDKIM)
			break
		}
		if walkErr != nil && alignmentErr == nil {
			alignmentErr = walkErr
		}
	}
	if len(result.AlignedBy) > 0 {
		result.Status = StatusPass
		result.Detail = "at least one authenticated identifier aligns with the From-header domain"
		return result
	}
	if alignmentErr != nil {
		result.Status, result.Detail = alignmentErr.status, alignmentErr.err.Error()
		return result
	}
	result.Status = StatusFail
	result.Detail = "no authenticated identifier aligns with the From-header domain"
	return result
}

// evaluateAuthentication applies the policy's strict/relaxed alignment modes
// to each passing mechanism and then derives the DMARC result from that same
// evidence. Non-pass mechanism results deliberately retain aligned=null.
func (e *dmarcEvaluator) evaluateAuthentication(ctx context.Context, headerFromDomain string, authentication *Authentication) {
	if authentication == nil {
		return
	}
	discovery := e.discover(ctx, headerFromDomain)
	if discovery.record != nil && discovery.err == nil {
		if authentication.SPF.Status == StatusPass && authentication.SPF.Domain != nil {
			aligned, _ := e.domainsAlign(ctx, *authentication.SPF.Domain, headerFromDomain, discovery.record.SPFStrict, discovery.organizationalDomain)
			authentication.SPF.Aligned = boolPtr(aligned)
		}
		for i := range authentication.DKIM {
			result := &authentication.DKIM[i]
			if result.Status != StatusPass || result.Domain == nil {
				continue
			}
			aligned, _ := e.domainsAlign(ctx, *result.Domain, headerFromDomain, discovery.record.DKIMStrict, discovery.organizationalDomain)
			result.Aligned = boolPtr(aligned)
		}
	}
	authentication.DMARC = e.evaluate(ctx, headerFromDomain, authentication.SPF, authentication.DKIM)
}

func (e *dmarcEvaluator) domainsAlign(ctx context.Context, authenticatedDomain, authorDomain string, strict bool, authorOrgDomain string) (bool, *dmarcWalk) {
	authenticatedDomain, authorDomain = normDomain(authenticatedDomain), normDomain(authorDomain)
	if authenticatedDomain == "" || authorDomain == "" {
		return false, nil
	}
	if isPublicSuffixDomain(authenticatedDomain) || isPublicSuffixDomain(authorDomain) {
		return false, nil
	}
	if authenticatedDomain == authorDomain {
		return true, nil
	}
	if strict {
		return false, nil
	}
	walk := e.walk(ctx, authenticatedDomain)
	if walk.err != nil {
		return false, &walk
	}
	return organizationalDomain(authenticatedDomain, walk.entries) == authorOrgDomain, nil
}

func (e *dmarcEvaluator) discover(ctx context.Context, headerFromDomain string) dmarcDiscovery {
	return discoverDMARCRecordWithEvaluator(ctx, e, headerFromDomain)
}

// discoverDMARCRecord is exposed to package tests and callers that inject a
// resolver. Production evaluation uses the evaluator so tree walks are cached.
func discoverDMARCRecord(ctx context.Context, resolver TXTResolver, headerFromDomain string) dmarcDiscovery {
	return discoverDMARCRecordWithEvaluator(ctx, newDMARCEvaluator(resolver), headerFromDomain)
}

func discoverDMARCRecordWithEvaluator(ctx context.Context, evaluator *dmarcEvaluator, headerFromDomain string) dmarcDiscovery {
	domain := normDomain(headerFromDomain)
	if !validDomainName(domain) {
		return dmarcDiscovery{}
	}
	walk := evaluator.walk(ctx, domain)
	if walk.err != nil {
		return dmarcDiscovery{err: walk.err, status: walk.status}
	}
	if len(walk.entries) == 0 {
		return dmarcDiscovery{organizationalDomain: domain}
	}

	orgDomain := organizationalDomain(domain, walk.entries)
	// A valid record at the Author Domain always has highest policy priority.
	if walk.entries[0].domain == domain {
		return dmarcDiscovery{record: walk.entries[0].record, domain: domain, organizationalDomain: orgDomain}
	}
	// Otherwise use the Organizational Domain's record, or the PSD record that
	// established it when the Organizational Domain itself has no record.
	for _, entry := range walk.entries {
		if entry.domain == orgDomain {
			return dmarcDiscovery{record: entry.record, domain: entry.domain, organizationalDomain: orgDomain}
		}
	}
	for _, entry := range walk.entries {
		if entry.record.PSD == "y" {
			return dmarcDiscovery{record: entry.record, domain: entry.domain, organizationalDomain: orgDomain}
		}
	}
	entry := walk.entries[len(walk.entries)-1]
	return dmarcDiscovery{record: entry.record, domain: entry.domain, organizationalDomain: orgDomain}
}

func (e *dmarcEvaluator) walk(ctx context.Context, domain string) dmarcWalk {
	domain = normDomain(domain)
	now := e.now()
	e.mu.Lock()
	if entry, ok := e.cache[domain]; ok && now.Before(entry.expires) {
		e.mu.Unlock()
		return entry.walk
	}
	e.mu.Unlock()

	walk := walkDMARCTree(ctx, e.resolver, domain)
	// RFC 9989 permits receiver discretion for DNS errors. Do not cache any
	// error so a later delivery can reach a definitive result.
	if walk.err == nil {
		e.mu.Lock()
		if len(e.cache) >= dmarcCacheLimit && len(e.order) > 0 {
			delete(e.cache, e.order[0])
			e.order = e.order[1:]
		}
		if _, exists := e.cache[domain]; !exists {
			e.order = append(e.order, domain)
		}
		e.cache[domain] = dmarcCacheEntry{walk: walk, expires: now.Add(dmarcCacheTTL)}
		e.mu.Unlock()
	}
	return walk
}

func walkDMARCTree(ctx context.Context, resolver TXTResolver, domain string) dmarcWalk {
	targets := dmarcTreeTargets(domain)
	if len(targets) == 0 {
		return dmarcWalk{}
	}
	walk := dmarcWalk{entries: make([]dmarcWalkEntry, 0, len(targets))}
	for _, target := range targets {
		lookupCtx, cancel := context.WithTimeout(ctx, dmarcLookupTimeout)
		txt, err := resolver.LookupTXT(lookupCtx, "_dmarc."+target)
		cancel()
		if err != nil && !isDNSNotFound(err) {
			walk.status = StatusPermError
			if isTemporaryError(err) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
				walk.status = StatusTempError
			}
			walk.err = fmt.Errorf("DMARC lookup for %s: %w", target, err)
			return walk
		}
		record, parseErr := selectDMARCRecord(txt)
		if parseErr != nil {
			walk.err, walk.status = parseErr, StatusPermError
			return walk
		}
		if record != nil {
			walk.entries = append(walk.entries, dmarcWalkEntry{domain: target, record: record})
			if record.PSD == "n" || record.PSD == "y" {
				break
			}
		}
	}
	return walk
}

func dmarcTreeTargets(domain string) []string {
	if !validDomainName(domain) {
		return nil
	}
	labels := strings.Split(domain, ".")
	targets := []string{domain}
	if len(labels) == 1 {
		return targets
	}
	suffix, _ := publicsuffix.PublicSuffix(domain)
	suffixLabels := strings.Split(normDomain(suffix), ".")
	last := len(labels) - len(suffixLabels)
	if suffix == "" || last < 0 {
		last = len(labels) - 1
	}
	start := 1
	if len(labels) > dmarcMaxQueries {
		start = len(labels) - (dmarcMaxQueries - 1)
	}
	for i := start; i <= last && len(targets) < dmarcMaxQueries; i++ {
		targets = append(targets, strings.Join(labels[i:], "."))
	}
	return targets
}

func organizationalDomain(start string, entries []dmarcWalkEntry) string {
	for _, entry := range entries {
		if entry.record.PSD == "n" && !isPublicSuffixDomain(entry.domain) {
			return entry.domain
		}
		if entry.record.PSD == "y" && entry.domain != start {
			return directChild(start, entry.domain)
		}
	}
	if organizational, err := publicsuffix.EffectiveTLDPlusOne(start); err == nil {
		return normDomain(organizational)
	}
	return start
}

func isPublicSuffixDomain(domain string) bool {
	domain = normDomain(domain)
	if domain == "" {
		return false
	}
	suffix, _ := publicsuffix.PublicSuffix(domain)
	return normDomain(suffix) == domain
}

func directChild(start, parent string) string {
	startLabels, parentLabels := strings.Split(start, "."), strings.Split(parent, ".")
	if len(startLabels) <= len(parentLabels) {
		return start
	}
	return strings.Join(startLabels[len(startLabels)-len(parentLabels)-1:], ".")
}

// selectDMARCRecord discards a target containing multiple versioned records,
// as required by RFC 9989 section 4.10. A single malformed versioned record is
// surfaced as permerror so developers can distinguish it from no policy.
func selectDMARCRecord(records []string) (*dmarcRecord, error) {
	var candidates []string
	for _, record := range records {
		if hasDMARCVersion(record) {
			candidates = append(candidates, strings.TrimSpace(record))
		}
	}
	if len(candidates) != 1 {
		return nil, nil
	}
	return parseDMARCRecord(candidates[0])
}

func hasDMARCVersion(record string) bool {
	first, _, _ := strings.Cut(strings.TrimSpace(record), ";")
	key, value, ok := strings.Cut(first, "=")
	return ok && strings.EqualFold(strings.TrimSpace(key), "v") && strings.TrimSpace(value) == "DMARC1"
}

func parseDMARCRecord(value string) (*dmarcRecord, error) {
	parts := strings.Split(value, ";")
	seen, values := make(map[string]bool, len(parts)), make(map[string]string, len(parts))
	first := true
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		key, tagValue, ok := strings.Cut(part, "=")
		key, tagValue = strings.ToLower(strings.TrimSpace(key)), strings.TrimSpace(tagValue)
		if !ok || key == "" || tagValue == "" {
			return nil, fmt.Errorf("%w: malformed tag %q", errInvalidDMARCRecord, part)
		}
		if first && (key != "v" || tagValue != "DMARC1") {
			return nil, fmt.Errorf("%w: v=DMARC1 must be first", errInvalidDMARCRecord)
		}
		first = false
		if seen[key] {
			return nil, fmt.Errorf("%w: duplicate %s tag", errInvalidDMARCRecord, key)
		}
		seen[key], values[key] = true, tagValue
	}
	if values["v"] != "DMARC1" {
		return nil, fmt.Errorf("%w: missing v=DMARC1", errInvalidDMARCRecord)
	}
	policy := DMARCPolicyNone
	if value, ok := values["p"]; ok {
		parsed, err := parseDMARCPolicy(strings.ToLower(value))
		if err != nil {
			if !hasValidDMARCReportURI(values["rua"]) {
				return nil, nil
			}
			return &dmarcRecord{Policy: DMARCPolicyNone, PSD: "u"}, nil
		} else {
			policy = parsed
		}
	}
	record := &dmarcRecord{Policy: policy, PSD: "u"}
	if value, ok := values["sp"]; ok {
		parsed, parseErr := parseDMARCPolicy(strings.ToLower(value))
		if parseErr != nil {
			if !hasValidDMARCReportURI(values["rua"]) {
				return nil, nil
			}
			return &dmarcRecord{Policy: DMARCPolicyNone, PSD: "u"}, nil
		} else {
			record.SubdomainPolicy = &parsed
		}
	}
	for tag, destination := range map[string]*bool{"aspf": &record.SPFStrict, "adkim": &record.DKIMStrict} {
		if value, ok := values[tag]; ok {
			value = strings.ToLower(value)
			if value != "r" && value != "s" {
				continue
			}
			*destination = value == "s"
		}
	}
	if value, ok := values["psd"]; ok {
		value = strings.ToLower(value)
		if value != "y" && value != "n" && value != "u" {
			value = "u"
		}
		record.PSD = value
	}
	return record, nil
}

func hasValidDMARCReportURI(value string) bool {
	for _, candidate := range strings.Split(value, ",") {
		candidate = strings.TrimSpace(candidate)
		if before, _, ok := strings.Cut(candidate, "!"); ok {
			candidate = before
		}
		parsed, err := url.ParseRequestURI(candidate)
		if err == nil && parsed.Scheme != "" {
			return true
		}
	}
	return false
}

func parseDMARCPolicy(value string) (DMARCPolicy, error) {
	switch DMARCPolicy(value) {
	case DMARCPolicyNone, DMARCPolicyQuarantine, DMARCPolicyReject:
		return DMARCPolicy(value), nil
	default:
		return "", fmt.Errorf("%w: invalid or missing policy %q", errInvalidDMARCRecord, value)
	}
}

func appendUniqueAlignment(values []AlignmentMechanism, value AlignmentMechanism) []AlignmentMechanism {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

func stringPtr(value string) *string { return &value }

func boolPtr(value bool) *bool { return &value }

func validDomainName(domain string) bool {
	if domain == "" || len(domain) > 253 {
		return false
	}
	for _, label := range strings.Split(domain, ".") {
		if label == "" || len(label) > 63 {
			return false
		}
	}
	return true
}

func isDNSNotFound(err error) bool {
	var dnsErr *net.DNSError
	return errors.As(err, &dnsErr) && dnsErr.IsNotFound
}

func isTemporaryError(err error) bool {
	type temporary interface{ Temporary() bool }
	var temp temporary
	return errors.As(err, &temp) && temp.Temporary()
}
