package genesis

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/template"

	"config-builder/internal/config"
)

// configtxTemplate is the template for generating configtx.yaml
// It uses YAML anchors and merge keys like Ansible does
const configtxTemplate = `#
################################################################################
#
#   ORGANIZATIONS
#
################################################################################
---
Organizations:{{range .OrdererOrgs}}
  - &{{.MSPID}}MSP
    Name: {{.Name}}
    SkipAsForeign: false
    ID: {{.MSPID}}MSP
    MSPDir: {{.MSPDir}}
    Policies: &{{.MSPID}}Policies
      Readers:
        Type: Signature
        Rule: OR('{{.MSPID}}MSP.member')
      Writers:
        Type: Signature
        Rule: OR('{{.MSPID}}MSP.member')
      Admins:
        Type: Signature
        Rule: OR('{{.MSPID}}MSP.admin'){{if .OrdererEndpoints}}
    OrdererEndpoints:{{range .OrdererEndpoints}}
      - {{.}}{{end}}{{end}}{{end}}{{range .PeerOrgs}}
  - &{{.MSPID}}MSP
    Name: {{.Name}}
    ID: {{.MSPID}}MSP
    MSPDir: {{.MSPDir}}
    Policies: &{{.MSPID}}Policies
      Readers:
        Type: Signature
        Rule: OR('{{.MSPID}}MSP.member')
      Writers:
        Type: Signature
        Rule: OR('{{.MSPID}}MSP.member')
      Admins:
        Type: Signature
        Rule: OR('{{.MSPID}}MSP.admin'){{end}}

################################################################################
#
#   CAPABILITIES
#
################################################################################
Capabilities:
  Channel: &ChannelCapabilities
    V2_0: true
  Orderer: &OrdererCapabilities
    V2_0: true
  Application: &ApplicationCapabilities
    V2_0: true

################################################################################
#
#   APPLICATION
#
################################################################################
Application: &ApplicationDefaults
  Organizations:{{range .ApplicationOrgs}}
    - *{{.}}MSP{{end}}
  Policies:
    Readers:
      Type: ImplicitMeta
      Rule: ANY Readers
    Writers:
      Type: ImplicitMeta
      Rule: ANY Writers
    Admins:
      Type: ImplicitMeta
      Rule: ANY Admins
    Endorsement:
      Type: ImplicitMeta
      Rule: ANY Endorsement
    LifecycleEndorsement:{{if .HasPeerOrgs}}
      Type: Signature
      Rule: OR({{.LifecycleEndorsementRule}}){{else}}
      Type: ImplicitMeta
      Rule: ANY Admins{{end}}
  Capabilities:
    <<: *ApplicationCapabilities
  ACLs:
    _lifecycle/CheckCommitReadiness: /Channel/Application/Writers
    _lifecycle/CommitChaincodeDefinition: /Channel/Application/Writers
    _lifecycle/QueryChaincodeDefinition: /Channel/Application/Writers
    _lifecycle/QueryChaincodeDefinitions: /Channel/Application/Writers
    lscc/ChaincodeExists: /Channel/Application/Readers
    lscc/GetDeploymentSpec: /Channel/Application/Readers
    lscc/GetChaincodeData: /Channel/Application/Readers
    lscc/GetInstantiatedChaincodes: /Channel/Application/Readers
    qscc/GetChainInfo: /Channel/Application/Readers
    qscc/GetBlockByNumber: /Channel/Application/Readers
    qscc/GetBlockByHash: /Channel/Application/Readers
    qscc/GetTransactionByID: /Channel/Application/Readers
    qscc/GetBlockByTxID: /Channel/Application/Readers
    cscc/GetConfigBlock: /Channel/Application/Readers
    cscc/GetChannelConfig: /Channel/Application/Readers
    peer/Propose: /Channel/Application/Writers
    peer/ChaincodeToChaincode: /Channel/Application/Writers
    event/Block: /Channel/Application/Readers
    event/FilteredBlock: /Channel/Application/Readers

################################################################################
#
#   ORDERER
#
################################################################################
Orderer: &OrdererDefaults
  OrdererType: arma
  BatchTimeout: 500ms
  BatchSize:
    MaxMessageCount: 3500
    AbsoluteMaxBytes: 16 MB
    PreferredMaxBytes: 4 MB
  MaxChannels: 0
  Capabilities:
    <<: *OrdererCapabilities
  Policies:
    Readers:
      Type: ImplicitMeta
      Rule: ANY Readers
    Writers:
      Type: ImplicitMeta
      Rule: ANY Writers
    Admins:
      Type: ImplicitMeta
      Rule: MAJORITY Admins
    BlockValidation:
      Type: ImplicitMeta
      Rule: ANY Writers
  Organizations:{{range .OrdererOrgRefs}}
    - *{{.}}MSP{{end}}
  ConsenterMapping:{{range .Consenters}}
    - ID: {{.ID}}
      Host: {{.Host}}
      Port: {{.Port}}
      MSPID: {{.MSPID}}MSP
      Identity: {{.Identity}}
      ClientTLSCert: {{.ClientTLSCert}}
      ServerTLSCert: {{.ServerTLSCert}}{{end}}

################################################################################
#
#   CHANNEL
#
################################################################################
Channel: &ChannelDefaults
  Policies:
    Readers:
      Type: ImplicitMeta
      Rule: ANY Readers
    Writers:
      Type: ImplicitMeta
      Rule: ANY Writers
    Admins:
      Type: ImplicitMeta
      Rule: MAJORITY Admins
  Capabilities:
    <<: *ChannelCapabilities

################################################################################
#
#   PROFILES
#
################################################################################
Profiles:
  OrgsChannel:
    <<: *ChannelDefaults
    Orderer:
      <<: *OrdererDefaults
      Arma:
        Path: {{.ArmaSharedConfigPath}}
    Consortium: SampleConsortium
    Application:
      <<: *ApplicationDefaults
`

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
	tmpl, err := template.New("configtx").Parse(configtxTemplate)
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
	for _, org := range g.config.OrdererOrgs {
		mspID := org.Name
		if strings.HasSuffix(org.Name, "MSP") {
			// Remove MSP suffix for anchor name
			mspID = strings.TrimSuffix(org.Name, "MSP")
		}

		// Check MSP directory path
		mspDir := filepath.Join(cryptoArtifactsDir, "crypto", "ordererOrganizations", org.Domain, "msp")
		if _, err := os.Stat(mspDir); os.IsNotExist(err) {
			mspDir = filepath.Join(cryptoArtifactsDir, "ordererOrganizations", org.Domain, "msp")
		}

		orgData := OrgTemplateData{
			Name:             org.Name,
			MSPID:            mspID,
			MSPDir:           mspDir,
			OrdererEndpoints: g.getOrdererEndpoints(&org),
		}
		data.OrdererOrgs = append(data.OrdererOrgs, orgData)
		data.OrdererOrgRefs = append(data.OrdererOrgRefs, mspID)

		// Add consenters for ConsenterMapping (only consenter type orderers)
		for _, orderer := range org.Orderers {
			if orderer.Type == "consenter" {
				consenter := g.buildConsenterData(&org, &orderer, mspID, cryptoArtifactsDir)
				data.Consenters = append(data.Consenters, consenter)
			}
		}
	}

	// Build peer organizations
	for _, org := range g.config.PeerOrgs {
		mspID := org.Name
		if strings.HasSuffix(org.Name, "MSP") {
			mspID = strings.TrimSuffix(org.Name, "MSP")
		}

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
func (g *Generator) buildConsenterData(org *config.OrdererOrg, orderer *config.Node, mspID, cryptoArtifactsDir string) ConsenterData {
	host := orderer.Host
	if host == "" {
		host = "localhost"
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
		ID:            1, // Default group ID, can be configured later
		Host:          host,
		Port:          port,
		MSPID:         mspID,
		Identity:      identity,
		ClientTLSCert: tlsCert,
		ServerTLSCert: tlsCert,
	}
}
