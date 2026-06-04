package template

import (
	"bytes"
	"strings"
	"testing"

	"config-builder/internal/config"
)

// TestOrdererTemplatesEmitOperationsAndMetrics verifies the fabric-x-orderer
// v1.0.0 (#853) schema: General.MonitoringListenAddress/Port moved to
// top-level Operations / Metrics sections (mirrors upstream
// fabric-x-ansible-collection sections/operations.yaml.j2 + metrics.yaml.j2).
func TestOrdererTemplatesEmitOperationsAndMetrics(t *testing.T) {
	engine := NewEngine(&config.NetworkConfig{ChannelID: "arma"}, t.TempDir(), false)

	for _, ordererType := range []string{"router", "batcher", "consensus", "assembler"} {
		data := &OrdererTemplateData{
			PartyID:                 1,
			OrdererType:             ordererType,
			ShardID:                 1,
			ConfigDir:               "/config",
			ListenAddress:           "0.0.0.0",
			ListenPort:              7052,
			MonitoringListenAddress: "0.0.0.0",
			MonitoringListenPort:    7062,
			MSPID:                   "OrdererOrg1MSP",
			ChannelID:               "arma",
			TLS: TLSConfig{
				PrivateKey:  "/config/tls/server.key",
				Certificate: "/config/tls/server.crt",
				RootCAs:     []string{"/config/tls/ca.crt"},
			},
		}
		tmpl, err := engine.getOrdererTemplate(ordererType)
		if err != nil {
			t.Fatalf("getOrdererTemplate(%s): %v", ordererType, err)
		}
		var out bytes.Buffer
		if err := tmpl.Execute(&out, data); err != nil {
			t.Fatalf("execute %s template: %v", ordererType, err)
		}
		rendered := out.String()

		for _, want := range []string{
			"Operations:",
			"ListenPort: 7062",
			"Provider: prometheus",
			"MetricsLogInterval: 0s",
		} {
			if !strings.Contains(rendered, want) {
				t.Fatalf("%s config missing %q:\n%s", ordererType, want, rendered)
			}
		}
		// Pre-v1.0.0 keys must be gone from the General section.
		for _, banned := range []string{
			"MonitoringListenAddress:",
			"MonitoringListenPort:",
		} {
			if strings.Contains(rendered, banned) {
				t.Fatalf("%s config still contains pre-v1.0.0 key %q:\n%s", ordererType, banned, rendered)
			}
		}
	}
}

// TestOrdererTemplatesSkipOperationsWhenMonitoringDisabled verifies the
// MonitoringListenPort gate: with no monitoring port, neither Operations nor
// Metrics is rendered and the binary falls back to its built-in defaults.
func TestOrdererTemplatesSkipOperationsWhenMonitoringDisabled(t *testing.T) {
	engine := NewEngine(&config.NetworkConfig{ChannelID: "arma"}, t.TempDir(), false)

	data := &OrdererTemplateData{
		PartyID:       1,
		OrdererType:   "router",
		ConfigDir:     "/config",
		ListenAddress: "0.0.0.0",
		ListenPort:    7052,
		MSPID:         "OrdererOrg1MSP",
		ChannelID:     "arma",
	}
	tmpl, err := engine.getOrdererTemplate("router")
	if err != nil {
		t.Fatalf("getOrdererTemplate(router): %v", err)
	}
	var out bytes.Buffer
	if err := tmpl.Execute(&out, data); err != nil {
		t.Fatalf("execute router template: %v", err)
	}
	rendered := out.String()

	for _, banned := range []string{"Operations:", "Metrics:"} {
		if strings.Contains(rendered, banned) {
			t.Fatalf("router config should omit %q when monitoring is disabled:\n%s", banned, rendered)
		}
	}
}
