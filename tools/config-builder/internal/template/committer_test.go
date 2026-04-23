package template

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"config-builder/internal/config"
)

func TestSidecarTemplateV020Schema(t *testing.T) {
	cfg := &config.NetworkConfig{
		ChannelID: "arma",
		PeerOrgs:  []config.PeerOrg{{Name: "Org1", Domain: "org1.example.com"}},
		OrdererOrgs: []config.OrdererOrg{{
			Name: "OrdererOrg1",
			Orderers: []config.Node{{
				Name: "assembler1",
				Type: "assembler",
				Port: 7053,
			}},
		}},
		Committer: &config.CommitterConfig{
			Components: []config.CommitterNode{{Name: "committer-sidecar", Type: "sidecar", Port: 5130}},
		},
	}
	engine := NewEngine(cfg, t.TempDir(), false)

	data, err := engine.buildCommitterTemplateData("sidecar", &cfg.Committer.Components[0], t.TempDir())
	if err != nil {
		t.Fatalf("buildCommitterTemplateData returned error: %v", err)
	}
	tmpl, err := engine.getCommitterTemplate("sidecar")
	if err != nil {
		t.Fatalf("getCommitterTemplate returned error: %v", err)
	}
	var out bytes.Buffer
	if err := tmpl.Execute(&out, data); err != nil {
		t.Fatalf("execute sidecar template: %v", err)
	}
	rendered := out.String()

	for _, want := range []string{
		"msp-id: Org1MSP",
		"msp-dir: /config/msp",
		"fault-tolerance-level: BFT",
		"latest-known-config-block-path: /config/genesis.block",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("rendered sidecar config missing %q:\n%s", want, rendered)
		}
	}
	for _, banned := range []string{
		"organizations:",
		"channel-id:",
		"consensus-type:",
		"bootstrap:",
	} {
		if strings.Contains(rendered, banned) {
			t.Fatalf("rendered sidecar config still contains v0.1.9 field %q:\n%s", banned, rendered)
		}
	}
}

func TestCommitterTemplatesIncludeOfficialDefaultFields(t *testing.T) {
	engine := NewEngine(&config.NetworkConfig{}, t.TempDir(), false)

	checks := []struct {
		name     string
		rendered string
		wants    []string
		absent   []string
	}{
		{
			name: "validator",
			rendered: mustRenderCommitterTemplate(t, engine, "validator", &CommitterTemplateData{
				Port: 5100,
			}),
			wants: []string{
				"resource-limits:",
				"logSpec: info",
				"max-workers-for-preparer: 1",
				"max-workers-for-validator: 1",
				"max-workers-for-committer: 20",
				"timeout-for-min-transaction-batch-size: 2s",
			},
		},
		{
			name: "query-service",
			rendered: mustRenderCommitterTemplate(t, engine, "query-service", &CommitterTemplateData{
				Port: 5140,
			}),
			wants: []string{
				"max-active-views: 4096",
				"max-request-keys: 10000",
			},
		},
		{
			name: "coordinator",
			rendered: mustRenderCommitterTemplate(t, engine, "coordinator", &CommitterTemplateData{
				Port: 5120,
			}),
			absent: []string{
				"num-of-workers-for-global-dep-manager",
			},
		},
	}

	for _, check := range checks {
		for _, want := range check.wants {
			if !strings.Contains(check.rendered, want) {
				t.Fatalf("%s template missing %q:\n%s", check.name, want, check.rendered)
			}
		}
		for _, absent := range check.absent {
			if strings.Contains(check.rendered, absent) {
				t.Fatalf("%s template should not contain %q:\n%s", check.name, absent, check.rendered)
			}
		}
	}
}

