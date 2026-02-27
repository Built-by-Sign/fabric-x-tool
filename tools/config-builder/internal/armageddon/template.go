package armageddon

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"config-builder/internal/config"
)

// getDefaultHost returns the default host based on the operating system
// This matches Ansible's ansible_host default behavior:
// - macOS: host.docker.internal
// - Linux/other: localhost
func getDefaultHost() string {
	if runtime.GOOS == "darwin" {
		return "host.docker.internal"
	}
	return "localhost"
}

// sharedConfigTemplate is the template for generating shared_config.yaml
const sharedConfigTemplate = `#
# Copyright IBM Corp. All Rights Reserved.
#
# SPDX-License-Identifier: Apache-2.0
#

Parties:{{range .Parties}}
  - PartyID: {{.PartyID}}
    CACerts:{{range .CACerts}}
      - {{.}}{{end}}
    TLSCACerts:{{range .TLSCACerts}}
      - {{.}}{{end}}
    RouterConfig:
      TLSCert: {{.RouterConfig.TLSCert}}
      Host: {{.RouterConfig.Host}}
      Port: {{.RouterConfig.Port}}
    BatchersConfig:{{range .BatchersConfig}}
      - ShardID: {{.ShardID}}
        SignCert: {{.SignCert}}
        TLSCert: {{.TLSCert}}
        Host: {{.Host}}
        Port: {{.Port}}{{end}}
    ConsenterConfig:
      SignCert: {{.ConsenterConfig.SignCert}}
      TLSCert: {{.ConsenterConfig.TLSCert}}
      Host: {{.ConsenterConfig.Host}}
      Port: {{.ConsenterConfig.Port}}
    AssemblerConfig:
      TLSCert: {{.AssemblerConfig.TLSCert}}
      Host: {{.AssemblerConfig.Host}}
      Port: {{.AssemblerConfig.Port}}{{end}}
Consensus:
    SmartBFT:
        selfid: 0
        requestbatchmaxcount: 100
        requestbatchmaxbytes: 10485760
        requestbatchmaxinterval: 500ms
        incomingmessagebuffersize: 200
        requestpoolsize: 400
        requestforwardtimeout: 10s
        requestcomplaintimeout: 20s
        requestautoremovetimeout: 3m0s
        viewchangeresendinterval: 5s
        viewchangetimeout: 20s
        leaderheartbeattimeout: 1m0s
        leaderheartbeatcount: 10
        numofticksbehindbeforesyncing: 10
        collecttimeout: 1s
        synconstart: false
        speedupviewchange: false
        leaderrotation: false
        decisionsperleader: 0
        requestmaxbytes: 10240
        requestpoolsubmittimeout: 5s
Batching:
    BatchTimeouts:
        BatchCreationTimeout: 500ms
        FirstStrikeThreshold: 10s
        SecondStrikeThreshold: 10s
        AutoRemoveTimeout: 10s
    BatchSize:
        MaxMessageCount: 10000
        AbsoluteMaxBytes: 10485760
    RequestMaxBytes: 1048576
`

// TemplateData holds data for shared_config.yaml template
type TemplateData struct {
	Parties []PartyData
}

// PartyData holds party configuration data
type PartyData struct {
	PartyID         int
	CACerts         []string
	TLSCACerts      []string
	RouterConfig    RouterConfig
	BatchersConfig  []BatcherConfig
	ConsenterConfig ConsenterConfig
	AssemblerConfig AssemblerConfig
}

// RouterConfig holds router configuration
type RouterConfig struct {
	TLSCert string
	Host    string
	Port    int
}

// BatcherConfig holds batcher configuration
type BatcherConfig struct {
	ShardID  int
	SignCert string
	TLSCert  string
	Host     string
	Port     int
}

// ConsenterConfig holds consenter configuration
type ConsenterConfig struct {
	SignCert string
	TLSCert  string
	Host     string
	Port     int
}

// AssemblerConfig holds assembler configuration
type AssemblerConfig struct {
	TLSCert string
	Host    string
	Port    int
}

