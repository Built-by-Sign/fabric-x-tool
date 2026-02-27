package template

import (
	"fmt"
	"os"
	"path/filepath"
	"text/template"

	"config-builder/internal/bccsp"
	"config-builder/internal/config"
)

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
			// Ansible uses mode: "0o750" for directories
			componentDirName := fmt.Sprintf("orderer-%s-%d", componentType, componentIndex)
			componentDir := filepath.Join(absOutputDir, "local-deployment", componentDirName)
			nodeConfigDir := filepath.Join(componentDir, "config")
			if err := os.MkdirAll(nodeConfigDir, 0750); err != nil {
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
		// Skip db type - Ansible does not generate config file for database component
		// Database component only needs data directory, not config file
		if component.Type == "db" {
			// Create data directory for PostgreSQL (will be used by docker-compose)
			componentDirName := fmt.Sprintf("committer-%s", component.Type)
			componentDir := filepath.Join(absOutputDir, "local-deployment", componentDirName)
			dataDir := filepath.Join(componentDir, "data")
			if err := os.MkdirAll(dataDir, 0750); err != nil {
				return fmt.Errorf("failed to create committer db data directory: %w", err)
			}
			e.log("Created data directory for committer: %s (%s)", componentDirName, component.Type)
			continue
		}

		// Create component config directory following Ansible structure: committer-{type}/config/
		// Ansible uses mode: "0o750" for directories
		componentDirName := fmt.Sprintf("committer-%s", component.Type)
		componentDir := filepath.Join(absOutputDir, "local-deployment", componentDirName)
		componentConfigDir := filepath.Join(componentDir, "config")
		if err := os.MkdirAll(componentConfigDir, 0750); err != nil {
			return fmt.Errorf("failed to create committer config directory: %w", err)
		}

		// Generate component config based on type
		// Ansible uses config-{type}.yml naming
		configFileName := fmt.Sprintf("config-%s.yml", component.Type)
		configPath := filepath.Join(componentConfigDir, configFileName)
		if err := e.generateCommitterConfig(component.Type, &component, configPath, componentConfigDir); err != nil {
			return fmt.Errorf("failed to generate config for %s: %w", componentDirName, err)
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
		}

		e.log("Generated config for committer: %s (%s)", componentDirName, component.Type)
	}

	return nil
}

// generateOrdererConfig generates a configuration file for an orderer node
func (e *Engine) generateOrdererConfig(ordererType string, org *config.OrdererOrg, node *config.Node, configPath, configDir, cryptoDir, genesisDir string, partyID int) error {
	// Build template data (pass partyID)
	data := e.buildOrdererTemplateData(ordererType, org, node, configDir, cryptoDir, genesisDir, partyID)

	// Get template based on orderer type
	tmpl, err := e.getOrdererTemplate(ordererType)
	if err != nil {
		return err
	}

	// Execute template
	return e.executeTemplate(tmpl, data, configPath)
}

// generateCommitterConfig generates a configuration file for a committer component
func (e *Engine) generateCommitterConfig(componentType string, component *config.CommitterNode, configPath, configDir string) error {
	// Build template data
	data := e.buildCommitterTemplateData(componentType, component, configDir)

	// Get template based on component type
	tmpl, err := e.getCommitterTemplate(componentType)
	if err != nil {
		return err
	}

	// Execute template
	return e.executeTemplate(tmpl, data, configPath)
}