func TestCommitterTemplatesRenderTLSSections(t *testing.T) {
	cfg := &config.NetworkConfig{
		ChannelID: "arma",
		TLS:       &config.TLSConfig{Enabled: true, ClientAuthRequired: true},
		PeerOrgs:  []config.PeerOrg{{Name: "Org1", Domain: "org1.example.com"}},
		OrdererOrgs: []config.OrdererOrg{{
			Name: "OrdererOrg1",
			Orderers: []config.Node{{
				Name: "assembler1",
				Type: "assembler",
				Port: 7053,
			}},
		}},
		Committer: &config.CommitterConfig{
			Components: []config.CommitterNode{
				{Name: "validator", Type: "validator", Port: 5100},
				{Name: "verifier", Type: "verifier", Port: 5110},
				{Name: "coordinator", Type: "coordinator", Port: 5120},
				{Name: "committer-sidecar", Type: "sidecar", Port: 5130},
			},
		},
	}
	engine := NewEngine(cfg, t.TempDir(), false)

	coordinatorData, err := engine.buildCommitterTemplateData("coordinator", &cfg.Committer.Components[2], t.TempDir())
	if err != nil {
		t.Fatalf("build coordinator data: %v", err)
	}
	coordinatorTemplate, err := engine.getCommitterTemplate("coordinator")
	if err != nil {
		t.Fatalf("get coordinator template: %v", err)
	}
	var coordinatorOut bytes.Buffer
	if err := coordinatorTemplate.Execute(&coordinatorOut, coordinatorData); err != nil {
		t.Fatalf("execute coordinator template: %v", err)
	}
	coordinatorRendered := coordinatorOut.String()
	for _, want := range []string{
		"mode: mtls",
		"- /config/tls/verifiers/ca.crt",
		"- /config/tls/validators/ca.crt",
		"key-path: /config/tls/server.key",
	} {
		if !strings.Contains(coordinatorRendered, want) {
			t.Fatalf("coordinator TLS config missing %q:\n%s", want, coordinatorRendered)
		}
	}

	sidecarData, err := engine.buildCommitterTemplateData("sidecar", &cfg.Committer.Components[3], t.TempDir())
	if err != nil {
		t.Fatalf("build sidecar data: %v", err)
	}
	sidecarTemplate, err := engine.getCommitterTemplate("sidecar")
	if err != nil {
		t.Fatalf("get sidecar template: %v", err)
	}
	var sidecarOut bytes.Buffer
	if err := sidecarTemplate.Execute(&sidecarOut, sidecarData); err != nil {
		t.Fatalf("execute sidecar template: %v", err)
	}
	sidecarRendered := sidecarOut.String()
	for _, want := range []string{
		"common-ca-cert-paths:",
		"- /config/tls/orderers/OrdererOrg1/assembler1/ca.crt",
		"endpoint: host.docker.internal:5120",
		"- /config/tls/coordinator/ca.crt",
	} {
		if !strings.Contains(sidecarRendered, want) {
			t.Fatalf("sidecar TLS config missing %q:\n%s", want, sidecarRendered)
		}
	}
	if strings.Contains(sidecarRendered, "/assembler1/server.crt") {
		t.Fatalf("v0.2.0 sidecar should not pin per-orderer server.crt (loaded from config block):\n%s", sidecarRendered)
	}
}

func TestSidecarCommonCACertPathsSpanAllOrdererOrgs(t *testing.T) {
	cfg := &config.NetworkConfig{
		ChannelID: "arma",
		TLS:       &config.TLSConfig{Enabled: true},
		PeerOrgs:  []config.PeerOrg{{Name: "Org1", Domain: "org1.example.com"}},
		OrdererOrgs: []config.OrdererOrg{
			{
				Name: "OrdererOrg1",
				Orderers: []config.Node{{
					Name: "assembler0",
					Type: "assembler",
					Port: 7053,
				}},
			},
			{
				Name: "OrdererOrg2",
				Orderers: []config.Node{{
					Name: "assembler0",
					Type: "assembler",
					Port: 8053,
				}},
			},
		},
		Committer: &config.CommitterConfig{
			Components: []config.CommitterNode{{Name: "committer-sidecar", Type: "sidecar", Port: 5130}},
		},
	}
	engine := NewEngine(cfg, t.TempDir(), false)

	data, err := engine.buildCommitterTemplateData("sidecar", &cfg.Committer.Components[0], t.TempDir())
	if err != nil {
		t.Fatalf("build sidecar data: %v", err)
	}
	rendered := mustRenderCommitterTemplate(t, engine, "sidecar", data)

	for _, want := range []string{
		"- /config/tls/orderers/OrdererOrg1/assembler0/ca.crt",
		"- /config/tls/orderers/OrdererOrg2/assembler0/ca.crt",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("sidecar TLS config missing %q:\n%s", want, rendered)
		}
	}
}

