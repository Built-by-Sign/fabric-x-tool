package config

// NetworkConfig represents the complete network configuration
type NetworkConfig struct {
	// Global settings
	ProjectDir string `yaml:"project_dir"`
	OutputDir  string `yaml:"output_dir"`
	ChannelID  string `yaml:"channel_id"`
	CliVersion string `yaml:"cli_version"`

	// KMS configuration
	KMS *KMSConfig `yaml:"kms,omitempty"`

	// TLS configuration
	TLS *TLSConfig `yaml:"tls,omitempty"`

	// Organizations
	OrdererOrgs []OrdererOrg `yaml:"orderer_orgs"`
	PeerOrgs    []PeerOrg    `yaml:"peer_orgs"`

	// Committer configuration
	Committer *CommitterConfig `yaml:"committer"`

	// Docker settings
	Docker DockerConfig `yaml:"docker"`
}

// TLSConfig represents TLS configuration for orderer nodes
type TLSConfig struct {
	Enabled            bool `yaml:"enabled"`              // Enable TLS for orderer nodes
	ClientAuthRequired bool `yaml:"client_auth_required"` // Require client authentication (mTLS)
}

// KMSConfig represents KMS configuration for remote HSM access
type KMSConfig struct {
	Enabled    bool   `yaml:"enabled"`     // Enable KMS integration
	Endpoint   string `yaml:"endpoint"`    // KMS service endpoint address
	TokenLabel string `yaml:"token_label"` // Base token label for KMS
	CAURL      string `yaml:"ca_url"`      // Fabric CA URL for certificate enrollment
}

// OrdererOrg represents an orderer organization
type OrdererOrg struct {
	Name                  string `yaml:"name"`
	Domain                string `yaml:"domain"`
	EnableOrganizationOUs bool   `yaml:"enable_organizational_units"`
	Orderers              []Node `yaml:"orderers"`
	KMSTokenLabel         string `yaml:"kms_token_label"` // Token label for KMS (organization-level)
	KMSUserPin            string `yaml:"kms_user_pin"`    // User PIN for KMS access (organization-level, corresponds to token)
}

// PeerOrg represents a peer organization
type PeerOrg struct {
	Name                  string `yaml:"name"`
	Domain                string `yaml:"domain"`
	EnableOrganizationOUs bool   `yaml:"enable_organizational_units"`
	Peers                 []Node `yaml:"peers"`
	Users                 []User `yaml:"users"`
	KMSTokenLabel         string `yaml:"kms_token_label"` // Token label for KMS (organization-level)
	KMSUserPin            string `yaml:"kms_user_pin"`    // User PIN for KMS access (organization-level, corresponds to token)
}

// Node represents a network node (orderer or peer)
type Node struct {
	Name    string `yaml:"name"`
	Type    string `yaml:"type"` // router, batcher, consenter, assembler (for orderer)
	Port    int    `yaml:"port"`
	ShardID int    `yaml:"shard_id,omitempty"`
	Host    string `yaml:"host"`
	UserPin string `yaml:"user_pin,omitempty"` // User PIN for KMS access (per-node)
}

// User represents a user identity
type User struct {
	Name               string `yaml:"name"`
	MetaNamespaceAdmin bool   `yaml:"meta_namespace_admin,omitempty"`
}

// CommitterConfig represents committer component configuration
type CommitterConfig struct {
	UsePostgres bool            `yaml:"use_postgres"`
	Components  []CommitterNode `yaml:"components"`
}

// CommitterNode represents a committer component
type CommitterNode struct {
	Name string `yaml:"name"`
	Type string `yaml:"type"` // db, validator, verifier, coordinator, sidecar, query-service
	Port int    `yaml:"port"`
	Host string `yaml:"host"`

	// Database specific
	PostgresUser     string `yaml:"postgres_user,omitempty"`
	PostgresPassword string `yaml:"postgres_password,omitempty"`
	PostgresDB       string `yaml:"postgres_db,omitempty"`
}

// DockerConfig represents Docker-related settings
type DockerConfig struct {
	Name            string `yaml:"name"`
	Network         string `yaml:"network"`
	NetworkDriver   string `yaml:"network_driver"`
	NetworkExternal bool   `yaml:"network_external"`

	// Image settings
	OrdererImage   string `yaml:"orderer_image"`
	CommitterImage string `yaml:"committer_image"`

	// Tools image (for cryptogen, configtxgen, etc.)
	// Defaults to docker.io/hyperledger/fabric-x-tools:0.0.4 (matching Ansible)
	ToolsImage string `yaml:"tools_image"`

	// UseLocalTools determines whether to use local tools instead of Docker
	// When true, cryptogen, configtxgen, and fabric-ca-client will be executed directly
	// Requires these tools to be installed and available in PATH
	UseLocalTools bool `yaml:"use_local_tools"`

	PostgresImage string `yaml:"postgres_image"`
}

// DefaultConfig returns a default network configuration
func DefaultConfig() *NetworkConfig {
	return &NetworkConfig{
		ProjectDir: ".",
		OutputDir:  "./out",
		ChannelID:  "arma",
		CliVersion: "latest",
		Docker: DockerConfig{
			Name:          "fx-network",
			Network:       "fx-network_net",
			NetworkDriver: "bridge",
			OrdererImage:  "hyperledger/fabric-x-orderer:local",
			ToolsImage:    "docker.io/hyperledger/fabric-x-tools:0.0.4", // Match Ansible default
		},
	}
}
