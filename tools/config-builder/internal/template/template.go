package template

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"text/template"

	"config-builder/internal/bccsp"
	"config-builder/internal/config"
	"config-builder/internal/perms"
)

// FabricCARootPEMFilename is the filename under each committer component's
// /config/tls/ directory where the remote Fabric CA's TLS root cert is
// expected. Referenced by the generated committer config's CACertPaths and
// by the cbdc-network Makefile fetch-fabric-ca-root target that drops the
// file in place. Keep the two in sync if renamed.
const FabricCARootPEMFilename = "fabric-ca-root.pem"

// Engine handles template-based configuration file generation
type Engine struct {
	config    *config.NetworkConfig
	outputDir string
	verbose   bool
}

// NewEngine creates a new template engine
func NewEngine(cfg *config.NetworkConfig, outputDir string, verbose bool) *Engine {
	return &Engine{
		config:    cfg,
		outputDir: outputDir,
		verbose:   verbose,
	}
}

// GenerateNodeConfigs generates configuration files for all nodes
func (e *Engine) GenerateNodeConfigs() error {
	// Generate orderer node configs
	if err := e.generateOrdererConfigs(); err != nil {
		return fmt.Errorf("failed to generate orderer configs: %w", err)
	}

	// Generate committer node configs (if configured)
	if e.config.Committer != nil {
		if err := e.generateCommitterConfigs(); err != nil {
			return fmt.Errorf("failed to generate committer configs: %w", err)
		}
	}

	return nil
}

// generateOrdererConfigs generates configuration files for all orderer nodes
// Following Ansible structure: orderer-{type}-{index}/config/
func (e *Engine) generateOrdererConfigs() error {
	absOutputDir, _ := filepath.Abs(e.outputDir)
	cryptoArtifactsDir := filepath.Join(absOutputDir, "build", "config", "cryptogen-artifacts")
	configtxgenArtifactsDir := filepath.Join(absOutputDir, "build", "config", "configtxgen-artifacts")

	// Track component indices per type
	componentIndices := make(map[string]int) // type -> index

	// Determine PartyID for each organization (like Ansible's orderer_group)
	// Each org gets a unique PartyID starting from 1
	orgPartyIDs := make(map[string]int)
	for i, org := range e.config.OrdererOrgs {
		orgPartyIDs[org.Name] = i + 1 // PartyID starts from 1
	}

	for _, org := range e.config.OrdererOrgs {
		partyID := orgPartyIDs[org.Name] // Get PartyID for this org

		for _, orderer := range org.Orderers {
			// Determine orderer FQDN (used for crypto material lookup)
			ordererFQDN := fmt.Sprintf("%s.%s", orderer.Name, org.Domain)

			// Determine config directory paths
			ordererCryptoDir := filepath.Join(cryptoArtifactsDir, "crypto", "ordererOrganizations", org.Domain, "orderers", ordererFQDN)
			if _, err := os.Stat(ordererCryptoDir); os.IsNotExist(err) {
				ordererCryptoDir = filepath.Join(cryptoArtifactsDir, "ordererOrganizations", org.Domain, "orderers", ordererFQDN)
			}

			// Get or increment component index
			componentType := orderer.Type
			if componentIndices[componentType] == 0 {
				componentIndices[componentType] = 1
			} else {
				componentIndices[componentType]++
			}
			componentIndex := componentIndices[componentType]

			// Create node config directory following Ansible structure: orderer-{type}-{index}/config/
				componentDirName := fmt.Sprintf("orderer-%s-%d", componentType, componentIndex)
			componentDir := filepath.Join(absOutputDir, "local-deployment", componentDirName)
			nodeConfigDir := filepath.Join(componentDir, "config")
			if err := os.MkdirAll(nodeConfigDir, perms.Dir); err != nil {
				return fmt.Errorf("failed to create orderer config directory: %w", err)
			}

			// Generate node config based on type (pass partyID)
			configPath := filepath.Join(nodeConfigDir, "node_config.yaml")
			if err := e.generateOrdererConfig(componentType, &org, &orderer, configPath, nodeConfigDir, ordererCryptoDir, configtxgenArtifactsDir, partyID); err != nil {
				return fmt.Errorf("failed to generate config for %s: %w", componentDirName, err)
			}

			// Copy genesis block
			genesisBlockSrc := filepath.Join(configtxgenArtifactsDir, e.config.ChannelID+"_block.pb")
			genesisBlockDst := filepath.Join(nodeConfigDir, "genesis.block")
			if err := e.copyFile(genesisBlockSrc, genesisBlockDst); err != nil {
				return fmt.Errorf("failed to copy genesis block: %w", err)
			}

			// Copy crypto materials (symlink or copy)
			if err := e.copyCryptoMaterials(ordererCryptoDir, nodeConfigDir, &org); err != nil {
				return fmt.Errorf("failed to copy crypto materials: %w", err)
			}

			// Note: Store directory is NOT created here (matching Ansible behavior)
			// Ansible does not create the store directory during setup
			// The orderer container will create it automatically at startup based on
			// the FileStore: Location: /config/store configuration

			e.log("Generated config for orderer: %s (%s)", componentDirName, componentType)
		}
	}

	return nil
}

