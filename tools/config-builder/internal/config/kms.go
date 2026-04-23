package config

import (
	"fmt"
	"strings"
)

// DeriveMSPIDBase returns the org name with any trailing "MSP" stripped.
// Used as the base token inside configtx.yaml (anchor names, etc.).
func DeriveMSPIDBase(orgName string) string {
	return strings.TrimSuffix(orgName, "MSP")
}

// DeriveMSPID derives the canonical MSP ID for an organization name.
// Genesis always emits the ID as "<base>MSP", so callers that need to
// reference that same ID (sidecar identity, etc.) must go through this helper
// to stay in sync.
func DeriveMSPID(orgName string) string {
	return DeriveMSPIDBase(orgName) + "MSP"
}

// ResolveKMSUserPin returns the KMS user PIN for a peer organization.
// Precedence: org.KMSUserPin > first peer's UserPin.
func (o *PeerOrg) ResolveKMSUserPin() (string, error) {
	if o.KMSUserPin != "" {
		return o.KMSUserPin, nil
	}
	if len(o.Peers) > 0 && o.Peers[0].UserPin != "" {
		return o.Peers[0].UserPin, nil
	}
	return "", fmt.Errorf("peer org %q: kms_user_pin not configured (set peer_orgs[].kms_user_pin or peers[0].user_pin)", o.Name)
}

// ResolveKMSUserPin returns the KMS user PIN for an orderer organization.
// Precedence: org.KMSUserPin > first orderer's UserPin.
func (o *OrdererOrg) ResolveKMSUserPin() (string, error) {
	if o.KMSUserPin != "" {
		return o.KMSUserPin, nil
	}
	if len(o.Orderers) > 0 && o.Orderers[0].UserPin != "" {
		return o.Orderers[0].UserPin, nil
	}
	return "", fmt.Errorf("orderer org %q: kms_user_pin not configured (set orderer_orgs[].kms_user_pin or orderers[0].user_pin)", o.Name)
}

// ResolveKMSTokenLabel returns the KMS token label to use.
// Precedence: global KMS.TokenLabel > org-level label. No silent default —
// callers in the same deployment must agree on the label, so we fail loud
// instead of letting crypto and runtime disagree.
func (c *NetworkConfig) ResolveKMSTokenLabel(orgLabel string) (string, error) {
	if c.KMS != nil && c.KMS.TokenLabel != "" {
		return c.KMS.TokenLabel, nil
	}
	if orgLabel != "" {
		return orgLabel, nil
	}
	return "", fmt.Errorf("kms token label not configured (set kms.token_label or org-level kms_token_label)")
}
