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
	Name                  string   `yaml:"name"`
	Domain                string   `yaml:"domain"`
	EnableOrganizationOUs bool     `yaml:"enable_organizational_units"`
	Orderers              []Node   `yaml:"orderers"`
	KMSTokenLabel         string   `yaml:"kms_token_label"`         // Token label for KMS (organization-level)
	KMSUserPin            string   `yaml:"kms_user_pin"`            // User PIN for KMS access (organization-level, corresponds to token)
	CAURL                 string   `yaml:"ca_url,omitempty"`        // fabric-ca-server URL for MSP enroll (org-level override of kms.ca_url)
	TLSCAURL              string   `yaml:"tls_ca_url,omitempty"`    // fabric-ca-server URL for TLS leaf enroll (falls back to CAURL)
	KMSSetupPin           string   `yaml:"kms_setup_pin,omitempty"` // PKCS#11 BCCSP PIN used at setup-kms time (falls back to KMSUserPin)
	TLSHosts              []string `yaml:"tls_hosts,omitempty"`     // Extra SAN entries applied to every node's TLS cert (LB aliases, prod IPs, ...)
}

// PeerOrg represents a peer organization
type PeerOrg struct {
	Name                  string   `yaml:"name"`
	Domain                string   `yaml:"domain"`
	EnableOrganizationOUs bool     `yaml:"enable_organizational_units"`
	Peers                 []Node   `yaml:"peers"`
	Users                 []User   `yaml:"users"`
	KMSTokenLabel         string   `yaml:"kms_token_label"`         // Token label for KMS (organization-level)
	KMSUserPin            string   `yaml:"kms_user_pin"`            // User PIN for KMS access (organization-level, corresponds to token)
	CAURL                 string   `yaml:"ca_url,omitempty"`        // fabric-ca-server URL for MSP enroll (org-level override of kms.ca_url)
	TLSCAURL              string   `yaml:"tls_ca_url,omitempty"`    // fabric-ca-server URL for TLS leaf enroll (falls back to CAURL)
	KMSSetupPin           string   `yaml:"kms_setup_pin,omitempty"` // PKCS#11 BCCSP PIN used at setup-kms time (falls back to KMSUserPin)
	TLSHosts              []string `yaml:"tls_hosts,omitempty"`     // Extra SAN entries applied to every node's TLS cert (LB aliases, prod IPs, ...)
}

// Node represents a network node (orderer or peer)
type Node struct {
	Name           string `yaml:"name"`
	Type           string `yaml:"type"` // router, batcher, consensus, assembler (for orderer)
	Port           int    `yaml:"port"`
	MonitoringPort int    `yaml:"monitoring_port,omitempty"`
	ShardID        int    `yaml:"shard_id,omitempty"`
	Host           string `yaml:"host"`
	UserPin        string `yaml:"user_pin,omitempty"` // User PIN for KMS access (per-node)
}

// User represents a user identity
type User struct {
	Name               string `yaml:"name"`
	MetaNamespaceAdmin bool   `yaml:"meta_namespace_admin,omitempty"`
}

// CommitterConfig represents committer component configuration
type CommitterConfig struct {
	UsePostgres     bool                   `yaml:"use_postgres"`
	Database        *CommitterDatabase     `yaml:"database,omitempty"`
	SidecarIdentity *SidecarIdentityConfig `yaml:"sidecar_identity,omitempty"`
	Components      []CommitterNode        `yaml:"components"`
}

// CommitterDatabase represents an external database connection used by
// validator/query-service, aligned with the Fabric-X Ansible collection's
// database configuration model.
type CommitterDatabase struct {
	Type                 string             `yaml:"type,omitempty"` // postgres or yugabyte
	Endpoints            []DatabaseEndpoint `yaml:"endpoints,omitempty"`
	Username             string             `yaml:"username,omitempty"`
	Password             string             `yaml:"password,omitempty"`
	Database             string             `yaml:"database,omitempty"`
	LoadBalance          *bool              `yaml:"load_balance,omitempty"`
	TablePreSplitTablets int                `yaml:"table_pre_split_tablets,omitempty"`
	TLS                  *DatabaseTLS       `yaml:"tls,omitempty"`
}

// DatabaseEndpoint is a database host:port pair.
type DatabaseEndpoint struct {
	Host string `yaml:"host"`
	Port int    `yaml:"port"`
}

// DatabaseTLS configures one-way TLS for database connections.
type DatabaseTLS struct {
	Enabled    bool   `yaml:"enabled"`
	CACertPath string `yaml:"ca_cert_path,omitempty"`
}

// SidecarIdentityConfig selects the peer-organization identity used by the
// committer sidecar when it signs deliver requests to orderers.
type SidecarIdentityConfig struct {
	Org  string `yaml:"org,omitempty"`
	Name string `yaml:"name,omitempty"`
}

// CommitterNode represents a committer component
type CommitterNode struct {
	Name           string `yaml:"name"`
	Type           string `yaml:"type"` // db, validator, verifier, coordinator, sidecar, query-service
	Port           int    `yaml:"port"`
	MonitoringPort int    `yaml:"monitoring_port,omitempty"`
	Host           string `yaml:"host"`

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

// DefaultMonitoringPort returns the default monitoring port: service port + 10.
func DefaultMonitoringPort(servicePort int) int {
	if servicePort > 0 {
		return servicePort + 10
	}
	return 0
}

// DefaultOrdererPort returns the default service port for an orderer component.
func DefaultOrdererPort(ordererType string) int {
	switch ordererType {
	case "router":
		return 7050
	case "batcher":
		return 7051
	case "consensus":
		return 7052
	case "assembler":
		return 7053
	default:
		return 0
	}
}

// CommitterComponentDirName returns the local-deployment directory for a
// committer component. A single component of a type keeps the Ansible-style
// committer-<type> directory. Multiple instances of the same type need unique
// local directories because config-builder renders them into one output tree.
func (c *NetworkConfig) CommitterComponentDirName(component CommitterNode) string {
	if c == nil || c.Committer == nil || component.Type == "" {
		return "committer-" + component.Type
	}
	count := 0
	for _, candidate := range c.Committer.Components {
		if candidate.Type == component.Type {
			count++
		}
	}
	if count <= 1 || component.Name == "" {
		return "committer-" + component.Type
	}
	return "committer-" + component.Name
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
			PostgresImage: "docker.io/library/postgres:16.4",
		},
	}
}