// generateCommitterConfigs generates configuration files for committer components
// Following Ansible structure: committer-{type}/config/
func (e *Engine) generateCommitterConfigs() error {
	absOutputDir, _ := filepath.Abs(e.outputDir)

	for _, component := range e.config.Committer.Components {
		// Skip config file generation for db type. The official Postgres role
		// does not render a standalone committer config, but it does transfer
		// TLS material under config/tls when postgres_use_tls is enabled.
		if component.Type == "db" {
			componentDirName := e.config.CommitterComponentDirName(component)
			componentDir := filepath.Join(absOutputDir, "local-deployment", componentDirName)
			dataDir := filepath.Join(componentDir, "data")
			if err := os.MkdirAll(dataDir, perms.Dir); err != nil {
				return fmt.Errorf("failed to create committer db data directory: %w", err)
			}
			if e.getTLSEnabled() {
				componentConfigDir := filepath.Join(componentDir, "config")
				if err := os.MkdirAll(componentConfigDir, perms.Dir); err != nil {
					return fmt.Errorf("failed to create committer db config directory: %w", err)
				}
				if err := e.copyCommitterTLS(&component, componentConfigDir); err != nil {
					return fmt.Errorf("failed to copy committer db TLS for %s: %w", componentDirName, err)
				}
			}
			e.log("Created data directory for committer: %s (%s)", componentDirName, component.Type)
			continue
		}

		// Create component config directory following Ansible structure: committer-{type}/config/
		componentDirName := e.config.CommitterComponentDirName(component)
		componentDir := filepath.Join(absOutputDir, "local-deployment", componentDirName)
		componentConfigDir := filepath.Join(componentDir, "config")
		if err := os.MkdirAll(componentConfigDir, perms.Dir); err != nil {
			return fmt.Errorf("failed to create committer config directory: %w", err)
		}

		// Generate component config based on type
		// Ansible uses config-{type}.yml naming
		configFileName := fmt.Sprintf("config-%s.yml", component.Type)
		configPath := filepath.Join(componentConfigDir, configFileName)
		if err := e.generateCommitterConfig(component.Type, &component, configPath, componentConfigDir); err != nil {
			return fmt.Errorf("failed to generate config for %s: %w", componentDirName, err)
		}
		if component.Type == "validator" || component.Type == "query-service" {
			if err := e.copyCommitterDatabaseTLS(componentConfigDir); err != nil {
				return fmt.Errorf("failed to copy committer database TLS for %s: %w", componentDirName, err)
			}
		}
		if e.getTLSEnabled() {
			if err := e.copyCommitterTLS(&component, componentConfigDir); err != nil {
				return fmt.Errorf("failed to copy committer TLS for %s: %w", componentDirName, err)
			}
		}
		if component.Type == "coordinator" && e.getTLSEnabled() {
			if err := e.copyCoordinatorClientTLS(componentConfigDir); err != nil {
				return fmt.Errorf("failed to copy coordinator client TLS material: %w", err)
			}
		}

		// Copy genesis block for sidecar (required for bootstrap)
		if component.Type == "sidecar" {
			configtxgenArtifactsDir := filepath.Join(absOutputDir, "build", "config", "configtxgen-artifacts")
			genesisBlockSrc := filepath.Join(configtxgenArtifactsDir, e.config.ChannelID+"_block.pb")
			genesisBlockDst := filepath.Join(componentConfigDir, "genesis.block")
			if err := e.copyFile(genesisBlockSrc, genesisBlockDst); err != nil {
				return fmt.Errorf("failed to copy genesis block for sidecar: %w", err)
			}
			e.log("Copied genesis block for sidecar: %s", genesisBlockDst)

			if err := e.copySidecarMSP(&component, componentConfigDir); err != nil {
				return fmt.Errorf("failed to copy sidecar MSP for %s: %w", componentDirName, err)
			}
			if e.getTLSEnabled() {
				if err := e.copySidecarClientTLS(componentConfigDir); err != nil {
					return fmt.Errorf("failed to copy sidecar client TLS material: %w", err)
				}
			}
		}

		e.log("Generated config for committer: %s (%s)", componentDirName, component.Type)
	}

	return nil
}

