package config

import "testing"

func TestResolveSidecarIdentityDefaultsToFirstPeerOrgAndComponentName(t *testing.T) {
	cfg := &NetworkConfig{
		PeerOrgs: []PeerOrg{
			{Name: "Org1", Domain: "org1.example.com"},
			{Name: "Org2", Domain: "org2.example.com"},
		},
		Committer: &CommitterConfig{
			Components: []CommitterNode{{Name: "committer-sidecar", Type: "sidecar"}},
		},
	}

	org, name, err := cfg.ResolveSidecarIdentity("committer-sidecar")
	if err != nil {
		t.Fatalf("ResolveSidecarIdentity returned error: %v", err)
	}
	if org.Name != "Org1" {
		t.Fatalf("expected Org1, got %s", org.Name)
	}
	if name != "committer-sidecar" {
		t.Fatalf("expected committer-sidecar, got %s", name)
	}
}

func TestResolveSidecarIdentityUsesExplicitOrgAndName(t *testing.T) {
	cfg := &NetworkConfig{
		PeerOrgs: []PeerOrg{
			{Name: "Org1", Domain: "org1.example.com"},
			{Name: "Org2", Domain: "org2.example.com"},
		},
		Committer: &CommitterConfig{
			SidecarIdentity: &SidecarIdentityConfig{Org: "Org2MSP", Name: "sc"},
			Components:      []CommitterNode{{Name: "committer-sidecar", Type: "sidecar"}},
		},
	}

	org, name, err := cfg.ResolveSidecarIdentity("committer-sidecar")
	if err != nil {
		t.Fatalf("ResolveSidecarIdentity returned error: %v", err)
	}
	if org.Name != "Org2" {
		t.Fatalf("expected Org2, got %s", org.Name)
	}
	if name != "sc" {
		t.Fatalf("expected sc, got %s", name)
	}
}

func TestResolveSidecarIdentitiesByOrgDeduplicatesExplicitName(t *testing.T) {
	cfg := &NetworkConfig{
		PeerOrgs: []PeerOrg{{Name: "Org1", Domain: "org1.example.com"}},
		Committer: &CommitterConfig{
			SidecarIdentity: &SidecarIdentityConfig{Name: "sidecar-peer"},
			Components: []CommitterNode{
				{Name: "sidecar-a", Type: "sidecar"},
				{Name: "sidecar-b", Type: "sidecar"},
			},
		},
	}

	byOrg, err := cfg.ResolveSidecarIdentitiesByOrg()
	if err != nil {
		t.Fatalf("ResolveSidecarIdentitiesByOrg returned error: %v", err)
	}
	if got := byOrg["Org1"]; len(got) != 1 || got[0] != "sidecar-peer" {
		t.Fatalf("expected one sidecar-peer identity, got %#v", got)
	}
}

func TestResolveCommitterCryptoIdentitiesByOrgIncludesNonDBComponents(t *testing.T) {
	cfg := &NetworkConfig{
		PeerOrgs: []PeerOrg{{Name: "Org1", Domain: "org1.example.com"}},
		Committer: &CommitterConfig{
			Components: []CommitterNode{
				{Name: "db", Type: "db"},
				{Name: "validator", Type: "validator"},
				{Name: "verifier", Type: "verifier"},
				{Name: "committer-sidecar", Type: "sidecar"},
			},
		},
	}

	byOrg, err := cfg.ResolveCommitterCryptoIdentitiesByOrg()
	if err != nil {
		t.Fatalf("ResolveCommitterCryptoIdentitiesByOrg returned error: %v", err)
	}
	got := byOrg["Org1"]
	want := []string{"validator", "verifier", "committer-sidecar"}
	if len(got) != len(want) {
		t.Fatalf("expected %v, got %v", want, got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("expected %v, got %v", want, got)
		}
	}
}
