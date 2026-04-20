package armageddon

import (
	"os"
	"strings"
	"testing"

	"config-builder/internal/config"
)

func TestGenerateSharedConfigYamlUsesVendoredTemplate(t *testing.T) {
	outputDir := t.TempDir()
	cfg := &config.NetworkConfig{
		OrdererOrgs: []config.OrdererOrg{{
			Name:   "Orderer",
			Domain: "example.com",
			Orderers: []config.Node{
				{Name: "router0", Type: "router", Port: 7050},
				{Name: "batcher0", Type: "batcher", Port: 7051, ShardID: 7},
				{Name: "consenter0", Type: "consenter", Port: 7052},
				{Name: "assembler0", Type: "assembler", Port: 7053},
			},
		}},
	}

	g := NewGenerator(cfg, outputDir, false)
	configPath, err := g.generateSharedConfigYaml()
	if err != nil {
		t.Fatalf("generateSharedConfigYaml: %v", err)
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read generated shared config: %v", err)
	}
	rendered := string(data)

	for _, want := range []string{
		"PartyID: 1",
		"Host: " + getDefaultHost(),
		"ShardID: 7",
		"requestbatchmaxcount: 100",
		"RequestMaxBytes: 1048576",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("generated shared config missing %q:\n%s", want, rendered)
		}
	}
}

func TestSharedConfigIncludesAllBatchers(t *testing.T) {
	outputDir := t.TempDir()
	cfg := &config.NetworkConfig{
		OrdererOrgs: []config.OrdererOrg{{
			Name:   "Orderer",
			Domain: "example.com",
			Orderers: []config.Node{
				{Name: "router0", Type: "router", Port: 7050},
				{Name: "batcher0", Type: "batcher", Host: "batcher0.example.com", Port: 7051, ShardID: 0},
				{Name: "batcher1", Type: "batcher", Host: "batcher1.example.com", Port: 8051, ShardID: 1},
				{Name: "consenter0", Type: "consenter", Port: 7052},
				{Name: "assembler0", Type: "assembler", Port: 7053},
			},
		}},
	}

	g := NewGenerator(cfg, outputDir, false)
	configPath, err := g.generateSharedConfigYaml()
	if err != nil {
		t.Fatalf("generateSharedConfigYaml: %v", err)
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read generated shared config: %v", err)
	}
	rendered := string(data)

	for _, want := range []string{
		"ShardID: 0",
		"Host: batcher0.example.com",
		"Port: 7051",
		"ShardID: 1",
		"Host: batcher1.example.com",
		"Port: 8051",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("generated shared config missing %q:\n%s", want, rendered)
		}
	}
}