// generateOrdererConfig generates a configuration file for an orderer node
func (e *Engine) generateOrdererConfig(ordererType string, org *config.OrdererOrg, node *config.Node, configPath, configDir, cryptoDir, genesisDir string, partyID int) error {
	data, err := e.buildOrdererTemplateData(ordererType, org, node, configDir, cryptoDir, genesisDir, partyID)
	if err != nil {
		return err
	}

	tmpl, err := e.getOrdererTemplate(ordererType)
	if err != nil {
		return err
	}

	return e.executeTemplate(tmpl, data, configPath)
}

// generateCommitterConfig generates a configuration file for a committer component
func (e *Engine) generateCommitterConfig(componentType string, component *config.CommitterNode, configPath, configDir string) error {
	data, err := e.buildCommitterTemplateData(componentType, component, configDir)
	if err != nil {
		return err
	}

	tmpl, err := e.getCommitterTemplate(componentType)
	if err != nil {
		return err
	}

	return e.executeTemplate(tmpl, data, configPath)
}

// buildOrdererTemplateData builds template data for orderer nodes
func (e *Engine) buildOrdererTemplateData(ordererType string, org *config.OrdererOrg, node *config.Node, configDir, cryptoDir, genesisDir string, partyID int) (*OrdererTemplateData, error) {
	// Use container path for configDir (matches Ansible's orderer_docker_config_dir = "/config")
	// The configDir parameter is the host path, but we need container paths in the config file
	containerConfigDir := "/config"

	bccsConfig, err := e.buildBCCSPConfig(org.KMSTokenLabel, func() (string, error) {
		pin, err := org.ResolveKMSUserPin()
		if err != nil {
			return "", err
		}
		if node.UserPin != "" {
			return node.UserPin, nil
		}
		return pin, nil
	})
	if err != nil {
		return nil, fmt.Errorf("orderer org %q: %w", org.Name, err)
	}

	listenPort := node.Port
	if listenPort == 0 {
		listenPort = config.DefaultOrdererPort(node.Type)
	}

	// Auto-calculate monitoring port if not specified
	monPort := node.MonitoringPort
	if monPort == 0 {
		monPort = config.DefaultMonitoringPort(listenPort)
	}

	data := &OrdererTemplateData{
		PartyID:                 partyID, // Use provided partyID (each org has different PartyID)
		OrdererType:             ordererType,
		ShardID:                 node.ShardID,
		ConfigDir:               containerConfigDir, // Container path, not host path
		CryptoDir:               cryptoDir,
		GenesisDir:              genesisDir,
		ListenAddress:           "0.0.0.0",
		ListenPort:              listenPort,
		MonitoringListenAddress: "0.0.0.0",
		MonitoringListenPort:    monPort,
		MSPID:                   org.Name,
		ChannelID:               e.config.ChannelID,
		BCCSP:                   bccsConfig, // Use generated BCCSP config
		TLS: TLSConfig{
			Enabled:            e.getTLSEnabled(),            // Use config value or default to false
			ClientAuthRequired: e.getTLSClientAuthRequired(), // Use config value or default to false
			PrivateKey:         filepath.Join(containerConfigDir, "tls", "server.key"),
			Certificate:        filepath.Join(containerConfigDir, "tls", "server.crt"),
			RootCAs:            []string{filepath.Join(containerConfigDir, "tls", "ca.crt")},
		},
	}

	return data, nil
}

