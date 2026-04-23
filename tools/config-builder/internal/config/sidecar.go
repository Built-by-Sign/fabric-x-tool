package config

import "fmt"

const defaultSidecarIdentityName = "committer-sidecar"

// HasCommitterSidecar reports whether this network includes a committer sidecar.
func (c *NetworkConfig) HasCommitterSidecar() bool {
	if c.Committer == nil {
		return false
	}
	for _, component := range c.Committer.Components {
		if component.Type == "sidecar" {
			return true
		}
	}
	return false
}

// ResolveSidecarIdentity resolves the peer organization and peer identity name
// used by a sidecar component. This mirrors the Ansible collection model: the
// sidecar has its own peer MSP under the selected organization, instead of
// borrowing an admin user MSP.
func (c *NetworkConfig) ResolveSidecarIdentity(componentName string) (*PeerOrg, string, error) {
	if len(c.PeerOrgs) == 0 {
		return nil, "", fmt.Errorf("committer sidecar requires at least one peer organization")
	}

	orgName := ""
	identityName := componentName
	if c.Committer != nil && c.Committer.SidecarIdentity != nil {
		orgName = c.Committer.SidecarIdentity.Org
		if c.Committer.SidecarIdentity.Name != "" {
			identityName = c.Committer.SidecarIdentity.Name
		}
	}
	if identityName == "" {
		identityName = defaultSidecarIdentityName
	}

	if orgName == "" {
		return &c.PeerOrgs[0], identityName, nil
	}
	for i := range c.PeerOrgs {
		org := &c.PeerOrgs[i]
		if org.Name == orgName || DeriveMSPID(org.Name) == orgName || DeriveMSPIDBase(org.Name) == orgName {
			return org, identityName, nil
		}
	}
	return nil, "", fmt.Errorf("committer sidecar identity org %q does not match any peer org", orgName)
}

// ResolveCommitterCryptoIdentity resolves the peer organization and peer
// identity name used for a committer component's TLS material. The Ansible
// collection models committer hosts as peer identities under an organization;
// sidecar additionally uses the same identity as its MSP signer.
func (c *NetworkConfig) ResolveCommitterCryptoIdentity(componentName, componentType string) (*PeerOrg, string, error) {
	if componentType == "sidecar" {
		return c.ResolveSidecarIdentity(componentName)
	}
	if len(c.PeerOrgs) == 0 {
		return nil, "", fmt.Errorf("committer TLS requires at least one peer organization")
	}
	if componentName == "" {
		componentName = defaultSidecarIdentityName
	}
	return &c.PeerOrgs[0], componentName, nil
}

// ResolveSidecarIdentitiesByOrg returns the sidecar peer identity names that
// should be generated under each peer organization.
func (c *NetworkConfig) ResolveSidecarIdentitiesByOrg() (map[string][]string, error) {
	result := make(map[string][]string)
	if c.Committer == nil {
		return result, nil
	}
	seen := make(map[string]map[string]struct{})
	for _, component := range c.Committer.Components {
		if component.Type != "sidecar" {
			continue
		}
		org, identityName, err := c.ResolveSidecarIdentity(component.Name)
		if err != nil {
			return nil, err
		}
		if seen[org.Name] == nil {
			seen[org.Name] = make(map[string]struct{})
		}
		if _, exists := seen[org.Name][identityName]; exists {
			continue
		}
		seen[org.Name][identityName] = struct{}{}
		result[org.Name] = append(result[org.Name], identityName)
	}
	return result, nil
}

// ResolveCommitterCryptoIdentitiesByOrg returns peer identity names that should
// exist so committer components can mount TLS material in the same shape as the
// Ansible collection.
func (c *NetworkConfig) ResolveCommitterCryptoIdentitiesByOrg() (map[string][]string, error) {
	result := make(map[string][]string)
	if c.Committer == nil {
		return result, nil
	}
	seen := make(map[string]map[string]struct{})
	for _, component := range c.Committer.Components {
		if component.Type == "db" && (c.TLS == nil || !c.TLS.Enabled) {
			continue
		}
		org, identityName, err := c.ResolveCommitterCryptoIdentity(component.Name, component.Type)
		if err != nil {
			return nil, err
		}
		if seen[org.Name] == nil {
			seen[org.Name] = make(map[string]struct{})
		}
		if _, exists := seen[org.Name][identityName]; exists {
			continue
		}
		seen[org.Name][identityName] = struct{}{}
		result[org.Name] = append(result[org.Name], identityName)
	}
	return result, nil
}
