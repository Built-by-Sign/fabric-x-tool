package template

import (
	"fmt"
	"text/template"
)

// EndpointConfig holds endpoint configuration (host:port)
type EndpointConfig struct {
	Host string
	Port int
}

// CommitterTemplateData holds data for committer component configuration templates
type CommitterTemplateData struct {
	ComponentType      string
	ComponentName      string
	ConfigDir          string
	Host               string
	Port               int
	Database           *DatabaseConfig
	ChannelID          string
	CommitterHost      string
	CommitterPort      int
	GenesisBlockPath   string
	VerifierEndpoints  []EndpointConfig // For coordinator: list of verifier endpoints
	ValidatorEndpoints []EndpointConfig // For coordinator: list of validator endpoints
	AssemblerEndpoints []EndpointConfig // For sidecar: list of orderer assembler endpoints
}

// DatabaseConfig holds database configuration
type DatabaseConfig struct {
	Type     string
	Host     string
	Port     int
	User     string
	Password string
	DBName   string
}

// getCommitterTemplate returns the template for a specific committer component type
func (e *Engine) getCommitterTemplate(componentType string) (*template.Template, error) {
	var tmplStr string

	switch componentType {
	case "validator":
		tmplStr = e.getValidatorTemplate()
	case "verifier":
		tmplStr = e.getVerifierTemplate()
	case "coordinator":
		tmplStr = e.getCoordinatorTemplate()
	case "sidecar":
		tmplStr = e.getSidecarTemplate()
	case "query-service":
		tmplStr = e.getQueryServiceTemplate()
	case "db":
		tmplStr = e.getDatabaseTemplate()
	default:
		return nil, fmt.Errorf("unknown committer component type: %s", componentType)
	}

	tmpl := template.New("committer")
	return tmpl.Parse(tmplStr)
}

// getValidatorTemplate returns the validator configuration template
func (e *Engine) getValidatorTemplate() string {
	return `#
# Copyright IBM Corp. All Rights Reserved.
#
# SPDX-License-Identifier: Apache-2.0
#

server:
  endpoint: 0.0.0.0:{{ .Port }}
{{ if .Database }}
database:
  endpoints:
    - {{ .Database.Host }}:{{ .Database.Port }}
  # The username for the database
  username: {{ .Database.User }}
  # The password for the database
  password: {{ .Database.Password }}
  # The database name
  database: {{ .Database.DBName }}
  # The maximum size of the connection pool
  max-connections: 10
  # The minimum size of the connection pool
  min-connections: 5
  # Should be enabled for DB cluster
  load-balance: false
  # The exponential backoff retry strategy for database operation.
  retry:
    # This strategy increases the delay between retry attempts exponentially.
    # When using YugabyteDB as the backend, it is needed to handle retryable errors.
    # https://support.yugabyte.com/hc/en-us/articles/4409627048461-How-to-Troubleshoot-Database-Transaction-Retryable-Errors

    # initial-interval: Specifies the duration of the first backoff interval.
    # This is the time to wait before the first retry attempt.
    # Format: string representing a duration (e.g., "500ms", "1s", "2.5s").
    initial-interval: 500ms

    # randomization-factor: Controls the amount of randomness (jitter) applied to each backoff interval.
    # The actual backoff duration for an attempt will be randomly selected from the range:
    # [current_interval * (1 - randomization_factor), current_interval * (1 + randomization_factor)]
    # A factor of 0 means no randomization. A factor of 0.5 means the actual interval
    # can vary by +/- 50% of the calculated interval.
    # Must be between 0 and 1.
    randomization-factor: 0.5

    # multiplier: The factor by which the backoff interval increases after each failed attempt.
    # The next interval (before randomization) is calculated as: current_interval * multiplier.
    # A value of 1.5 means each subsequent interval will be 50% longer than the previous one.
    # Must be >= 1.
    multiplier: 1.5

    # max-interval: Sets the absolute maximum duration for any single backoff interval.
    # Even if the calculated interval (initial_interval * multiplier^n) exceeds this value,
    # the interval used (before randomization) will be capped at max-interval.
    # Format: string representing a duration (e.g., "60s", "1m", "5m").
    max-interval: 60s

    # max-elapsed-time: The maximum total time allowed for retries since the operation first began.
    # If the total time spent (including execution time of attempts and backoff waits)
    # exceeds this duration, the retry mechanism will stop, even if other limits haven't been reached.
    # Setting this to "0" means there is no time limit, and retries will continue
    # indefinitely until successful.
    # Format: string representing a duration (e.g., "15m", "1h").
    max-elapsed-time: 15m
{{ end }}
monitoring:
  server:
    endpoint: 0.0.0.0:2120
logging:
  enabled: true
  development: false
  level: INFO
  name: validator
`
}

// getVerifierTemplate returns the verifier configuration template
func (e *Engine) getVerifierTemplate() string {
	return `#
# Copyright IBM Corp. All Rights Reserved.
#
# SPDX-License-Identifier: Apache-2.0
#

server:
  endpoint: 0.0.0.0:{{ .Port }}
parallel-executor:
  batch-size-cutoff: 500
  batch-time-cutoff: 2ms
  channel-buffer-size: 1000
  parallelism: 80
monitoring:
  server:
    endpoint: 0.0.0.0:2130
logging:
  enabled: true
  development: false
  level: INFO
  name: verifier
`
}