// buildBCCSPConfig returns a BCCSP config honoring KMS.Enabled.
// When KMS is enabled, resolvePin is invoked to get the per-identity PIN;
// otherwise the software provider is returned.
func (e *Engine) buildBCCSPConfig(orgTokenLabel string, resolvePin func() (string, error)) (*bccsp.BCCSPConfig, error) {
	if e.config.KMS == nil || !e.config.KMS.Enabled {
		return bccsp.GenerateSoftwareConfig(), nil
	}
	pin, err := resolvePin()
	if err != nil {
		return nil, err
	}
	tokenLabel, err := e.config.ResolveKMSTokenLabel(orgTokenLabel)
	if err != nil {
		return nil, err
	}
	return bccsp.GenerateKMSConfig(e.config.KMS.Endpoint, tokenLabel, pin), nil
}

// monitoringPort returns the monitoring port for a committer component,
// falling back to service port + 10.
func monitoringPort(c *config.CommitterNode) int {
	if c.MonitoringPort != 0 {
		return c.MonitoringPort
	}
	return config.DefaultMonitoringPort(c.Port)
}

// buildCommitterTemplateData builds template data for committer components
func (e *Engine) buildCommitterTemplateData(componentType string, component *config.CommitterNode, configDir string) (*CommitterTemplateData, error) {
	// Use container path for configDir (matches Ansible's committer_docker_config_dir = "/config")
	containerConfigDir := "/config"

	// Default host for container-to-host communication on Mac Docker Desktop
	// This matches Ansible's ansible_host: "host.docker.internal"
	dockerHost := "host.docker.internal"

	data := &CommitterTemplateData{
		ComponentType:    componentType,
		ComponentName:    component.Name,
		ConfigDir:        containerConfigDir, // Container path, not host path
		Host:             component.Host,
		Port:             component.Port,
		MonitoringPort:   monitoringPort(component),
		ChannelID:        e.config.ChannelID,
		GenesisBlockPath: filepath.Join(containerConfigDir, "genesis.block"), // Container path
	}
	if componentType != "db" && e.getTLSEnabled() {
		data.ServerTLS = e.committerServerTLSConfig(containerConfigDir)
		data.MonitoringTLS = e.committerServerTLSConfig(containerConfigDir)
	}

	// Collect verifier and validator endpoints for coordinator
	if componentType == "coordinator" {
		for _, comp := range e.config.Committer.Components {
			if comp.Type == "verifier" {
				data.VerifierEndpoints = append(data.VerifierEndpoints, EndpointConfig{
					Host: dockerHost,
					Port: comp.Port,
				})
			} else if comp.Type == "validator" {
				data.ValidatorEndpoints = append(data.ValidatorEndpoints, EndpointConfig{
					Host: dockerHost,
					Port: comp.Port,
				})
			}
		}
		if e.getTLSEnabled() {
			data.VerifierTLS = e.committerClientTLSConfig(filepath.Join(containerConfigDir, "tls", "verifiers", "ca.crt"))
			data.ValidatorTLS = e.committerClientTLSConfig(filepath.Join(containerConfigDir, "tls", "validators", "ca.crt"))
		}
	}

	if componentType == "verifier" {
		data.VerifierParallelism = runtime.NumCPU() * 2
		if data.VerifierParallelism == 0 {
			data.VerifierParallelism = 4
		}
	}

	// Find coordinator for sidecar and collect assembler endpoints
	if componentType == "sidecar" {
		for _, comp := range e.config.Committer.Components {
			if comp.Type == "coordinator" {
				data.CommitterHost = dockerHost
				data.CommitterPort = comp.Port
				break
			}
		}
		if data.CommitterHost == "" {
			data.CommitterHost = dockerHost
			data.CommitterPort = 5300 // Default coordinator port
		}

		// Populate the sidecar's dedicated peer MSP identity, matching the
		// Ansible collection model: the sidecar represents a configured peer
		// org and signs deliver requests with its own MSP under /config/msp.
		org, _, err := e.config.ResolveSidecarIdentity(component.Name)
		if err != nil {
			return nil, err
		}
		data.SidecarIdentityMSPID = config.DeriveMSPID(org.Name)
		data.SidecarIdentityMSPDir = filepath.Join(containerConfigDir, "msp")

		bccspCfg, err := e.buildBCCSPConfig(org.KMSTokenLabel, org.ResolveKMSUserPin)
		if err != nil {
			return nil, fmt.Errorf("sidecar identity for peer org %q: %w", org.Name, err)
		}
		data.SidecarIdentityBCCSP = bccspCfg

		// v0.2.0 sidecar discovers orderer endpoints + TLS CAs from the config
		// block (orderer.latest-known-config-block-path → GenesisBlockPath).
		// Only the sidecar's own client TLS material + a CA fallback list is
		// still supplied here; orderer.tls.common-ca-cert-paths is kept as a
		// temporary workaround per upstream ordererdial.TLSConfig.
		if e.getTLSEnabled() {
			data.OrdererTLS = e.committerClientTLSConfig()
			for _, org := range e.config.OrdererOrgs {
				for _, orderer := range org.Orderers {
					if orderer.Type != "assembler" {
						continue
					}
					ordererTLSDir := filepath.Join(containerConfigDir, "tls", "orderers", org.Name, orderer.Name)
					data.OrdererTLS.CommonCACertPaths = append(data.OrdererTLS.CommonCACertPaths, filepath.Join(ordererTLSDir, "ca.crt"))
				}
			}
		}
		if e.getTLSEnabled() {
			data.CommitterTLS = e.committerClientTLSConfig(filepath.Join(containerConfigDir, "tls", "coordinator", "ca.crt"))
		}
	}

	// Add database config if applicable
	if componentType == "db" {
		// Database component itself
		data.Database = &DatabaseConfig{
			Type:      "postgres",
			Endpoints: []string{fmt.Sprintf("%s:%d", component.Host, component.Port)},
			User:      component.PostgresUser,
			Password:  component.PostgresPassword,
			DBName:    component.PostgresDB,
		}
	} else if componentType == "validator" || componentType == "query-service" {
		dbCfg, err := e.buildCommitterDatabaseConfig(dockerHost)
		if err != nil {
			return nil, err
		}
		data.Database = dbCfg
	}

	return data, nil
}

