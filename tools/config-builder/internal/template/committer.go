package template

import (
	"fmt"
	"text/template"

	"config-builder/internal/bccsp"
	templatefiles "config-builder/templates"
)

// EndpointConfig holds endpoint configuration (host:port)
type EndpointConfig struct {
	Host string
	Port int
}

// CommitterTemplateData holds data for committer component configuration templates
type CommitterTemplateData struct {
	ComponentType        string
	ComponentName        string
	ConfigDir            string
	Host                 string
	Port                 int
	MonitoringPort       int
	Database             *DatabaseConfig
	ChannelID            string
	CommitterHost        string
	CommitterPort        int
	GenesisBlockPath     string
	VerifierEndpoints    []EndpointConfig // For coordinator: list of verifier endpoints
	ValidatorEndpoints   []EndpointConfig // For coordinator: list of validator endpoints
	OrdererOrganizations []OrdererOrganizationConfig
	VerifierParallelism  int
	ServerTLS            *CommitterTLSConfig
	MonitoringTLS        *CommitterTLSConfig
	VerifierTLS          *CommitterTLSConfig
	ValidatorTLS         *CommitterTLSConfig
	OrdererTLS           *CommitterTLSConfig
	CommitterTLS         *CommitterTLSConfig

	// For sidecar v0.1.9+: MSP identity required for orderer deliver authorization.
	// Signer returns nil when MSPID or MSPDir is empty, causing orderer to reject
	// the unsigned deliver request with FORBIDDEN.
	SidecarIdentityMSPID  string
	SidecarIdentityMSPDir string
	SidecarIdentityBCCSP  *bccsp.BCCSPConfig
}

// OrdererOrganizationConfig holds sidecar orderer connection material.
type OrdererOrganizationConfig struct {
	Name        string
	Endpoints   []string
	CACertPaths []string
}

// DatabaseConfig holds database configuration
type DatabaseConfig struct {
	Type                 string
	Endpoints            []string
	User                 string
	Password             string
	DBName               string
	LoadBalance          bool
	TablePreSplitTablets int
	TLS                  *DatabaseTLSConfig
}

// DatabaseTLSConfig holds database client TLS configuration.
type DatabaseTLSConfig struct {
	Mode       string
	CACertPath string
}

// CommitterTLSConfig holds TLS configuration for committer server and client sections.
type CommitterTLSConfig struct {
	Mode              string
	KeyPath           string
	CertPath          string
	CACertPaths       []string
	CommonCACertPaths []string
}

// getCommitterTemplate returns the template for a specific committer component type
func (e *Engine) getCommitterTemplate(componentType string) (*template.Template, error) {
	var mainPath string

	switch componentType {
	case "validator":
		mainPath = "committer/config-validator.yaml.tmpl"
	case "verifier":
		mainPath = "committer/config-verifier.yaml.tmpl"
	case "coordinator":
		mainPath = "committer/config-coordinator.yaml.tmpl"
	case "sidecar":
		mainPath = "committer/config-sidecar.yaml.tmpl"
	case "query-service":
		mainPath = "committer/config-query-service.yaml.tmpl"
	case "db":
		mainPath = "committer/config-db.yaml.tmpl"
	default:
		return nil, fmt.Errorf("unknown committer component type: %s", componentType)
	}

	return templatefiles.Parse(mainPath, nil, "committer/sections/*.tmpl")
}
