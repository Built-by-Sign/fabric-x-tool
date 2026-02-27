package crypto

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"config-builder/internal/config"

	"gopkg.in/yaml.v3"
)

// Generator handles crypto material generation
// Note: cryptogen is always run via Docker container (local binary support removed)
type Generator struct {
	config    *config.NetworkConfig
	outputDir string
	verbose   bool
}

// NewGenerator creates a new crypto generator
func NewGenerator(cfg *config.NetworkConfig, outputDir string, verbose bool) *Generator {
	return &Generator{
		config:    cfg,
		outputDir: outputDir,
		verbose:   verbose,
	}
}

// Generate generates all crypto materials
func (g *Generator) Generate() error {
	// Step 1: Find cryptogen (prefer Docker container, fallback to local binary)
	_, err := g.findCryptogen()
	if err != nil {
		return fmt.Errorf("failed to find cryptogen: %w", err)
	}

	// Step 2: Generate crypto-config.yaml
	configPath, err := g.generateCryptoConfig()
	if err != nil {
		return fmt.Errorf("failed to generate crypto-config.yaml: %w", err)
	}

	// Step 3: Run cryptogen
	if err := g.runCryptogen(configPath); err != nil {
		return fmt.Errorf("failed to run cryptogen: %w", err)
	}

	return nil
}

// GenerateCryptoConfigOnly generates only the crypto-config.yaml file
func (g *Generator) GenerateCryptoConfigOnly() (string, error) {
	return g.generateCryptoConfig()
}

// findCryptogen verifies cryptogen is available (either via Docker or local binary)
// Returns empty string (cryptogen runs via Docker container) and an error if unavailable
func (g *Generator) findCryptogen() (string, error) {
	// If use_local_tools is enabled, check for local cryptogen binary
	if g.config.Docker.UseLocalTools {
		if _, err := exec.LookPath("cryptogen"); err != nil {
			return "", fmt.Errorf("cryptogen not found in PATH. Please install it or use Docker mode (set use_local_tools: false)")
		}
		g.log("Using local cryptogen binary")
		return "cryptogen", nil
	}

	// Docker mode: check if Docker is available
	if !g.checkDockerAvailable() {
		return "", fmt.Errorf("Docker is required to run cryptogen. Please ensure Docker is installed and the image %s is available", g.config.Docker.ToolsImage)
	}

	g.log("Using cryptogen from Docker container: %s", g.config.Docker.ToolsImage)
	return "", nil
}

// checkDockerAvailable checks if Docker and the tools image are available
func (g *Generator) checkDockerAvailable() bool {
	// Check if docker command is available
	if _, err := exec.LookPath("docker"); err != nil {
		g.log("Docker not found in PATH")
		return false
	}

	// Get the image name
	image := g.config.Docker.ToolsImage
	if image == "" {
		image = "docker.io/hyperledger/fabric-x-tools:0.0.4" // Default from Ansible
	}

	// Try to inspect the image to see if it exists
	cmd := exec.Command("docker", "image", "inspect", image)
	cmd.Stdout = nil
	cmd.Stderr = nil
	if err := cmd.Run(); err == nil {
		return true // Image exists
	}

	// Image doesn't exist locally, but Docker is available
	// We'll try to use it anyway and let docker pull it if needed
	g.log("Docker image %s not found locally, will attempt to pull when needed", image)
	return true
}

// generateCryptoConfig generates the crypto-config.yaml file
func (g *Generator) generateCryptoConfig() (string, error) {
	absOutputDir, err := filepath.Abs(g.outputDir)
	if err != nil {
		return "", fmt.Errorf("failed to get absolute output path: %w", err)
	}

	configDir := filepath.Join(absOutputDir, "build", "config", "cryptogen-artifacts")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create config directory: %w", err)
	}

	configPath := filepath.Join(configDir, "crypto-config.yaml")

	// Build crypto-config structure
	cryptoConfig := g.buildCryptoConfig()

	// Marshal to YAML
	data, err := yaml.Marshal(cryptoConfig)
	if err != nil {
		return "", fmt.Errorf("failed to marshal crypto config: %w", err)
	}

	// Write to file
	if err := os.WriteFile(configPath, data, 0644); err != nil {
		return "", fmt.Errorf("failed to write crypto config: %w", err)
	}

	g.log("Generated crypto-config.yaml at: %s", configPath)
	return configPath, nil
}

