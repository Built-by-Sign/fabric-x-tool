package crypto

import (
	"os"
	"strings"
	"testing"

	"config-builder/internal/config"
)

func TestBuildCryptoConfigAddsSidecarPeerMSP(t *testing.T) {
	cfg := &config.NetworkConfig{
		PeerOrgs: []config.PeerOrg{{
			Name:   "Org1",
			Domain: "org1.example.com",
			Peers:  []config.Node{{Name: "peer0"}},
		}},
		Committer: &config.CommitterConfig{
			Components: []config.CommitterNode{{Name: "committer-sidecar", Type: "sidecar"}},
		},
	}

	cc := NewGenerator(cfg, t.TempDir(), false).buildCryptoConfig()
	if len(cc.PeerOrgs) != 1 {
		t.Fatalf("expected one peer org, got %d", len(cc.PeerOrgs))
	}
	var found bool
	for _, spec := range cc.PeerOrgs[0].Specs {
		if spec.Hostname == "committer-sidecar" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected committer-sidecar peer spec, got %#v", cc.PeerOrgs[0].Specs)
	}
}

func TestBuildCryptoConfigAddsCommitterTLSIdentitiesWhenTLSEnabled(t *testing.T) {
	cfg := &config.NetworkConfig{
		TLS: &config.TLSConfig{Enabled: true},
		PeerOrgs: []config.PeerOrg{{
			Name:   "Org1",
			Domain: "org1.example.com",
			Peers:  []config.Node{{Name: "peer0"}},
		}},
		Committer: &config.CommitterConfig{
			Components: []config.CommitterNode{
				{Name: "db", Type: "db"},
				{Name: "validator", Type: "validator"},
				{Name: "verifier", Type: "verifier"},
				{Name: "committer-sidecar", Type: "sidecar"},
			},
		},
	}

	cc := NewGenerator(cfg, t.TempDir(), false).buildCryptoConfig()
	got := make(map[string]bool)
	for _, spec := range cc.PeerOrgs[0].Specs {
		got[spec.Hostname] = true
	}
	for _, want := range []string{"validator", "verifier", "committer-sidecar"} {
		if !got[want] {
			t.Fatalf("expected %s peer spec, got %#v", want, cc.PeerOrgs[0].Specs)
		}
	}
	if got["db"] {
		t.Fatalf("did not expect db peer spec, got %#v", cc.PeerOrgs[0].Specs)
	}
}

func TestGenerateCryptoConfigUsesVendoredTemplate(t *testing.T) {
	cfg := &config.NetworkConfig{
		TLS: &config.TLSConfig{Enabled: true},
		OrdererOrgs: []config.OrdererOrg{{
			Name:   "Orderer",
			Domain: "example.com",
			Orderers: []config.Node{
				{Name: "orderer0", Type: "router"},
			},
		}},
		PeerOrgs: []config.PeerOrg{{
			Name:   "Org1",
			Domain: "org1.example.com",
			Peers:  []config.Node{{Name: "peer0"}},
			Users:  []config.User{{Name: "Admin"}, {Name: "User1"}},
		}},
		Committer: &config.CommitterConfig{
			Components: []config.CommitterNode{
				{Name: "validator", Type: "validator"},
				{Name: "committer-sidecar", Type: "sidecar"},
			},
		},
	}

	path, err := NewGenerator(cfg, t.TempDir(), false).generateCryptoConfig()
	if err != nil {
		t.Fatalf("generateCryptoConfig: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read crypto-config: %v", err)
	}
	rendered := string(data)

	for _, want := range []string{
		"OrdererOrgs:",
		"PeerOrgs:",
		"Hostname: orderer0",
		"Hostname: peer0",
		"Hostname: validator",
		"Hostname: committer-sidecar",
		"EnableNodeOUs: false",
		"Name: User1",
		"- host.docker.internal",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("crypto-config missing %q:\n%s", want, rendered)
		}
	}
	if strings.Contains(rendered, "Hostname: db") {
		t.Fatalf("crypto-config should not include db identity:\n%s", rendered)
	}
}