// getCoordinatorTemplate returns the coordinator configuration template
func (e *Engine) getCoordinatorTemplate() string {
	return `#
# Copyright IBM Corp. All Rights Reserved.
#
# SPDX-License-Identifier: Apache-2.0
#

server:
  endpoint: 0.0.0.0:{{ .Port }}
verifier:
  endpoints:
{{- range .VerifierEndpoints }}
    - {{ .Host }}:{{ .Port }}
{{- end }}
validator-committer:
  endpoints:
{{- range .ValidatorEndpoints }}
    - {{ .Host }}:{{ .Port }}
{{- end }}
dependency-graph:
  num-of-local-dep-constructors: 4
  waiting-txs-limit: 20000000
  num-of-workers-for-global-dep-manager: 1
per-channel-buffer-size-per-goroutine: 10
monitoring:
  server:
    endpoint: 0.0.0.0:2140
logging:
  enabled: true
  development: false
  level: INFO
  name: coordinator
`
}

// getSidecarTemplate returns the sidecar configuration template
func (e *Engine) getSidecarTemplate() string {
	return `#
# Copyright IBM Corp. All Rights Reserved.
#
# SPDX-License-Identifier: Apache-2.0
#

server:
  endpoint: 0.0.0.0:{{ .Port }}
  keep-alive:
    params:
      time: 300s
      timeout: 600s
    enforcement-policy:
      min-time: 60s
      permit-without-stream: false
orderer:
  channel-id: {{ .ChannelID }}
  consensus-type: BFT
  connection:
    endpoints:
{{- range .AssemblerEndpoints }}
      - {{ .Host }}:{{ .Port }}
{{- end }}
committer:
  endpoint:
    host: {{ .CommitterHost }}
    port: {{ .CommitterPort }}
ledger:
  path: {{ .ConfigDir }}/ledger
last-committed-block-set-interval: 5s
bootstrap:
  genesis-block-file-path: {{ .GenesisBlockPath }}
monitoring:
  server:
    endpoint: 0.0.0.0:2150
logging:
  enabled: true
  development: false
  level: INFO
  name: sidecar
`
}

// getQueryServiceTemplate returns the query service configuration template
func (e *Engine) getQueryServiceTemplate() string {
	return `#
# Copyright IBM Corp. All Rights Reserved.
#
# SPDX-License-Identifier: Apache-2.0
#

server:
  # The server's endpoint configuration
  endpoint: 0.0.0.0:{{ .Port }}

# Resource limit configurations
# A batch will execute once it accumulated this number of keys.
min-batch-keys: 1024
# A batch will execute once it waited this much time.
max-batch-wait: 100ms
# A new view will be created if the previous view was created before this much time.
view-aggregation-window: 100ms
# A new view will be created if the previous view aggregated this number of views.
max-aggregated-views: 1024
# A view will be closed if it was opened for longer than this time.
max-view-timeout: 10s
{{ if .Database }}
database:
  endpoints:
    - {{ .Database.Host }}:{{ .Database.Port }}
  # The username for the database
  username: {{ .Database.User }}
  # The password for the database
  password: {{ .Database.Password }}
  # The database name
  database: {{ .Database.DBName }}
  # The maximum size of the connection pool
  max-connections: 10
  # The minimum size of the connection pool
  min-connections: 5
  # Should be enabled for DB cluster
  load-balance: false
  # The exponential backoff retry strategy for database operation.
  retry:
    # This strategy increases the delay between retry attempts exponentially.
    # When using YugabyteDB as the backend, it is needed to handle retryable errors.
    # https://support.yugabyte.com/hc/en-us/articles/4409627048461-How-to-Troubleshoot-Database-Transaction-Retryable-Errors

    # initial-interval: Specifies the duration of the first backoff interval.
    # This is the time to wait before the first retry attempt.
    # Format: string representing a duration (e.g., "500ms", "1s", "2.5s").
    initial-interval: 500ms

    # randomization-factor: Controls the amount of randomness (jitter) applied to each backoff interval.
    # The actual backoff duration for an attempt will be randomly selected from the range:
    # [current_interval * (1 - randomization_factor), current_interval * (1 + randomization_factor)]
    # A factor of 0 means no randomization. A factor of 0.5 means the actual interval
    # can vary by +/- 50% of the calculated interval.
    # Must be between 0 and 1.
    randomization-factor: 0.5

    # multiplier: The factor by which the backoff interval increases after each failed attempt.
    # The next interval (before randomization) is calculated as: current_interval * multiplier.
    # A value of 1.5 means each subsequent interval will be 50% longer than the previous one.
    # Must be >= 1.
    multiplier: 1.5

    # max-interval: Sets the absolute maximum duration for any single backoff interval.
    # Even if the calculated interval (initial_interval * multiplier^n) exceeds this value,
    # the interval used (before randomization) will be capped at max-interval.
    # Format: string representing a duration (e.g., "60s", "1m", "5m").
    max-interval: 60s

    # max-elapsed-time: The maximum total time allowed for retries since the operation first began.
    # If the total time spent (including execution time of attempts and backoff waits)
    # exceeds this duration, the retry mechanism will stop, even if other limits haven't been reached.
    # Setting this to "0" means there is no time limit, and retries will continue
    # indefinitely until successful.
    # Format: string representing a duration (e.g., "15m", "1h").
    max-elapsed-time: 15m
{{ end }}
monitoring:
  server:
    endpoint: 0.0.0.0:2160
logging:
  enabled: true
  development: false
  level: INFO
  name: query-service
`
}

// getDatabaseTemplate returns the database configuration template
// Note: Database component typically doesn't need a config file, but we'll create a placeholder
func (e *Engine) getDatabaseTemplate() string {
	return `#
# Copyright IBM Corp. All Rights Reserved.
#
# SPDX-License-Identifier: Apache-2.0
#

# Database component configuration
# This is a placeholder - database components typically don't require a config file
{{ if .Database }}
database:
  type: {{ .Database.Type }}
  host: {{ .Database.Host }}
  port: {{ .Database.Port }}
  user: {{ .Database.User }}
  password: {{ .Database.Password }}
  database: {{ .Database.DBName }}
{{ end }}
`
}