// buildTemplateData builds data for the shared_config template
// Each orderer organization gets its own Party with a unique PartyID (1, 2, 3, ...)
func (g *Generator) buildTemplateData() *TemplateData {
	absOutputDir, _ := filepath.Abs(g.outputDir)
	cryptoArtifactsDir := filepath.Join(absOutputDir, "build", "config", "cryptogen-artifacts")

	data := &TemplateData{
		Parties: make([]PartyData, 0),
	}

	// Create a Party for each orderer organization
	for partyID, org := range g.config.OrdererOrgs {
		partyID := partyID + 1 // PartyID starts from 1

		// Build CA cert paths for this organization only
		caCertPath := filepath.Join(cryptoArtifactsDir, "crypto", "ordererOrganizations", org.Domain, "msp", "cacerts", fmt.Sprintf("ca.%s-cert.pem", org.Domain))
		if _, err := os.Stat(caCertPath); os.IsNotExist(err) {
			caCertPath = filepath.Join(cryptoArtifactsDir, "ordererOrganizations", org.Domain, "msp", "cacerts", fmt.Sprintf("ca.%s-cert.pem", org.Domain))
		}

		tlsCACertPath := filepath.Join(cryptoArtifactsDir, "crypto", "ordererOrganizations", org.Domain, "msp", "tlscacerts", fmt.Sprintf("tlsca.%s-cert.pem", org.Domain))
		if _, err := os.Stat(tlsCACertPath); os.IsNotExist(err) {
			tlsCACertPath = filepath.Join(cryptoArtifactsDir, "ordererOrganizations", org.Domain, "msp", "tlscacerts", fmt.Sprintf("tlsca.%s-cert.pem", org.Domain))
		}

		// Find nodes for this organization
		var routerNode *config.Node
		var batcherNode *config.Node
		var consenterNode *config.Node
		var assemblerNode *config.Node

		for i := range org.Orderers {
			node := &org.Orderers[i]
			switch node.Type {
			case "router":
				routerNode = node
			case "batcher":
				batcherNode = node
			case "consenter":
				consenterNode = node
			case "assembler":
				assemblerNode = node
			}
		}

		// Build party data
		party := PartyData{
			PartyID:    partyID,
			CACerts:    []string{caCertPath},
			TLSCACerts: []string{tlsCACertPath},
		}

		// Router config
		if routerNode != nil {
			routerFQDN := fmt.Sprintf("%s.%s", routerNode.Name, org.Domain)
			tlsCertPath := filepath.Join(cryptoArtifactsDir, "crypto", "ordererOrganizations", org.Domain, "orderers", routerFQDN, "tls", "server.crt")
			if _, err := os.Stat(tlsCertPath); os.IsNotExist(err) {
				tlsCertPath = filepath.Join(cryptoArtifactsDir, "ordererOrganizations", org.Domain, "orderers", routerFQDN, "tls", "server.crt")
			}

			// Use node.Host (corresponds to Ansible's ansible_host)
			// If empty, use OS-dependent default (macOS: host.docker.internal, Linux: localhost)
			host := routerNode.Host
			if host == "" {
				host = getDefaultHost()
			}
			port := routerNode.Port
			if port == 0 {
				port = 7050
			}

			party.RouterConfig = RouterConfig{
				TLSCert: tlsCertPath,
				Host:    host,
				Port:    port,
			}
		} else if consenterNode != nil {
			// Fallback to consenter if no router
			consenterFQDN := fmt.Sprintf("%s.%s", consenterNode.Name, org.Domain)
			tlsCertPath := filepath.Join(cryptoArtifactsDir, "crypto", "ordererOrganizations", org.Domain, "orderers", consenterFQDN, "tls", "server.crt")
			if _, err := os.Stat(tlsCertPath); os.IsNotExist(err) {
				tlsCertPath = filepath.Join(cryptoArtifactsDir, "ordererOrganizations", org.Domain, "orderers", consenterFQDN, "tls", "server.crt")
			}

			host := consenterNode.Host
			if host == "" {
				host = "localhost"
			}
			port := consenterNode.Port
			if port == 0 {
				port = 7052
			}

			party.RouterConfig = RouterConfig{
				TLSCert: tlsCertPath,
				Host:    host,
				Port:    port,
			}
		}

		// Batcher config
		if batcherNode != nil {
			batcherFQDN := fmt.Sprintf("%s.%s", batcherNode.Name, org.Domain)

			signCertPath := filepath.Join(cryptoArtifactsDir, "crypto", "ordererOrganizations", org.Domain, "orderers", batcherFQDN, "msp", "signcerts", fmt.Sprintf("%s-cert.pem", batcherFQDN))
			if _, err := os.Stat(signCertPath); os.IsNotExist(err) {
				signCertPath = filepath.Join(cryptoArtifactsDir, "ordererOrganizations", org.Domain, "orderers", batcherFQDN, "msp", "signcerts", fmt.Sprintf("%s-cert.pem", batcherFQDN))
			}

			tlsCertPath := filepath.Join(cryptoArtifactsDir, "crypto", "ordererOrganizations", org.Domain, "orderers", batcherFQDN, "tls", "server.crt")
			if _, err := os.Stat(tlsCertPath); os.IsNotExist(err) {
				tlsCertPath = filepath.Join(cryptoArtifactsDir, "ordererOrganizations", org.Domain, "orderers", batcherFQDN, "tls", "server.crt")
			}

			// Use node.Host (corresponds to Ansible's ansible_host)
			// If empty, use OS-dependent default (macOS: host.docker.internal, Linux: localhost)
			host := batcherNode.Host
			if host == "" {
				host = getDefaultHost()
			}
			port := batcherNode.Port
			if port == 0 {
				port = 7051
			}

			party.BatchersConfig = []BatcherConfig{
				{
					ShardID:  batcherNode.ShardID,
					SignCert: signCertPath,
					TLSCert:  tlsCertPath,
					Host:     host,
					Port:     port,
				},
			}
		} else if consenterNode != nil {
			// Fallback to consenter if no batcher
			consenterFQDN := fmt.Sprintf("%s.%s", consenterNode.Name, org.Domain)

			signCertPath := filepath.Join(cryptoArtifactsDir, "crypto", "ordererOrganizations", org.Domain, "orderers", consenterFQDN, "msp", "signcerts", fmt.Sprintf("%s-cert.pem", consenterFQDN))
			if _, err := os.Stat(signCertPath); os.IsNotExist(err) {
				signCertPath = filepath.Join(cryptoArtifactsDir, "ordererOrganizations", org.Domain, "orderers", consenterFQDN, "msp", "signcerts", fmt.Sprintf("%s-cert.pem", consenterFQDN))
			}

			tlsCertPath := filepath.Join(cryptoArtifactsDir, "crypto", "ordererOrganizations", org.Domain, "orderers", consenterFQDN, "tls", "server.crt")
			if _, err := os.Stat(tlsCertPath); os.IsNotExist(err) {
				tlsCertPath = filepath.Join(cryptoArtifactsDir, "ordererOrganizations", org.Domain, "orderers", consenterFQDN, "tls", "server.crt")
			}

			host := consenterNode.Host
			if host == "" {
				host = "localhost"
			}
			port := consenterNode.Port
			if port == 0 {
				port = 7052
			}

			party.BatchersConfig = []BatcherConfig{
				{
					ShardID:  1, // Default shard ID
					SignCert: signCertPath,
					TLSCert:  tlsCertPath,
					Host:     host,
					Port:     port,
				},
			}
		}

		// Consenter config
		if consenterNode != nil {
			consenterFQDN := fmt.Sprintf("%s.%s", consenterNode.Name, org.Domain)

			signCertPath := filepath.Join(cryptoArtifactsDir, "crypto", "ordererOrganizations", org.Domain, "orderers", consenterFQDN, "msp", "signcerts", fmt.Sprintf("%s-cert.pem", consenterFQDN))
			if _, err := os.Stat(signCertPath); os.IsNotExist(err) {
				signCertPath = filepath.Join(cryptoArtifactsDir, "ordererOrganizations", org.Domain, "orderers", consenterFQDN, "msp", "signcerts", fmt.Sprintf("%s-cert.pem", consenterFQDN))
			}

			tlsCertPath := filepath.Join(cryptoArtifactsDir, "crypto", "ordererOrganizations", org.Domain, "orderers", consenterFQDN, "tls", "server.crt")
			if _, err := os.Stat(tlsCertPath); os.IsNotExist(err) {
				tlsCertPath = filepath.Join(cryptoArtifactsDir, "ordererOrganizations", org.Domain, "orderers", consenterFQDN, "tls", "server.crt")
			}

			// Use node.Host (corresponds to Ansible's ansible_host)
			// If empty, use OS-dependent default (macOS: host.docker.internal, Linux: localhost)
			host := consenterNode.Host
			if host == "" {
				host = getDefaultHost()
			}
			port := consenterNode.Port
			if port == 0 {
				port = 7052
			}

			party.ConsenterConfig = ConsenterConfig{
				SignCert: signCertPath,
				TLSCert:  tlsCertPath,
				Host:     host,
				Port:     port,
			}
		}

		// Assembler config
		if assemblerNode != nil {
			assemblerFQDN := fmt.Sprintf("%s.%s", assemblerNode.Name, org.Domain)

			tlsCertPath := filepath.Join(cryptoArtifactsDir, "crypto", "ordererOrganizations", org.Domain, "orderers", assemblerFQDN, "tls", "server.crt")
			if _, err := os.Stat(tlsCertPath); os.IsNotExist(err) {
				tlsCertPath = filepath.Join(cryptoArtifactsDir, "ordererOrganizations", org.Domain, "orderers", assemblerFQDN, "tls", "server.crt")
			}

			// Use node.Host (corresponds to Ansible's ansible_host)
			// If empty, use OS-dependent default (macOS: host.docker.internal, Linux: localhost)
			host := assemblerNode.Host
			if host == "" {
				host = getDefaultHost()
			}
			port := assemblerNode.Port
			if port == 0 {
				port = 7053
			}

			party.AssemblerConfig = AssemblerConfig{
				TLSCert: tlsCertPath,
				Host:    host,
				Port:    port,
			}
		} else if consenterNode != nil {
			// Fallback to consenter if no assembler
			consenterFQDN := fmt.Sprintf("%s.%s", consenterNode.Name, org.Domain)

			tlsCertPath := filepath.Join(cryptoArtifactsDir, "crypto", "ordererOrganizations", org.Domain, "orderers", consenterFQDN, "tls", "server.crt")
			if _, err := os.Stat(tlsCertPath); os.IsNotExist(err) {
				tlsCertPath = filepath.Join(cryptoArtifactsDir, "ordererOrganizations", org.Domain, "orderers", consenterFQDN, "tls", "server.crt")
			}

			host := consenterNode.Host
			if host == "" {
				host = "localhost"
			}
			port := consenterNode.Port
			if port == 0 {
				port = 7052
			}

			party.AssemblerConfig = AssemblerConfig{
				TLSCert: tlsCertPath,
				Host:    host,
				Port:    port,
			}
		}

		data.Parties = append(data.Parties, party)
	}

	return data
}
