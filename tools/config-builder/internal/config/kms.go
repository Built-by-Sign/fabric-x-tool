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

// ResolveMSPCAURL returns the fabric-ca-server URL used for MSP identity enroll.
// Precedence: org.CAURL > globalCAURL (kms.ca_url).
func (o *OrdererOrg) ResolveMSPCAURL(globalCAURL string) (string, error) {
	if o.CAURL != "" {
		return o.CAURL, nil
	}
	if globalCAURL != "" {
		return globalCAURL, nil
	}
	return "", fmt.Errorf("orderer org %q: ca_url not configured (set orderer_orgs[].ca_url or kms.ca_url)", o.Name)
}

// ResolveMSPCAURL returns the fabric-ca-server URL used for MSP identity enroll.
// Precedence: org.CAURL > globalCAURL (kms.ca_url).
func (o *PeerOrg) ResolveMSPCAURL(globalCAURL string) (string, error) {
	if o.CAURL != "" {
		return o.CAURL, nil
	}
	if globalCAURL != "" {
		return globalCAURL, nil
	}
	return "", fmt.Errorf("peer org %q: ca_url not configured (set peer_orgs[].ca_url or kms.ca_url)", o.Name)
}

// ResolveTLSCAURL returns the fabric-ca-server URL used for TLS leaf enroll.
// Precedence: org.TLSCAURL > MSP CA URL (shared-server fallback).
func (o *OrdererOrg) ResolveTLSCAURL(globalCAURL string) (string, error) {
	if o.TLSCAURL != "" {
		return o.TLSCAURL, nil
	}
	return o.ResolveMSPCAURL(globalCAURL)
}

// ResolveTLSCAURL returns the fabric-ca-server URL used for TLS leaf enroll.
// Precedence: org.TLSCAURL > MSP CA URL (shared-server fallback).
func (o *PeerOrg) ResolveTLSCAURL(globalCAURL string) (string, error) {
	if o.TLSCAURL != "" {
		return o.TLSCAURL, nil
	}
	return o.ResolveMSPCAURL(globalCAURL)
}

// ResolveSetupPin returns the PKCS#11 BCCSP PIN used during setup-kms MSP enroll.
// Precedence: org.KMSSetupPin > runtime PIN (KMSUserPin / node fallback).
// Older yaml without kms_setup_pin keeps the previous one-PIN behavior.
func (o *OrdererOrg) ResolveSetupPin() (string, error) {
	if o.KMSSetupPin != "" {
		return o.KMSSetupPin, nil
	}
	return o.ResolveKMSUserPin()
}

// ResolveSetupPin returns the PKCS#11 BCCSP PIN used during setup-kms MSP enroll.
// Precedence: org.KMSSetupPin > runtime PIN (KMSUserPin / node fallback).
// Older yaml without kms_setup_pin keeps the previous one-PIN behavior.
func (o *PeerOrg) ResolveSetupPin() (string, error) {
	if o.KMSSetupPin != "" {
		return o.KMSSetupPin, nil
	}
	return o.ResolveKMSUserPin()
}

// ResolveTLSHosts builds the SAN list used for a node's TLS leaf certificate.
// Order: nodeFQDN, node.Host, defaults, org tls_hosts. The result is
// deduplicated while preserving the first occurrence so the FQDN stays at
// index 0 (some clients pin SAN[0]).
func ResolveTLSHosts(defaults []string, orgTLSHosts []string, node *Node, domain string) []string {
	if node == nil {
		return nil
	}
	hosts := make([]string, 0, 2+len(defaults)+len(orgTLSHosts))
	if node.Name != "" && domain != "" {
		hosts = append(hosts, fmt.Sprintf("%s.%s", node.Name, domain))
	}
	if node.Host != "" {
		hosts = append(hosts, node.Host)
	}
	hosts = append(hosts, defaults...)
	hosts = append(hosts, orgTLSHosts...)
	return dedupStrings(hosts)
}

func dedupStrings(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if s == "" {
			continue
		}
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}