// CryptoConfig represents the structure for crypto-config.yaml
type CryptoConfig struct {
	OrdererOrgs []OrgSpec `yaml:"OrdererOrgs"`
	PeerOrgs    []OrgSpec `yaml:"PeerOrgs,omitempty"`
}

// OrgSpec matches cryptogen's OrgSpec
type OrgSpec struct {
	Name          string        `yaml:"Name"`
	Domain        string        `yaml:"Domain"`
	EnableNodeOUs bool          `yaml:"EnableNodeOUs"`
	CA            *NodeSpec     `yaml:"CA,omitempty"`
	Template      *NodeTemplate `yaml:"Template,omitempty"`
	Specs         []NodeSpec    `yaml:"Specs,omitempty"`
	Users         *UsersSpec    `yaml:"Users,omitempty"`
}

// NodeSpec matches cryptogen's NodeSpec
type NodeSpec struct {
	Hostname           string   `yaml:"Hostname,omitempty"`
	CommonName         string   `yaml:"CommonName,omitempty"`
	SANS               []string `yaml:"SANS,omitempty"`
	PublicKeyAlgorithm string   `yaml:"PublicKeyAlgorithm,omitempty"`
}

// NodeTemplate matches cryptogen's NodeTemplate
type NodeTemplate struct {
	Count              int      `yaml:"Count"`
	Start              int      `yaml:"Start,omitempty"`
	Hostname           string   `yaml:"Hostname,omitempty"`
	SANS               []string `yaml:"SANS,omitempty"`
	PublicKeyAlgorithm string   `yaml:"PublicKeyAlgorithm,omitempty"`
}

// UsersSpec matches cryptogen's UsersSpec
type UsersSpec struct {
	Count              int        `yaml:"Count"`
	PublicKeyAlgorithm string     `yaml:"PublicKeyAlgorithm,omitempty"`
	Specs              []UserSpec `yaml:"Specs,omitempty"`
}

// UserSpec matches cryptogen's UserSpec
type UserSpec struct {
	Name string `yaml:"Name"`
}

// buildCryptoConfig builds the crypto configuration from network config
func (g *Generator) buildCryptoConfig() *CryptoConfig {
	cc := &CryptoConfig{
		OrdererOrgs: make([]OrgSpec, 0, len(g.config.OrdererOrgs)),
		PeerOrgs:    make([]OrgSpec, 0, len(g.config.PeerOrgs)),
	}

	// Convert orderer orgs
	for _, org := range g.config.OrdererOrgs {
		orgSpec := g.convertOrdererOrg(&org)
		cc.OrdererOrgs = append(cc.OrdererOrgs, orgSpec)
	}

	// Convert peer orgs
	for _, org := range g.config.PeerOrgs {
		orgSpec := g.convertPeerOrg(&org)
		cc.PeerOrgs = append(cc.PeerOrgs, orgSpec)
	}

	return cc
}

// convertOrdererOrg converts network config orderer org to crypto config format
func (g *Generator) convertOrdererOrg(org *config.OrdererOrg) OrgSpec {
	// Ansible defaults to false if enable_organizational_units is not set
	// We match this behavior: if not explicitly set, default to false
	enableNodeOUs := org.EnableOrganizationOUs
	// Note: If the config explicitly sets it to true, we respect that
	// But to match Ansible's default behavior, we should check if it was explicitly set
	// For now, we use the value as-is, but this may need adjustment based on actual Ansible inventory

	spec := OrgSpec{
		Name:          org.Name,
		Domain:        org.Domain,
		EnableNodeOUs: enableNodeOUs,
		Specs:         make([]NodeSpec, 0, len(org.Orderers)),
	}

	// Convert orderer nodes to specs
	for _, node := range org.Orderers {
		// Build FQDN for this orderer node
		ordererFQDN := fmt.Sprintf("%s.%s", node.Name, org.Domain)

		nodeSpec := NodeSpec{
			Hostname: node.Name,
			// Include FQDN in SANS to support both FQDN and host.docker.internal connections
			// This fixes TLS certificate validation when connecting via host.docker.internal
			SANS: []string{
				ordererFQDN, // Add FQDN as first SAN
				"host.docker.internal",
				"0.0.0.0",
				"localhost",
				"127.0.0.1",
				"::1",
			},
		}
		spec.Specs = append(spec.Specs, nodeSpec)
	}

	// Don't add Users field for orderer orgs (matching Ansible behavior)
	// Admin user is created automatically by cryptogen

	return spec
}