// Helper functions

func (e *Engine) committerServerTLSConfig(containerConfigDir string) *CommitterTLSConfig {
	tlsConfig := e.committerClientTLSConfig()
	if e.getTLSClientAuthRequired() {
		// Committer trusts two CAs for incoming mTLS:
		//   1. ca.crt — cryptogen's tlsCA (signs internal committer/orderer mesh certs)
		//   2. fabric-ca-root.pem — remote Fabric CA's TLS root (signs biz service
		//      TLS certs issued via `fabric-ca-client enroll --enrollment.profile=tls`)
		// fabric-ca-root.pem is dropped into each component's /config/tls/ by the
		// cbdc-network Makefile fetch-fabric-ca-root target after config-builder runs.
		tlsConfig.CACertPaths = []string{
			filepath.Join(containerConfigDir, "tls", "ca.crt"),
			filepath.Join(containerConfigDir, "tls", FabricCARootPEMFilename),
		}
	}
	return tlsConfig
}

func (e *Engine) buildCommitterDatabaseConfig(dockerHost string) (*DatabaseConfig, error) {
	if e.config.Committer == nil {
		return nil, nil
	}
	if e.config.Committer.Database != nil {
		db := e.config.Committer.Database
		dbType := e.config.ResolveCommitterDatabaseType()
		loadBalance := dbType == "yugabyte"
		if db.LoadBalance != nil {
			loadBalance = *db.LoadBalance
		}
		tablePreSplitTablets := db.TablePreSplitTablets
		if dbType == "yugabyte" && tablePreSplitTablets == 0 {
			tablePreSplitTablets = len(db.Endpoints)
		}
		cfg := &DatabaseConfig{
			Type:                 dbType,
			Endpoints:            make([]string, 0, len(db.Endpoints)),
			User:                 db.Username,
			Password:             db.Password,
			DBName:               db.Database,
			LoadBalance:          loadBalance,
			TablePreSplitTablets: tablePreSplitTablets,
		}
		for _, endpoint := range db.Endpoints {
			cfg.Endpoints = append(cfg.Endpoints, fmt.Sprintf("%s:%d", endpoint.Host, endpoint.Port))
		}
		if db.TLS != nil && db.TLS.Enabled {
			cfg.TLS = &DatabaseTLSConfig{
				Mode:       "tls",
				CACertPath: filepath.Join("/config", "tls", "db", "ca.crt"),
			}
		}
		return cfg, nil
	}
	if !e.config.Committer.UsePostgres {
		return nil, nil
	}
	for _, comp := range e.config.Committer.Components {
		if comp.Type == "db" {
			dbPort := comp.Port
			if dbPort == 0 {
				dbPort = 5432
			}
			cfg := &DatabaseConfig{
				Type:        "postgres",
				Endpoints:   []string{fmt.Sprintf("%s:%d", dockerHost, dbPort)},
				User:        comp.PostgresUser,
				Password:    comp.PostgresPassword,
				DBName:      comp.PostgresDB,
				LoadBalance: false,
			}
			if e.getTLSEnabled() {
				cfg.TLS = &DatabaseTLSConfig{
					Mode:       "tls",
					CACertPath: filepath.Join("/config", "tls", "db", "ca.crt"),
				}
			}
			return cfg, nil
		}
	}
	return nil, fmt.Errorf("committer.use_postgres is enabled but no db component was found")
}

