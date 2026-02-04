package template

import (
	"fmt"
	"strings"
	"text/template"

	"golang.org/x/text/cases"
	"golang.org/x/text/language"

	"github.com/ethsign/cbdc-chain/cbdc-network/config-builder/internal/bccsp"
)

// OrdererTemplateData holds data for orderer node configuration templates
type OrdererTemplateData struct {
	PartyID       int
	OrdererType   string
	ShardID       int
	ConfigDir     string
	CryptoDir     string
	GenesisDir    string
	ListenAddress string
	ListenPort    int
	MSPID         string
	ChannelID     string
	BCCSP         *bccsp.BCCSPConfig // Use BCCSP config instead of HSM
	TLS           TLSConfig
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
	var tmplStr string
	var err error

	switch ordererType {
	case "router":
		tmplStr, err = e.getRouterTemplate()
	case "batcher":
		tmplStr, err = e.getBatcherTemplate()
	case "consenter":
		tmplStr, err = e.getConsenterTemplate()
	case "assembler":
		tmplStr, err = e.getAssemblerTemplate()
	default:
		return nil, fmt.Errorf("unknown orderer type: %s", ordererType)
	}

	if err != nil {
		return nil, err
	}

	// Create template with custom functions
	caser := cases.Title(language.English)
	tmpl := template.New("orderer").Funcs(template.FuncMap{
		"lower": strings.ToLower,
		"title": caser.String,
		"boolToLower": func(b bool) string {
			return strings.ToLower(fmt.Sprintf("%v", b))
		},
	})

	// Parse general and filestore templates first
	if _, err := tmpl.Parse(e.getGeneralTemplate()); err != nil {
		return nil, fmt.Errorf("failed to parse general template: %w", err)
	}
	if _, err := tmpl.Parse(e.getFilestoreTemplate()); err != nil {
		return nil, fmt.Errorf("failed to parse filestore template: %w", err)
	}

	// Parse the main template
	if _, err := tmpl.Parse(tmplStr); err != nil {
		return nil, fmt.Errorf("failed to parse orderer template: %w", err)
	}

	return tmpl, nil
}

// getRouterTemplate returns the router configuration template
func (e *Engine) getRouterTemplate() (string, error) {
	return `#
# Copyright IBM Corp. All Rights Reserved.
#
# SPDX-License-Identifier: Apache-2.0
#

PartyID: {{ .PartyID }}
Router:
    NumberOfConnectionsPerBatcher: 10
    NumberOfStreamsPerConnection: 5
{{ template "general" . }}
{{ template "filestore" . }}
`, nil
}

// getBatcherTemplate returns the batcher configuration template
func (e *Engine) getBatcherTemplate() (string, error) {
	return `#
# Copyright IBM Corp. All Rights Reserved.
#
# SPDX-License-Identifier: Apache-2.0
#

PartyID: {{ .PartyID }}
Batcher:
    ShardID: {{ .ShardID }}
    BatchSequenceGap: 10
    MemPoolMaxSize: 1000000
    SubmitTimeout: 500ms
{{ template "general" . }}
{{ template "filestore" . }}
`, nil
}

// getConsenterTemplate returns the consenter configuration template
func (e *Engine) getConsenterTemplate() (string, error) {
	return `#
# Copyright IBM Corp. All Rights Reserved.
#
# SPDX-License-Identifier: Apache-2.0
#

PartyID: {{ .PartyID }}
Consensus:
    WALDir: {{ .ConfigDir }}/wal
{{ template "general" . }}
{{ template "filestore" . }}
`, nil
}

// getAssemblerTemplate returns the assembler configuration template
func (e *Engine) getAssemblerTemplate() (string, error) {
	return `#
# Copyright IBM Corp. All Rights Reserved.
#
# SPDX-License-Identifier: Apache-2.0
#

PartyID: {{ .PartyID }}
Assembler:
    PrefetchBufferMemoryBytes: 1073741824
    RestartLedgerScanTimeout: 5s
    PrefetchEvictionTtl: 1h0m0s
    ReplicationChannelSize: 100
    BatchRequestsChannelSize: 1000
{{ template "general" . }}
{{ template "filestore" . }}
`, nil
}

// getGeneralTemplate returns the general section template
func (e *Engine) getGeneralTemplate() string {
	return `{{ define "general" }}
General:
    ListenAddress: {{ .ListenAddress }}
    ListenPort: {{ .ListenPort }}
    TLS:
        Enabled: {{ .TLS.Enabled | boolToLower }}
        ClientAuthRequired: {{ .TLS.ClientAuthRequired | boolToLower }}
        PrivateKey: {{ .TLS.PrivateKey }}
        Certificate: {{ .TLS.Certificate }}
        RootCAs:
{{- range .TLS.RootCAs }}
            - {{ . }}
{{- end }}
    Keepalive:
        ClientInterval: 1m0s
        ClientTimeout: 20s
        ServerInterval: 2h0m0s
        ServerTimeout: 20s
        ServerMinInterval: 1m0s
    Backoff:
        BaseDelay: 1s
        Multiplier: 1.6
        MaxDelay: 2m0s
    MaxRecvMsgSize: 104857600
    MaxSendMsgSize: 104857600
    Bootstrap:
        Method: block
        File: {{ .ConfigDir }}/genesis.block
    Cluster:
        SendBufferSize: 2000
        ClientCertificate: {{ .TLS.Certificate }}
        ClientPrivateKey: {{ .TLS.PrivateKey }}
    LocalMSPDir: {{ .ConfigDir }}/msp
    LocalMSPID: {{ .MSPID }}
    LogSpec: info
{{- if .BCCSP }}
    # BCCSP configuration
    BCCSP:
        Default: {{ .BCCSP.Default }}
{{- if .BCCSP.PKCS11 }}
        PKCS11:
            Library: {{ .BCCSP.PKCS11.Library }}
            Pin: "{{ .BCCSP.PKCS11.Pin }}"
            Label: "{{ .BCCSP.PKCS11.Label }}"
            Hash: {{ .BCCSP.PKCS11.Hash }}
            Security: {{ .BCCSP.PKCS11.Security }}
{{- end }}
{{- if .BCCSP.SW }}
        SW:
            Hash: {{ .BCCSP.SW.Hash }}
            Security: {{ .BCCSP.SW.Security }}
{{- end }}
{{- end }}
{{ end }}
`
}

// getFilestoreTemplate returns the filestore section template
func (e *Engine) getFilestoreTemplate() string {
	return `{{ define "filestore" }}
FileStore:
    Location: {{ .ConfigDir }}/store
{{ end }}
`
}
