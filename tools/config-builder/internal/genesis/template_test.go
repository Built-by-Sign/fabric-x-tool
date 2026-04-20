package genesis

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"config-builder/internal/config"
)

func TestGenerateConfigtxFromTemplateUsesVendoredTemplate(t *testing.T) {
	outputDir := t.TempDir()
	cfg := &config.NetworkConfig{
		ChannelID: "arma",
		OrdererOrgs: []config.OrdererOrg{{
			Name:   "Orderer",
			Domain: "example.com",
			Orderers: []config.Node{
				{Name: "router0", Type: "router", Host: "router0.example.com", Port: 7050},
				{Name: "assembler0", Type: "assembler", Host: "assembler0.example.com", Port: 7053},
				{Name: "consenter0", Type: "consenter", Host: "consenter0.example.com", Port: 7052},
			},
		}},
		PeerOrgs: []config.PeerOrg{{
			Name:   "Org1",
			Domain: "org1.example.com",
		}},
	}

	g := NewGenerator(cfg, outputDir, false)
	configPath, err := g.generateConfigtxFromTemplate()
	if err != nil {
		t.Fatalf("generateConfigtxFromTemplate: %v", err)
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read generated configtx: %v", err)
	}
	rendered := string(data)

	for _, want := range []string{
		"OrdererType: arma",
		"Rule: OR('Org1MSP.member')",
		"id=1,broadcast,router0.example.com:7050",
		"id=1,deliver,assembler0.example.com:7053",
		"ID: 1",
		"Host: consenter0.example.com",
		filepath.Join(outputDir, "build", "config", "armageddon-artifacts", "shared_config.binpb"),
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("generated configtx missing %q:\n%s", want, rendered)
		}
	}
}

func TestGenerateConfigtxUsesPartyIDsForOrdererEndpoints(t *testing.T) {
	outputDir := t.TempDir()
	cfg := &config.NetworkConfig{
		ChannelID: "arma",
		OrdererOrgs: []config.OrdererOrg{
			{
				Name:   "OrdererOne",
				Domain: "one.example.com",
				Orderers: []config.Node{
					{Name: "router0", Type: "router", Host: "router0.one.example.com", Port: 7050},
					{Name: "assembler0", Type: "assembler", Host: "assembler0.one.example.com", Port: 7053},
				},
			},
			{
				Name:   "OrdererTwo",
				Domain: "two.example.com",
				Orderers: []config.Node{
					{Name: "router0", Type: "router", Host: "router0.two.example.com", Port: 8050},
					{Name: "assembler0", Type: "assembler", Host: "assembler0.two.example.com", Port: 8053},
				},
			},
		},
	}

	g := NewGenerator(cfg, outputDir, false)
	configPath, err := g.generateConfigtxFromTemplate()
	if err != nil {
		t.Fatalf("generateConfigtxFromTemplate: %v", err)
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read generated configtx: %v", err)
	}
	rendered := string(data)

	for _, want := range []string{
		"id=1,broadcast,router0.one.example.com:7050",
		"id=1,deliver,assembler0.one.example.com:7053",
		"id=2,broadcast,router0.two.example.com:8050",
		"id=2,deliver,assembler0.two.example.com:8053",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("generated configtx missing %q:\n%s", want, rendered)
		}
	}
}

func TestGenerateConfigtxUsesPartyIDsForConsenterMapping(t *testing.T) {
	outputDir := t.TempDir()
	cfg := &config.NetworkConfig{
		ChannelID: "arma",
		OrdererOrgs: []config.OrdererOrg{
			{
				Name:   "OrdererOne",
				Domain: "one.example.com",
				Orderers: []config.Node{{
					Name: "consenter0",
					Type: "consenter",
					Host: "consenter0.one.example.com",
					Port: 7052,
				}},
			},
			{
				Name:   "OrdererTwo",
				Domain: "two.example.com",
				Orderers: []config.Node{{
					Name: "consenter0",
					Type: "consenter",
					Host: "consenter0.two.example.com",
					Port: 8052,
				}},
			},
		},
	}

	g := NewGenerator(cfg, outputDir, false)
	data := g.buildTemplateData()
	if len(data.Consenters) != 2 {
		t.Fatalf("expected two consenters, got %#v", data.Consenters)
	}
	if data.Consenters[0].ID != 1 || data.Consenters[1].ID != 2 {
		t.Fatalf("expected consenter IDs [1 2], got %#v", data.Consenters)
	}
}

func TestOrdererEndpointsUseAssemblerDefaultPort(t *testing.T) {
	cfg := &config.NetworkConfig{}
	g := NewGenerator(cfg, t.TempDir(), false)

	endpoints := g.getOrdererEndpoints(&config.OrdererOrg{
		Orderers: []config.Node{{
			Name: "assembler0",
			Type: "assembler",
			Host: "assembler0.example.com",
		}},
	}, 3)

	if len(endpoints) != 1 {
		t.Fatalf("expected one endpoint, got %#v", endpoints)
	}
	if endpoints[0] != "id=3,deliver,assembler0.example.com:7053" {
		t.Fatalf("unexpected endpoint: %s", endpoints[0])
	}
}

func TestOrdererEndpointsUseDefaultHost(t *testing.T) {
	g := NewGenerator(&config.NetworkConfig{}, t.TempDir(), false)

	endpoints := g.getOrdererEndpoints(&config.OrdererOrg{
		Orderers: []config.Node{{
			Name: "router0",
			Type: "router",
			Port: 7050,
		}},
	}, 1)

	if len(endpoints) != 1 {
		t.Fatalf("expected one endpoint, got %#v", endpoints)
	}
	want := "id=1,broadcast," + getDefaultHost() + ":7050"
	if endpoints[0] != want {
		t.Fatalf("expected endpoint %q, got %q", want, endpoints[0])
	}
}
