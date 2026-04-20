package genesis

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"config-builder/internal/config"
	templatefiles "config-builder/templates"
)

// TemplateData holds data for configtx.yaml template
type TemplateData struct {
	OrdererOrgs              []OrgTemplateData
	PeerOrgs                 []OrgTemplateData
	ApplicationOrgs          []string
	OrdererOrgRefs           []string
	Consenters               []ConsenterData
	LifecycleEndorsementRule string
	HasPeerOrgs              bool
	ArmaSharedConfigPath     string
}

// ConsenterData holds consenter information for ConsenterMapping
type ConsenterData struct {
	ID            int
	Host          string
	Port          int
	MSPID         string
	Identity      string
	ClientTLSCert string
	ServerTLSCert string
}

// OrgTemplateData holds organization data for template
type OrgTemplateData struct {
	Name             string
	MSPID            string
	MSPDir           string
	OrdererEndpoints []string
}

// generateConfigtxFromTemplate generates configtx.yaml using text template
func (g *Generator) generateConfigtxFromTemplate() (string, error) {
	absOutputDir, _ := filepath.Abs(g.outputDir)
	configDir := filepath.Join(absOutputDir, "build", "config", "configtxgen-artifacts")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create config directory: %w", err)
	}

	configPath := filepath.Join(configDir, "configtx.yaml")

	// Build template data
	data := g.buildTemplateData()

	// Parse and execute template
	tmpl, err := templatefiles.Parse("genesis/configtx.yaml.tmpl", nil)
	if err != nil {
		return "", fmt.Errorf("failed to parse template: %w", err)
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("failed to execute template: %w", err)
	}

	// Write to file
	if err := os.WriteFile(configPath, buf.Bytes(), 0644); err != nil {
		return "", fmt.Errorf("failed to write configtx: %w", err)
	}

	g.log("Generated configtx.yaml at: %s", configPath)
	return configPath, nil
}

// buildTemplateData builds data for the configtx template
func (g *Generator) buildTemplateData() *TemplateData {
	absOutputDir, _ := filepath.Abs(g.outputDir)
	cryptoArtifactsDir := filepath.Join(absOutputDir, "build", "config", "cryptogen-artifacts")
	armaPath := filepath.Join(absOutputDir, "build", "config", "armageddon-artifacts", "shared_config.binpb")

	data := &TemplateData{
		OrdererOrgs:          make([]OrgTemplateData, 0),
		PeerOrgs:             make([]OrgTemplateData, 0),
		ApplicationOrgs:      make([]string, 0),
		OrdererOrgRefs:       make([]string, 0),
		Consenters:           make([]ConsenterData, 0),
		ArmaSharedConfigPath: armaPath,
		HasPeerOrgs:          len(g.config.PeerOrgs) > 0,
	}

	// Build orderer organizations
	for orgIndex, org := range g.config.OrdererOrgs {
		mspID := config.DeriveMSPIDBase(org.Name)
		partyID := orgIndex + 1

		// Check MSP directory path
		mspDir := filepath.Join(cryptoArtifactsDir, "crypto", "ordererOrganizations", org.Domain, "msp")
		if _, err := os.Stat(mspDir); os.IsNotExist(err) {
			mspDir = filepath.Join(cryptoArtifactsDir, "ordererOrganizations", org.Domain, "msp")
		}

		orgData := OrgTemplateData{
			Name:             org.Name,
			MSPID:            mspID,
			MSPDir:           mspDir,
			OrdererEndpoints: g.getOrdererEndpoints(&org, partyID),
		}
		data.OrdererOrgs = append(data.OrdererOrgs, orgData)
		data.OrdererOrgRefs = append(data.OrdererOrgRefs, mspID)

		// Add consenters for ConsenterMapping (only consenter type orderers)
		for _, orderer := range org.Orderers {
			if orderer.Type == "consenter" {
				consenter := g.buildConsenterData(&org, &orderer, mspID, partyID, cryptoArtifactsDir)
				data.Consenters = append(data.Consenters, consenter)
			}
		}
	}

	// Build peer organizations
	for _, org := range g.config.PeerOrgs {
		mspID := config.DeriveMSPIDBase(org.Name)

		// Check MSP directory path
		mspDir := filepath.Join(cryptoArtifactsDir, "crypto", "peerOrganizations", org.Domain, "msp")
		if _, err := os.Stat(mspDir); os.IsNotExist(err) {
			mspDir = filepath.Join(cryptoArtifactsDir, "peerOrganizations", org.Domain, "msp")
		}

		orgData := OrgTemplateData{
			Name:   org.Name,
			MSPID:  mspID,
			MSPDir: mspDir,
		}
		data.PeerOrgs = append(data.PeerOrgs, orgData)
		data.ApplicationOrgs = append(data.ApplicationOrgs, mspID)
	}

	// Build lifecycle endorsement rule
	rules := make([]string, 0, len(data.ApplicationOrgs))
	for _, mspID := range data.ApplicationOrgs {
		rules = append(rules, fmt.Sprintf("'%sMSP.member'", mspID))
	}
	if len(rules) == 0 {
		data.LifecycleEndorsementRule = "'SampleOrgMSP.member'"
	} else {
		data.LifecycleEndorsementRule = strings.Join(rules, ", ")
	}

	return data
}

// buildConsenterData builds consenter data for ConsenterMapping
func (g *Generator) buildConsenterData(org *config.OrdererOrg, orderer *config.Node, mspID string, partyID int, cryptoArtifactsDir string) ConsenterData {
	host := orderer.Host
	if host == "" {
		host = getDefaultHost()
	}
	port := orderer.Port
	if port == 0 {
		port = 7050
	}

	// Build orderer FQDN
	ordererFQDN := fmt.Sprintf("%s.%s", orderer.Name, org.Domain)

	// Build certificate paths
	// Check if crypto subdirectory exists
	basePath := filepath.Join(cryptoArtifactsDir, "crypto", "ordererOrganizations", org.Domain, "orderers", ordererFQDN)
	if _, err := os.Stat(basePath); os.IsNotExist(err) {
		basePath = filepath.Join(cryptoArtifactsDir, "ordererOrganizations", org.Domain, "orderers", ordererFQDN)
	}

	identity := filepath.Join(basePath, "msp", "signcerts", fmt.Sprintf("%s-cert.pem", ordererFQDN))
	tlsCert := filepath.Join(basePath, "tls", "server.crt")

	return ConsenterData{
		ID:            partyID,
		Host:          host,
		Port:          port,
		MSPID:         mspID,
		Identity:      identity,
		ClientTLSCert: tlsCert,
		ServerTLSCert: tlsCert,
	}
}