func (e *Engine) committerClientTLSConfig(caCertPaths ...string) *CommitterTLSConfig {
	mode := "tls"
	if e.getTLSClientAuthRequired() {
		mode = "mtls"
	}
	return &CommitterTLSConfig{
		Mode:        mode,
		KeyPath:     filepath.Join("/config", "tls", "server.key"),
		CertPath:    filepath.Join("/config", "tls", "server.crt"),
		CACertPaths: caCertPaths,
	}
}

func (e *Engine) copyCommitterDatabaseTLS(componentConfigDir string) error {
	if e.config.Committer == nil {
		return nil
	}
	if e.config.Committer.Database != nil {
		if e.config.Committer.Database.TLS == nil || !e.config.Committer.Database.TLS.Enabled {
			return nil
		}
		return e.copyFile(
			e.config.Committer.Database.TLS.CACertPath,
			filepath.Join(componentConfigDir, "tls", "db", "ca.crt"),
		)
	}
	if !e.getTLSEnabled() {
		return nil
	}
	dbComponent := e.firstCommitterComponentByType("db")
	if dbComponent == nil {
		return nil
	}
	return e.copyCommitterCACert(dbComponent, filepath.Join(componentConfigDir, "tls", "db", "ca.crt"))
}

func (e *Engine) copyCommitterTLS(component *config.CommitterNode, componentConfigDir string) error {
	org, identityName, err := e.config.ResolveCommitterCryptoIdentity(component.Name, component.Type)
	if err != nil {
		return err
	}
	srcTLS := e.peerTLSDir(org, identityName)
	dstTLS := filepath.Join(componentConfigDir, "tls")
	if err := e.copyDir(srcTLS, dstTLS); err != nil {
		return fmt.Errorf("copy %s to %s: %w", srcTLS, dstTLS, err)
	}
	e.log("Copied committer TLS for %s: %s", component.Name, dstTLS)
	return nil
}

func (e *Engine) copyCoordinatorClientTLS(componentConfigDir string) error {
	if verifier := e.firstCommitterComponentByType("verifier"); verifier != nil {
		if err := e.copyCommitterCACert(verifier, filepath.Join(componentConfigDir, "tls", "verifiers", "ca.crt")); err != nil {
			return fmt.Errorf("verifier CA: %w", err)
		}
	}
	if validator := e.firstCommitterComponentByType("validator"); validator != nil {
		if err := e.copyCommitterCACert(validator, filepath.Join(componentConfigDir, "tls", "validators", "ca.crt")); err != nil {
			return fmt.Errorf("validator CA: %w", err)
		}
	}
	return nil
}

func (e *Engine) copySidecarClientTLS(componentConfigDir string) error {
	for _, org := range e.config.OrdererOrgs {
		for _, orderer := range org.Orderers {
			if orderer.Type != "assembler" {
				continue
			}
			srcTLS := e.ordererTLSDir(&org, &orderer)
			dstDir := filepath.Join(componentConfigDir, "tls", "orderers", org.Name, orderer.Name)
			if err := e.copyFile(filepath.Join(srcTLS, "ca.crt"), filepath.Join(dstDir, "ca.crt")); err != nil {
				return fmt.Errorf("orderer %s CA: %w", orderer.Name, err)
			}
			if err := e.copyFile(filepath.Join(srcTLS, "server.crt"), filepath.Join(dstDir, "server.crt")); err != nil {
				return fmt.Errorf("orderer %s server cert: %w", orderer.Name, err)
			}
		}
	}
	if coordinator := e.firstCommitterComponentByType("coordinator"); coordinator != nil {
		if err := e.copyCommitterCACert(coordinator, filepath.Join(componentConfigDir, "tls", "coordinator", "ca.crt")); err != nil {
			return fmt.Errorf("coordinator CA: %w", err)
		}
	}
	return nil
}

