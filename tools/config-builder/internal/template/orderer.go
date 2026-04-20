package template

import (
	"fmt"
	"text/template"

	"config-builder/internal/bccsp"
	templatefiles "config-builder/templates"
)

// OrdererTemplateData holds data for orderer node configuration templates
type OrdererTemplateData struct {
	PartyID                 int
	OrdererType             string
	ShardID                 int
	ConfigDir               string
	CryptoDir               string
	GenesisDir              string
	ListenAddress           string
	ListenPort              int
	MonitoringListenAddress string
	MonitoringListenPort    int
	MSPID                   string
	ChannelID               string
	BCCSP                   *bccsp.BCCSPConfig // Use BCCSP config instead of HSM
	TLS                     TLSConfig
}

// TLSConfig holds TLS configuration
type TLSConfig struct {
	Enabled            bool
	ClientAuthRequired bool
	PrivateKey         string
	Certificate        string
	RootCAs            []string
}

// getOrdererTemplate returns the template for a specific orderer type
func (e *Engine) getOrdererTemplate(ordererType string) (*template.Template, error) {
	var mainPath string

	switch ordererType {
	case "router":
		mainPath = "orderer/config-router.yaml.tmpl"
	case "batcher":
		mainPath = "orderer/config-batcher.yaml.tmpl"
	case "consensus":
		mainPath = "orderer/config-consensus.yaml.tmpl"
	case "assembler":
		mainPath = "orderer/config-assembler.yaml.tmpl"
	default:
		return nil, fmt.Errorf("unknown orderer type: %s", ordererType)
	}

	return templatefiles.Parse(mainPath, template.FuncMap{
		"boolToLower": func(b bool) string {
			if b {
				return "true"
			}
			return "false"
		},
	}, "orderer/sections/*.tmpl")
}