// convertPeerOrg converts network config peer org to crypto config format
func (g *Generator) convertPeerOrg(org *config.PeerOrg) OrgSpec {
	spec := OrgSpec{
		Name:          org.Name,
		Domain:        org.Domain,
		EnableNodeOUs: org.EnableOrganizationOUs,
		Specs:         make([]NodeSpec, 0, len(org.Peers)),
	}

	// Convert peer nodes to specs
	// Only add Specs if there are peers defined (matching Ansible behavior)
	if len(org.Peers) > 0 {
		for _, node := range org.Peers {
			// Build FQDN for this peer node
			peerFQDN := fmt.Sprintf("%s.%s", node.Name, org.Domain)

			nodeSpec := NodeSpec{
				Hostname: node.Name,
				// Include FQDN in SANS to support both FQDN and host.docker.internal connections
				// This fixes TLS certificate validation when connecting via host.docker.internal
				SANS: []string{
					peerFQDN, // Add FQDN as first SAN
					"host.docker.internal",
					"0.0.0.0",
					"localhost",
					"127.0.0.1",
					"::1",
				},
			}
			spec.Specs = append(spec.Specs, nodeSpec)
		}
	}
	// If no peers, don't set Specs field (matching Ansible behavior)

	// Convert users
	// Count is the number of users in addition to Admin (matching Ansible behavior)
	userCount := 0
	userSpecs := make([]UserSpec, 0)
	for _, user := range org.Users {
		if user.Name != "Admin" {
			userSpecs = append(userSpecs, UserSpec{
				Name: user.Name,
			})
			userCount++
		}
	}
	// Only add Users field if there are non-Admin users
	// Count should be the number of additional users (excluding Admin)
	if userCount > 0 {
		spec.Users = &UsersSpec{
			Count: userCount, // Count is in addition to Admin
			Specs: userSpecs,
		}
	}

	return spec
}

// runCryptogen executes the cryptogen tool via Docker container
// Local binary support has been removed - Docker is now required
func (g *Generator) runCryptogen(configPath string) error {
	absOutputDir, _ := filepath.Abs(g.outputDir)
	// Cryptogen generates files directly in the output directory (peerOrganizations, ordererOrganizations)
	// But we need them in a "crypto" subdirectory to match the expected structure
	// So we point cryptogen to a temp directory, then move files to crypto/
	baseDir := filepath.Join(absOutputDir, "build", "config", "cryptogen-artifacts")
	tempOutputDir := filepath.Join(baseDir, "temp-crypto")
	cryptoDir := filepath.Join(baseDir, "crypto")

	// Always use Docker container (local binary support removed)
	return g.runCryptogenContainer(configPath, baseDir, tempOutputDir, cryptoDir)
}

// runCryptogenContainer runs cryptogen using Docker container or local binary
func (g *Generator) runCryptogenContainer(configPath, baseDir, tempOutputDir, cryptoDir string) error {
	if g.config.Docker.UseLocalTools {
		return g.runCryptogenLocal(configPath, tempOutputDir, cryptoDir)
	}
	return g.runCryptogenDocker(configPath, baseDir, tempOutputDir, cryptoDir)
}

// runCryptogenLocal runs cryptogen using local binary
func (g *Generator) runCryptogenLocal(configPath, outputDir, cryptoDir string) error {
	// Check if cryptogen is available
	if _, err := exec.LookPath("cryptogen"); err != nil {
		return fmt.Errorf("cryptogen not found in PATH. Please install it or use Docker mode (set use_local_tools: false)")
	}

	// Ensure output directory exists
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return fmt.Errorf("failed to create output directory: %w", err)
	}

	// Build command
	cmd := exec.Command("cryptogen",
		"generate",
		"--config", configPath,
		"--output", outputDir,
	)

	g.log("Running cryptogen locally: %v", cmd.Args)

	// Execute command
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("cryptogen failed: %w\nOutput: %s", err, string(output))
	}

	if g.verbose {
		fmt.Printf("Cryptogen output: %s\n", string(output))
	}

	// Move generated files from temp directory to crypto/ subdirectory
	return g.moveCryptoFiles(outputDir, cryptoDir)
}