func (e *Engine) copyCommitterCACert(component *config.CommitterNode, dst string) error {
	org, identityName, err := e.config.ResolveCommitterCryptoIdentity(component.Name, component.Type)
	if err != nil {
		return err
	}
	return e.copyFile(filepath.Join(e.peerTLSDir(org, identityName), "ca.crt"), dst)
}

func (e *Engine) firstCommitterComponentByType(componentType string) *config.CommitterNode {
	if e.config.Committer == nil {
		return nil
	}
	for i := range e.config.Committer.Components {
		if e.config.Committer.Components[i].Type == componentType {
			return &e.config.Committer.Components[i]
		}
	}
	return nil
}

func (e *Engine) peerTLSDir(org *config.PeerOrg, identityName string) string {
	absOutputDir, _ := filepath.Abs(e.outputDir)
	cryptoArtifactsDir := filepath.Join(absOutputDir, "build", "config", "cryptogen-artifacts")
	identityFQDN := fmt.Sprintf("%s.%s", identityName, org.Domain)
	srcTLS := filepath.Join(cryptoArtifactsDir, "crypto", "peerOrganizations", org.Domain, "peers", identityFQDN, "tls")
	if _, err := os.Stat(srcTLS); os.IsNotExist(err) {
		srcTLS = filepath.Join(cryptoArtifactsDir, "peerOrganizations", org.Domain, "peers", identityFQDN, "tls")
	}
	return srcTLS
}

func (e *Engine) ordererTLSDir(org *config.OrdererOrg, orderer *config.Node) string {
	absOutputDir, _ := filepath.Abs(e.outputDir)
	cryptoArtifactsDir := filepath.Join(absOutputDir, "build", "config", "cryptogen-artifacts")
	ordererFQDN := fmt.Sprintf("%s.%s", orderer.Name, org.Domain)
	srcTLS := filepath.Join(cryptoArtifactsDir, "crypto", "ordererOrganizations", org.Domain, "orderers", ordererFQDN, "tls")
	if _, err := os.Stat(srcTLS); os.IsNotExist(err) {
		srcTLS = filepath.Join(cryptoArtifactsDir, "ordererOrganizations", org.Domain, "orderers", ordererFQDN, "tls")
	}
	return srcTLS
}

func (e *Engine) copySidecarMSP(component *config.CommitterNode, componentConfigDir string) error {
	org, identityName, err := e.config.ResolveSidecarIdentity(component.Name)
	if err != nil {
		return err
	}
	absOutputDir, _ := filepath.Abs(e.outputDir)
	cryptoArtifactsDir := filepath.Join(absOutputDir, "build", "config", "cryptogen-artifacts")
	identityFQDN := fmt.Sprintf("%s.%s", identityName, org.Domain)
	srcMSP := filepath.Join(cryptoArtifactsDir, "crypto", "peerOrganizations", org.Domain, "peers", identityFQDN, "msp")
	if _, err := os.Stat(srcMSP); os.IsNotExist(err) {
		srcMSP = filepath.Join(cryptoArtifactsDir, "peerOrganizations", org.Domain, "peers", identityFQDN, "msp")
	}
	dstMSP := filepath.Join(componentConfigDir, "msp")
	if err := e.copyDir(srcMSP, dstMSP); err != nil {
		return fmt.Errorf("copy %s to %s: %w", srcMSP, dstMSP, err)
	}
	e.log("Copied sidecar MSP for %s: %s", component.Name, dstMSP)
	return nil
}

func (e *Engine) getTLSEnabled() bool {
	if e.config.TLS != nil {
		return e.config.TLS.Enabled
	}
	return false // Default to disabled (matches Ansible default: orderer_use_tls: false)
}

func (e *Engine) getTLSClientAuthRequired() bool {
	if e.config.TLS != nil {
		return e.config.TLS.ClientAuthRequired
	}
	return false // Default to disabled
}

