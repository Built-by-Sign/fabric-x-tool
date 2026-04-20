package config

import "testing"

func TestResolveCommitterDatabaseTypeNormalizesExplicitType(t *testing.T) {
	cfg := &NetworkConfig{
		Committer: &CommitterConfig{
			Database: &CommitterDatabase{
				Type: " Yugabyte ",
			},
		},
	}

	if got := cfg.ResolveCommitterDatabaseType(); got != "yugabyte" {
		t.Fatalf("expected yugabyte, got %q", got)
	}
}

func TestValidateAcceptsNormalizedCommitterDatabaseType(t *testing.T) {
	cfg := &NetworkConfig{
		ChannelID: "arma",
		OrdererOrgs: []OrdererOrg{{
			Name:     "Orderer",
			Domain:   "example.com",
			Orderers: []Node{{Name: "orderer0", Type: "router"}},
		}},
		Committer: &CommitterConfig{
			Database: &CommitterDatabase{
				Type: " Yugabyte ",
				Endpoints: []DatabaseEndpoint{{
					Host: "yb.example.com",
					Port: 5433,
				}},
				Username: "fx",
				Password: "secret",
				Database: "fxdb",
			},
		},
	}

	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate returned error: %v", err)
	}
}

func TestCommitterComponentDirNameKeepsSingleTypeAnsibleStyle(t *testing.T) {
	cfg := &NetworkConfig{
		Committer: &CommitterConfig{
			Components: []CommitterNode{{Name: "validator", Type: "validator"}},
		},
	}

	if got := cfg.CommitterComponentDirName(cfg.Committer.Components[0]); got != "committer-validator" {
		t.Fatalf("expected committer-validator, got %q", got)
	}
}

func TestCommitterComponentDirNameUsesNameForRepeatedType(t *testing.T) {
	cfg := &NetworkConfig{
		Committer: &CommitterConfig{
			Components: []CommitterNode{
				{Name: "validator-a", Type: "validator"},
				{Name: "validator-b", Type: "validator"},
			},
		},
	}

	if got := cfg.CommitterComponentDirName(cfg.Committer.Components[0]); got != "committer-validator-a" {
		t.Fatalf("expected committer-validator-a, got %q", got)
	}
	if got := cfg.CommitterComponentDirName(cfg.Committer.Components[1]); got != "committer-validator-b" {
		t.Fatalf("expected committer-validator-b, got %q", got)
	}
}

func TestDefaultOrdererPort(t *testing.T) {
	tests := map[string]int{
		"router":    7050,
		"batcher":   7051,
		"consenter": 7052,
		"assembler": 7053,
		"unknown":   0,
	}

	for ordererType, want := range tests {
		if got := DefaultOrdererPort(ordererType); got != want {
			t.Fatalf("DefaultOrdererPort(%q) = %d, want %d", ordererType, got, want)
		}
	}
}

func TestValidateRejectsDuplicateCommitterNames(t *testing.T) {
	cfg := &NetworkConfig{
		ChannelID: "arma",
		OrdererOrgs: []OrdererOrg{{
			Name:     "Orderer",
			Domain:   "example.com",
			Orderers: []Node{{Name: "orderer0", Type: "router"}},
		}},
		Committer: &CommitterConfig{
			Components: []CommitterNode{
				{Name: "validator", Type: "validator"},
				{Name: "validator", Type: "verifier"},
			},
		},
	}

	if err := cfg.Validate(); err == nil {
		t.Fatal("expected duplicate committer component name to fail validation")
	}
}
