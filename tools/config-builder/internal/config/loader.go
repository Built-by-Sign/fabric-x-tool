package config

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"

	"gopkg.in/yaml.v3"
)

// expandEnvVars replaces ${VAR} or $VAR with environment variable values
func expandEnvVars(data []byte) []byte {
	// Replace ${VAR} pattern
	re := regexp.MustCompile(`\$\{([A-Z_][A-Z0-9_]*)\}`)
	result := re.ReplaceAllFunc(data, func(match []byte) []byte {
		varName := string(match[2 : len(match)-1]) // Extract variable name from ${VAR}
		if value := os.Getenv(varName); value != "" {
			return []byte(value)
		}
		return match // Keep original if env var not found
	})

	return result
}

// Load reads and parses a network configuration file
func Load(path string) (*NetworkConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	// Expand environment variables in the config file
	data = expandEnvVars(data)

	config := DefaultConfig()
	if err := yaml.Unmarshal(data, config); err != nil {
		return nil, fmt.Errorf("failed to parse config file: %w", err)
	}

	// Auto-detect project_dir if not set or is empty/relative
	// If project_dir is empty or ".", auto-detect from config file location
	// If project_dir is a relative path, resolve it first, then validate
	if config.ProjectDir == "" || config.ProjectDir == "." {
		// Auto-detect: find cbdc-network root directory
		detectedDir, err := detectProjectDir(path)
		if err != nil {
			return nil, fmt.Errorf("failed to detect project directory: %w", err)
		}
		config.ProjectDir = detectedDir
	} else if !filepath.IsAbs(config.ProjectDir) {
		// Resolve relative path based on config file directory
		configDir := filepath.Dir(path)
		absConfigDir, err := filepath.Abs(configDir)
		if err != nil {
			return nil, fmt.Errorf("failed to get absolute path for config directory: %w", err)
		}
		resolvedDir, err := filepath.Abs(filepath.Join(absConfigDir, config.ProjectDir))
		if err != nil {
			return nil, fmt.Errorf("failed to resolve project directory: %w", err)
		}
		config.ProjectDir = resolvedDir
	}

	// Resolve relative paths
	if !filepath.IsAbs(config.OutputDir) {
		config.OutputDir = filepath.Join(config.ProjectDir, config.OutputDir)
	}

	return config, nil
}

// Save writes a network configuration to a file
func Save(config *NetworkConfig, path string) error {
	data, err := yaml.Marshal(config)
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("failed to write config file: %w", err)
	}

	return nil
}

// detectProjectDir automatically detects the project directory by looking for
// the cbdc-network root directory (contains tools/ directory)
func detectProjectDir(configPath string) (string, error) {
	configDir, err := filepath.Abs(filepath.Dir(configPath))
	if err != nil {
		return "", fmt.Errorf("failed to get config directory: %w", err)
	}

	// Start from config file directory and walk up to find cbdc-network root
	// The root directory should contain a "tools" directory
	currentDir := configDir
	for {
		// Check if this directory contains "tools" directory (indicating cbdc-network root)
		toolsDir := filepath.Join(currentDir, "tools")
		if info, err := os.Stat(toolsDir); err == nil && info.IsDir() {
			return currentDir, nil
		}

		// Check if we've reached the filesystem root
		parentDir := filepath.Dir(currentDir)
		if parentDir == currentDir {
			// Reached filesystem root, use config directory's parent as fallback
			// This handles cases where config is in config-builder/configs/
			if filepath.Base(configDir) == "configs" {
				return filepath.Dir(filepath.Dir(configDir)), nil
			}
			return filepath.Dir(configDir), nil
		}

		currentDir = parentDir
	}
}

// Validate checks if the configuration is valid
func (c *NetworkConfig) Validate() error {
	if c.ChannelID == "" {
		return fmt.Errorf("channel_id is required")
	}

	if len(c.OrdererOrgs) == 0 {
		return fmt.Errorf("at least one orderer organization is required")
	}

	for i, org := range c.OrdererOrgs {
		if org.Name == "" {
			return fmt.Errorf("orderer_orgs[%d].name is required", i)
		}
		if org.Domain == "" {
			return fmt.Errorf("orderer_orgs[%d].domain is required", i)
		}
		if len(org.Orderers) == 0 {
			return fmt.Errorf("orderer_orgs[%d] must have at least one orderer", i)
		}
	}

	return nil
}

// GetOrdererOrg returns the orderer organization by name
func (c *NetworkConfig) GetOrdererOrg(name string) *OrdererOrg {
	for i := range c.OrdererOrgs {
		if c.OrdererOrgs[i].Name == name {
			return &c.OrdererOrgs[i]
		}
	}
	return nil
}

// GetPeerOrg returns the peer organization by name
func (c *NetworkConfig) GetPeerOrg(name string) *PeerOrg {
	for i := range c.PeerOrgs {
		if c.PeerOrgs[i].Name == name {
			return &c.PeerOrgs[i]
		}
	}
	return nil
}

// AllOrderers returns all orderer nodes across all organizations
func (c *NetworkConfig) AllOrderers() []Node {
	var nodes []Node
	for _, org := range c.OrdererOrgs {
		nodes = append(nodes, org.Orderers...)
	}
	return nodes
}

// AllPeers returns all peer nodes across all organizations
func (c *NetworkConfig) AllPeers() []Node {
	var nodes []Node
	for _, org := range c.PeerOrgs {
		nodes = append(nodes, org.Peers...)
	}
	return nodes
}