func (e *Engine) copyFile(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), perms.Dir); err != nil {
		return err
	}

	// Read source file
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}

	// Default to the shared public-file permission; if the source is restricted
	// (e.g. private key at 0600), preserve that mode so secrets stay secrets.
	perm := perms.FileConfig
	if info, err := os.Stat(src); err == nil {
		if info.Mode().Perm()&0o077 == 0 {
			perm = info.Mode().Perm()
		}
	}
	return os.WriteFile(dst, data, perm)
}

func (e *Engine) copyCryptoMaterials(srcDir, dstDir string, org *config.OrdererOrg) error {
	absOutputDir, _ := filepath.Abs(e.outputDir)
	cryptoArtifactsDir := filepath.Join(absOutputDir, "build", "config", "cryptogen-artifacts")

	// Copy MSP directory
	srcMSP := filepath.Join(srcDir, "msp")
	dstMSP := filepath.Join(dstDir, "msp")
	if _, err := os.Stat(srcMSP); err == nil {
		if err := e.copyDir(srcMSP, dstMSP); err != nil {
			return fmt.Errorf("failed to copy MSP directory: %w", err)
		}
	}

	// Copy admin certificates from organization users
	// Ansible copies Admin@<domain>-cert.pem to msp/admincerts/
	adminCertSrc := filepath.Join(cryptoArtifactsDir, "crypto", "ordererOrganizations", org.Domain, "users", fmt.Sprintf("Admin@%s", org.Domain), "msp", "signcerts", fmt.Sprintf("Admin@%s-cert.pem", org.Domain))
	if _, err := os.Stat(adminCertSrc); os.IsNotExist(err) {
		adminCertSrc = filepath.Join(cryptoArtifactsDir, "ordererOrganizations", org.Domain, "users", fmt.Sprintf("Admin@%s", org.Domain), "msp", "signcerts", fmt.Sprintf("Admin@%s-cert.pem", org.Domain))
	}
	if _, err := os.Stat(adminCertSrc); err == nil {
		adminCertDst := filepath.Join(dstMSP, "admincerts", fmt.Sprintf("Admin@%s-cert.pem", org.Domain))
		if err := e.copyFile(adminCertSrc, adminCertDst); err != nil {
			return fmt.Errorf("failed to copy admin certificate: %w", err)
		}
	}

	// Copy TLS directory
	srcTLS := filepath.Join(srcDir, "tls")
	dstTLS := filepath.Join(dstDir, "tls")
	if _, err := os.Stat(srcTLS); err == nil {
		if err := e.copyDir(srcTLS, dstTLS); err != nil {
			return fmt.Errorf("failed to copy TLS directory: %w", err)
		}
	}

	return nil
}

func (e *Engine) copyDir(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		relPath, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}

		// NOTE: Allow config.yaml to be copied for NodeOUs support
		// Previously skipped to match Ansible behavior, but NodeOUs requires this file
		// to properly identify admin roles without explicit admincerts directory.
		// When EnableNodeOUs is true, cryptogen generates config.yaml with OU definitions.
		// if relPath == "config.yaml" || filepath.Base(relPath) == "config.yaml" {
		// 	return nil
		// }

		// Log when config.yaml is being copied (for verification)
		if relPath == "config.yaml" || filepath.Base(relPath) == "config.yaml" {
			e.log("Copying MSP config.yaml: %s", path)
		}

		dstPath := filepath.Join(dst, relPath)

		if info.IsDir() {
			return os.MkdirAll(dstPath, info.Mode())
		}

		// Read source file
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}

		// Write destination file with original permissions
		return os.WriteFile(dstPath, data, info.Mode())
	})
}

func (e *Engine) executeTemplate(tmpl *template.Template, data interface{}, outputPath string) error {
	file, err := os.Create(outputPath)
	if err != nil {
		return fmt.Errorf("failed to create output file: %w", err)
	}
	defer file.Close()

	if err := tmpl.Execute(file, data); err != nil {
		return err
	}

	// World-readable so the generated tree can be copied to other hosts and
	// consumed by container processes running under any UID/GID.
	if err := os.Chmod(outputPath, perms.FileConfig); err != nil {
		return fmt.Errorf("failed to set file permissions: %w", err)
	}

	return nil
}

func (e *Engine) log(format string, args ...interface{}) {
	if e.verbose {
		fmt.Printf("  [template] "+format+"\n", args...)
	}
}
