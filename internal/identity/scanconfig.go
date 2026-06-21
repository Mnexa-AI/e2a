package identity

import (
	"context"
	"fmt"
)

// Screening config enum values (migration 038 / Slice 3).
const (
	ScanActionFlag   = "flag"
	ScanActionReview = "review"
	ScanActionBlock  = "block"

	ScanOff = "off"
	ScanOn  = "on"

	OutboundPolicyOpen      = "open"
	OutboundPolicyAllowlist = "allowlist"
	OutboundPolicyDomain    = "domain"
)

// ScanConfig is the full per-agent content-screening config. All fields are
// concrete (already merged over the current values by the caller, per the
// additive-PATCH model) so UpdateAgentScanConfig can validate the EFFECTIVE posture
// and write it atomically.
type ScanConfig struct {
	InboundPolicyAction         string
	OutboundPolicy              string
	OutboundAllowlist           []string
	OutboundPolicyAction        string
	InboundScan                 string
	InboundScanReviewThreshold  float64
	InboundScanBlockThreshold   float64
	OutboundScan                string
	OutboundScanReviewThreshold float64
	OutboundScanBlockThreshold  float64
}

func validScanAction(a string) bool {
	return a == ScanActionFlag || a == ScanActionReview || a == ScanActionBlock
}

func validScanToggle(s string) bool { return s == ScanOff || s == ScanOn }

func validOutboundPolicy(p string) bool {
	return p == OutboundPolicyOpen || p == OutboundPolicyAllowlist || p == OutboundPolicyDomain
}

// ValidateScanConfig mirrors the migration-038 CHECK constraints so callers get a
// clean pre-query error rather than a raw constraint violation.
func ValidateScanConfig(c ScanConfig) error {
	if !validScanAction(c.InboundPolicyAction) {
		return fmt.Errorf("inbound_policy_action must be flag, review, or block")
	}
	if !validScanAction(c.OutboundPolicyAction) {
		return fmt.Errorf("outbound_policy_action must be flag, review, or block")
	}
	if !validOutboundPolicy(c.OutboundPolicy) {
		return fmt.Errorf("outbound_policy must be open, allowlist, or domain")
	}
	if len(c.OutboundAllowlist) > maxInboundAllowlist {
		return fmt.Errorf("outbound_allowlist has %d entries, max %d", len(c.OutboundAllowlist), maxInboundAllowlist)
	}
	if !validScanToggle(c.InboundScan) {
		return fmt.Errorf("inbound_scan must be off or on")
	}
	if !validScanToggle(c.OutboundScan) {
		return fmt.Errorf("outbound_scan must be off or on")
	}
	if err := validThresholds("inbound", c.InboundScanReviewThreshold, c.InboundScanBlockThreshold); err != nil {
		return err
	}
	if err := validThresholds("outbound", c.OutboundScanReviewThreshold, c.OutboundScanBlockThreshold); err != nil {
		return err
	}
	return nil
}

func validThresholds(dir string, review, block float64) error {
	if review < 0 || review > 1 || block < 0 || block > 1 {
		return fmt.Errorf("%s scan thresholds must be between 0 and 1", dir)
	}
	// Strict <: equal thresholds collapse the review band so a score at the cutoff
	// jumps straight to block — e.g. (0,0) blocks every clean message. Require a real
	// allow < review < block ladder.
	if review >= block {
		return fmt.Errorf("%s_scan_review_threshold must be < %s_scan_block_threshold", dir, dir)
	}
	return nil
}

// UpdateAgentScanConfig writes the full screening config for an agent owned by
// userID, validating the effective posture first. Returns an error if the agent
// isn't found or isn't owned by the user.
func (s *Store) UpdateAgentScanConfig(ctx context.Context, agentID, userID string, c ScanConfig) error {
	if err := ValidateScanConfig(c); err != nil {
		return err
	}
	tag, err := s.pool.Exec(ctx,
		`UPDATE agent_identities SET
		    inbound_policy_action = $3,
		    outbound_policy = $4, outbound_allowlist = $5, outbound_policy_action = $6,
		    inbound_scan = $7, inbound_scan_review_threshold = $8, inbound_scan_block_threshold = $9,
		    outbound_scan = $10, outbound_scan_review_threshold = $11, outbound_scan_block_threshold = $12
		  WHERE id = $1 AND user_id = $2`,
		agentID, userID,
		c.InboundPolicyAction,
		c.OutboundPolicy, c.OutboundAllowlist, c.OutboundPolicyAction,
		c.InboundScan, c.InboundScanReviewThreshold, c.InboundScanBlockThreshold,
		c.OutboundScan, c.OutboundScanReviewThreshold, c.OutboundScanBlockThreshold,
	)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("agent not found or not owned by user")
	}
	return nil
}