func TestCopyCommitterDatabaseTLSCopiesCACert(t *testing.T) {
	srcDir := t.TempDir()
	srcCA := filepath.Join(srcDir, "db-ca.pem")
	if err := os.WriteFile(srcCA, []byte("db-ca"), 0644); err != nil {
		t.Fatalf("write source CA: %v", err)
	}

	engine := NewEngine(&config.NetworkConfig{
		Committer: &config.CommitterConfig{
			Database: &config.CommitterDatabase{
				TLS: &config.DatabaseTLS{
					Enabled:    true,
					CACertPath: srcCA,
				},
			},
		},
	}, t.TempDir(), false)

	dstDir := t.TempDir()
	if err := engine.copyCommitterDatabaseTLS(dstDir); err != nil {
		t.Fatalf("copyCommitterDatabaseTLS: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(dstDir, "tls", "db", "ca.crt"))
	if err != nil {
		t.Fatalf("read copied CA: %v", err)
	}
	if string(got) != "db-ca" {
		t.Fatalf("unexpected copied CA contents: %q", string(got))
	}
}

func TestLocalPostgresDatabaseConfigUsesTLSCA(t *testing.T) {
	cfg := &config.NetworkConfig{
		TLS: &config.TLSConfig{Enabled: true},
		Committer: &config.CommitterConfig{
			UsePostgres: true,
			Components: []config.CommitterNode{
				{Name: "db", Type: "db", Port: 15432, PostgresUser: "postgres", PostgresPassword: "secret", PostgresDB: "fxdb"},
				{Name: "validator", Type: "validator", Port: 5100},
			},
		},
	}
	engine := NewEngine(cfg, t.TempDir(), false)

	data, err := engine.buildCommitterTemplateData("validator", &cfg.Committer.Components[1], t.TempDir())
	if err != nil {
		t.Fatalf("build validator data: %v", err)
	}
	rendered := mustRenderCommitterTemplate(t, engine, "validator", data)

	for _, want := range []string{
		"- host.docker.internal:15432",
		"mode: tls",
		"ca-cert-path: /config/tls/db/ca.crt",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("expected local postgres TLS database config to contain %q:\n%s", want, rendered)
		}
	}
}

func TestExternalYugabyteDatabaseDefaultsTablePreSplitToEndpointCount(t *testing.T) {
	cfg := &config.NetworkConfig{
		Committer: &config.CommitterConfig{
			Database: &config.CommitterDatabase{
				Type: "yugabyte",
				Endpoints: []config.DatabaseEndpoint{
					{Host: "yb-1.example.com", Port: 5433},
					{Host: "yb-2.example.com", Port: 5433},
					{Host: "yb-3.example.com", Port: 5433},
				},
				Username: "fx",
				Password: "secret",
				Database: "fxdb",
			},
			Components: []config.CommitterNode{{Name: "validator", Type: "validator", Port: 5100}},
		},
	}
	engine := NewEngine(cfg, t.TempDir(), false)

	data, err := engine.buildCommitterTemplateData("validator", &cfg.Committer.Components[0], t.TempDir())
	if err != nil {
		t.Fatalf("build validator data: %v", err)
	}
	rendered := mustRenderCommitterTemplate(t, engine, "validator", data)

	for _, want := range []string{
		"load-balance: true",
		"table-pre-split-tablets: 3",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("expected external yugabyte config to contain %q:\n%s", want, rendered)
		}
	}
}

func TestCopyCommitterDatabaseTLSCopiesLocalPostgresCACert(t *testing.T) {
	outputDir := t.TempDir()
	dbTLSDir := filepath.Join(
		outputDir,
		"build", "config", "cryptogen-artifacts", "crypto",
		"peerOrganizations", "org1.example.com", "peers", "db.org1.example.com", "tls",
	)
	if err := os.MkdirAll(dbTLSDir, 0750); err != nil {
		t.Fatalf("create db TLS dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dbTLSDir, "ca.crt"), []byte("local-db-ca"), 0644); err != nil {
		t.Fatalf("write db CA: %v", err)
	}

	engine := NewEngine(&config.NetworkConfig{
		TLS:      &config.TLSConfig{Enabled: true},
		PeerOrgs: []config.PeerOrg{{Name: "Org1", Domain: "org1.example.com"}},
		Committer: &config.CommitterConfig{
			UsePostgres: true,
			Components: []config.CommitterNode{
				{Name: "db", Type: "db"},
				{Name: "validator", Type: "validator"},
			},
		},
	}, outputDir, false)

	dstDir := t.TempDir()
	if err := engine.copyCommitterDatabaseTLS(dstDir); err != nil {
		t.Fatalf("copyCommitterDatabaseTLS: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(dstDir, "tls", "db", "ca.crt"))
	if err != nil {
		t.Fatalf("read copied CA: %v", err)
	}
	if string(got) != "local-db-ca" {
		t.Fatalf("unexpected copied CA contents: %q", string(got))
	}
}

func mustRenderCommitterTemplate(t *testing.T, engine *Engine, componentType string, data *CommitterTemplateData) string {
	t.Helper()

	tmpl, err := engine.getCommitterTemplate(componentType)
	if err != nil {
		t.Fatalf("get %s template: %v", componentType, err)
	}

	var out bytes.Buffer
	if err := tmpl.Execute(&out, data); err != nil {
		t.Fatalf("execute %s template: %v", componentType, err)
	}
	return out.String()
}