// buildOrdererTemplateData builds template data for orderer nodes
func (e *Engine) buildOrdererTemplateData(ordererType string, org *config.OrdererOrg, node *config.Node, configDir, cryptoDir, genesisDir string, partyID int) *OrdererTemplateData {
	// Use container path for configDir (matches Ansible's orderer_docker_config_dir = "/config")
	// The configDir parameter is the host path, but we need container paths in the config file
	containerConfigDir := "/config"

	// Generate BCCSP configuration based on KMS settings
	var bccsConfig *bccsp.BCCSPConfig
	if e.config.KMS != nil && e.config.KMS.Enabled {
		// KMS mode: use KMS PKCS11 library with organization-level PIN
		// Priority: org.KMSUserPin > node.UserPin (backward compatibility)
		orgPin := org.KMSUserPin
		if orgPin == "" {
			orgPin = node.UserPin // Backward compatibility
		}
		// KMS mode: use unified token label from KMS config, not org-specific label
		tokenLabel := e.config.KMS.TokenLabel
		if tokenLabel == "" {
			tokenLabel = "tk" // Default KMS token label
		}
		bccsConfig = bccsp.GenerateKMSConfig(e.config.KMS.Endpoint, tokenLabel, orgPin)
	} else {
		// Software mode
		bccsConfig = bccsp.GenerateSoftwareConfig()
	}

	data := &OrdererTemplateData{
		PartyID:       partyID, // Use provided partyID (each org has different PartyID)
		OrdererType:   ordererType,
		ShardID:       node.ShardID,
		ConfigDir:     containerConfigDir, // Container path, not host path
		CryptoDir:     cryptoDir,
		GenesisDir:    genesisDir,
		ListenAddress: "0.0.0.0",
		ListenPort:    node.Port,
		MSPID:         org.Name,
		ChannelID:     e.config.ChannelID,
		BCCSP:         bccsConfig, // Use generated BCCSP config
		TLS: TLSConfig{
			Enabled:            e.getTLSEnabled(),            // Use config value or default to false
			ClientAuthRequired: e.getTLSClientAuthRequired(), // Use config value or default to false
			PrivateKey:         filepath.Join(containerConfigDir, "tls", "server.key"),
			Certificate:        filepath.Join(containerConfigDir, "tls", "server.crt"),
			RootCAs:            []string{filepath.Join(containerConfigDir, "tls", "ca.crt")},
		},
	}

	return data
}

// buildCommitterTemplateData builds template data for committer components
func (e *Engine) buildCommitterTemplateData(componentType string, component *config.CommitterNode, configDir string) *CommitterTemplateData {
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
		ChannelID:        e.config.ChannelID,
		GenesisBlockPath: filepath.Join(containerConfigDir, "genesis.block"), // Container path
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

		// Collect all orderer assembler endpoints
		for _, org := range e.config.OrdererOrgs {
			for _, orderer := range org.Orderers {
				if orderer.Type == "assembler" {
					data.AssemblerEndpoints = append(data.AssemblerEndpoints, EndpointConfig{
						Host: dockerHost,
						Port: orderer.Port,
					})
				}
			}
		}
	}

	// Add database config if applicable
	if componentType == "db" {
		// Database component itself
		data.Database = &DatabaseConfig{
			Type:     "postgres",
			Host:     component.Host,
			Port:     component.Port,
			User:     component.PostgresUser,
			Password: component.PostgresPassword,
			DBName:   component.PostgresDB,
		}
	} else if (componentType == "validator" || componentType == "query-service") && e.config.Committer.UsePostgres {
		// Find database component
		for _, comp := range e.config.Committer.Components {
			if comp.Type == "db" {
				// Ansible uses: {{ hostvars[db].ansible_host }}:{{ hostvars[db].postgres_port }}
				// On macOS, ansible_host is "host.docker.internal", postgres_port is the host port (5435)
				// This allows containers to access the database through Docker Desktop's special network
				// Use dockerHost (host.docker.internal) and comp.Port (host port, e.g., 5435)
				dbPort := comp.Port  // Use host port (e.g., 5435) as Ansible does
				dbHost := dockerHost // Use host.docker.internal (matches Ansible's ansible_host)
				data.Database = &DatabaseConfig{
					Type:     "postgres",
					Host:     dbHost, // host.docker.internal (matches Ansible)
					Port:     dbPort, // Host port (5435) as Ansible uses postgres_port
					User:     comp.PostgresUser,
					Password: comp.PostgresPassword,
					DBName:   comp.PostgresDB,
				}
				break
			}
		}
	}

	return data
}

// Helper functions

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
	// Create destination directory if needed
	// Ansible uses mode: "0o750" for directories
	if err := os.MkdirAll(filepath.Dir(dst), 0750); err != nil {
		return err
	}

	// Read source file
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}

	// Write destination file
	// For genesis.block and other data files, use 0o644 (rw-r--r--)
	// For sensitive files like private keys, preserve original permissions
	perm := os.FileMode(0644)
	if info, err := os.Stat(src); err == nil {
		// Preserve original permissions for sensitive files (like private keys)
		if info.Mode().Perm()&0077 == 0 {
			// If original file has no group/other permissions, preserve it
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

	// Set file permissions to match Ansible: 0o640 (rw-r-----) for config files
	// Ansible uses mode: "0o640" for node_config.yaml and config-*.yml files
	if err := os.Chmod(outputPath, 0640); err != nil {
		return fmt.Errorf("failed to set file permissions: %w", err)
	}

	return nil
}

func (e *Engine) log(format string, args ...interface{}) {
	if e.verbose {
		fmt.Printf("  [template] "+format+"\n", args...)
	}
}