// runCryptogenDocker runs cryptogen using Docker container (matching Ansible behavior)
func (g *Generator) runCryptogenDocker(configPath, baseDir, tempOutputDir, cryptoDir string) error {
	image := g.config.Docker.ToolsImage
	if image == "" {
		image = "docker.io/hyperledger/fabric-x-tools:0.0.4" // Default from Ansible
	}

	// Docker paths (matching Ansible's cryptogen_docker_config_dir and cryptogen_docker_output_dir)
	dockerConfigDir := "/tmp/cryptogen-artifacts"
	dockerOutputDir := filepath.Join(dockerConfigDir, "crypto")

	// Ensure directories exist
	if err := os.MkdirAll(baseDir, 0755); err != nil {
		return fmt.Errorf("failed to create base directory: %w", err)
	}
	if err := os.MkdirAll(tempOutputDir, 0755); err != nil {
		return fmt.Errorf("failed to create temp output directory: %w", err)
	}

	// Build docker run command (matching Ansible's container/start.yaml)
	args := []string{
		"run",
		"--rm",                                               // Auto-remove container (matches container_autoremove: true)
		"-v", fmt.Sprintf("%s:%s", baseDir, dockerConfigDir), // Mount config directory
	}

	// Set user to current user (matches container_run_as_host_user: true)
	if uid := os.Getuid(); uid != 0 {
		if gid := os.Getgid(); gid != 0 {
			args = append(args, "-u", fmt.Sprintf("%d:%d", uid, gid))
		}
	}

	// Copy config file to baseDir so it's accessible in container
	configFile := filepath.Base(configPath)
	configInBaseDir := filepath.Join(baseDir, configFile)
	configData, err := os.ReadFile(configPath)
	if err != nil {
		return fmt.Errorf("failed to read config file: %w", err)
	}
	if err := os.WriteFile(configInBaseDir, configData, 0644); err != nil {
		return fmt.Errorf("failed to copy config file to container directory: %w", err)
	}

	// Container command (matching Ansible's container_command)
	containerCmd := fmt.Sprintf("cryptogen generate --config=%s/%s --output=%s",
		dockerConfigDir, configFile, dockerOutputDir)
	args = append(args, image, "sh", "-c", containerCmd)

	g.log("Running cryptogen via Docker: docker %v", args)

	cmd := exec.Command("docker", args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("cryptogen container failed: %w\nOutput: %s", err, string(output))
	}

	if g.verbose {
		fmt.Println(string(output))
	}

	// Move generated files from temp directory to crypto/ subdirectory
	return g.moveCryptoFiles(tempOutputDir, cryptoDir)
}

// moveCryptoFiles moves generated crypto files to the final location
func (g *Generator) moveCryptoFiles(tempOutputDir, cryptoDir string) error {
	// Move generated files from temp directory to crypto/ subdirectory
	// Cryptogen generates peerOrganizations and ordererOrganizations directly in output dir
	// We need them in crypto/ subdirectory
	if err := os.MkdirAll(cryptoDir, 0755); err != nil {
		os.RemoveAll(tempOutputDir)
		return fmt.Errorf("failed to create crypto directory: %w", err)
	}

	// Move peerOrganizations if it exists
	peerOrgsSrc := filepath.Join(tempOutputDir, "peerOrganizations")
	if _, err := os.Stat(peerOrgsSrc); err == nil {
		peerOrgsDst := filepath.Join(cryptoDir, "peerOrganizations")
		if err := os.Rename(peerOrgsSrc, peerOrgsDst); err != nil {
			os.RemoveAll(tempOutputDir)
			return fmt.Errorf("failed to move peerOrganizations: %w", err)
		}
	}

	// Move ordererOrganizations if it exists
	ordererOrgsSrc := filepath.Join(tempOutputDir, "ordererOrganizations")
	if _, err := os.Stat(ordererOrgsSrc); err == nil {
		ordererOrgsDst := filepath.Join(cryptoDir, "ordererOrganizations")
		if err := os.Rename(ordererOrgsSrc, ordererOrgsDst); err != nil {
			os.RemoveAll(tempOutputDir)
			return fmt.Errorf("failed to move ordererOrganizations: %w", err)
		}
	}

	// Clean up temp directory
	if err := os.RemoveAll(tempOutputDir); err != nil {
		g.log("Warning: failed to remove temp directory: %v", err)
	}

	g.log("Crypto materials generated successfully")
	return nil
}

// log prints a message if verbose mode is enabled
func (g *Generator) log(format string, args ...interface{}) {
	if g.verbose {
		fmt.Printf("  [crypto] "+format+"\n", args...)
	}
}
