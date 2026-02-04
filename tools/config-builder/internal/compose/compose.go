package compose

import (
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/ethsign/cbdc-chain/cbdc-network/config-builder/internal/config"
	"gopkg.in/yaml.v3"
)

// Generator generates docker-compose.yaml files
type Generator struct {
	config    *config.NetworkConfig
	outputDir string
	verbose   bool
}

// NewGenerator creates a new docker-compose generator
func NewGenerator(cfg *config.NetworkConfig, outputDir string, verbose bool) *Generator {
	return &Generator{
		config:    cfg,
		outputDir: outputDir,
		verbose:   verbose,
	}
}

// Generate creates a docker-compose.yaml file for the network
func (g *Generator) Generate() error {
	absOutputDir, err := filepath.Abs(g.outputDir)
	if err != nil {
		return fmt.Errorf("failed to get absolute output path: %w", err)
	}

	composePath := filepath.Join(absOutputDir, "docker-compose.yaml")

	// Build compose structure
	compose := g.buildCompose(absOutputDir)

	// Marshal to YAML
	data, err := yaml.Marshal(compose)
	if err != nil {
		return fmt.Errorf("failed to marshal docker-compose: %w", err)
	}

	// Write to file
	if err := os.WriteFile(composePath, data, 0644); err != nil {
		return fmt.Errorf("failed to write docker-compose: %w", err)
	}

	g.log("Generated docker-compose.yaml at: %s", composePath)
	return nil
}

// Compose represents the docker-compose.yaml structure
// Note: version field is deprecated in Docker Compose v2, but we keep it for compatibility
type Compose struct {
	Name     string             `yaml:"name"`
	Services map[string]Service `yaml:"services"`
	Networks map[string]Network `yaml:"networks,omitempty"`
	Volumes  map[string]Volume  `yaml:"volumes,omitempty"`
}

// HealthCheck represents a docker healthcheck configuration
type HealthCheck struct {
	Test        []string `yaml:"test,omitempty"`
	Interval    string   `yaml:"interval,omitempty"`
	Timeout     string   `yaml:"timeout,omitempty"`
	Retries     int      `yaml:"retries,omitempty"`
	StartPeriod string   `yaml:"start_period,omitempty"`
}

// DependsOnCondition represents a depends_on condition
type DependsOnCondition struct {
	Condition string `yaml:"condition,omitempty"`
}

// Service represents a docker-compose service
type Service struct {
	Image         string                        `yaml:"image"`
	ContainerName string                        `yaml:"container_name,omitempty"`
	Hostname      string                        `yaml:"hostname,omitempty"`
	Command       interface{}                   `yaml:"command,omitempty"`
	Environment   []string                      `yaml:"environment,omitempty"`
	Volumes       []string                      `yaml:"volumes,omitempty"`
	Ports         []string                      `yaml:"ports,omitempty"`
	Networks      []string                      `yaml:"networks,omitempty"`
	DependsOn     map[string]DependsOnCondition `yaml:"depends_on,omitempty"`
	Labels        map[string]string             `yaml:"labels,omitempty"`
	User          string                        `yaml:"user,omitempty"`
	WorkingDir    string                        `yaml:"working_dir,omitempty"`
	HealthCheck   *HealthCheck                  `yaml:"healthcheck,omitempty"`
	ExtraHosts    []string                      `yaml:"extra_hosts,omitempty"`
}

// Network represents a docker network
type Network struct {
	Driver   string `yaml:"driver,omitempty"`
	External bool   `yaml:"external,omitempty"`
	Name     string `yaml:"name,omitempty"`
}

// Volume represents a docker volume
type Volume struct {
	Driver string `yaml:"driver,omitempty"`
}

// buildCompose constructs the docker-compose structure
func (g *Generator) buildCompose(outputDir string) *Compose {
	compose := &Compose{
		Name:     g.config.Docker.Name,
		Services: make(map[string]Service),
		Networks: map[string]Network{
			g.config.Docker.Network: {
				Driver:   g.config.Docker.NetworkDriver,
				External: g.config.Docker.NetworkExternal, // Use external network created by Makefile
				Name:     g.config.Docker.Network,         // Explicit name to avoid prefix
			},
		},
		// Volumes: map[string]Volume{
		// 	"committer-db-data": {
		// 		Driver: "local",
		// 	},
		// },
	}

	// Track component indices per type
	componentIndices := make(map[string]int) // type -> index
	// Track orderer services by type for dependency setup
	ordererServicesByType := make(map[string][]string) // type -> []serviceName

	// Add orderer services
	for _, org := range g.config.OrdererOrgs {
		for _, orderer := range org.Orderers {
			componentType := orderer.Type
			if componentIndices[componentType] == 0 {
				componentIndices[componentType] = 1
			} else {
				componentIndices[componentType]++
			}
			componentIndex := componentIndices[componentType]

			serviceName := fmt.Sprintf("orderer-%s-%d", componentType, componentIndex)
			service := g.buildOrdererService(serviceName, &org, &orderer, outputDir)
			compose.Services[serviceName] = service

			// Track service by type
			if _, exists := ordererServicesByType[componentType]; !exists {
				ordererServicesByType[componentType] = []string{}
			}
			ordererServicesByType[componentType] = append(ordererServicesByType[componentType], serviceName)
		}
	}

	// Add dependencies for orderer services based on startup order:
	// consenter → batcher → assembler → router
	// Each type depends on the previous type being started
	ordererTypeOrder := []string{"consenter", "batcher", "assembler", "router"}
	for i := 1; i < len(ordererTypeOrder); i++ {
		currentType := ordererTypeOrder[i]
		previousType := ordererTypeOrder[i-1]

		// All services of current type depend on all services of previous type
		if currentServices, ok := ordererServicesByType[currentType]; ok {
			if previousServices, ok := ordererServicesByType[previousType]; ok {
				for _, currentService := range currentServices {
					for _, previousService := range previousServices {
						// Use service_started condition since orderer components don't have healthchecks
						compose.Services[currentService].DependsOn[previousService] = DependsOnCondition{
							Condition: "service_started",
						}
					}
				}
			}
		}
	}

	// Add committer services
	if g.config.Committer != nil {
		for _, component := range g.config.Committer.Components {
			serviceName := component.Name
			service := g.buildCommitterService(serviceName, &component, outputDir)
			compose.Services[serviceName] = service
		}
	}

	return compose
}

// getCurrentUserUIDGID returns the current user's UID:GID string for container user setting
// Ansible uses: container_user: "{{ ansible_facts.user_uid ~ ':' ~ ansible_facts.user_gid if container_run_as_host_user else ” }}"
func getCurrentUserUIDGID() string {
	currentUser, err := user.Current()
	if err != nil {
		// If we can't get current user, return empty string (container will run as default user)
		return ""
	}

	uid := currentUser.Uid
	gid := currentUser.Gid

	// On some systems, Gid might be empty, try to get primary group
	if gid == "" {
		groups, err := currentUser.GroupIds()
		if err == nil && len(groups) > 0 {
			gid = groups[0]
		}
	}

	// Validate UID and GID are numeric
	if _, err := strconv.Atoi(uid); err != nil {
		return ""
	}
	if _, err := strconv.Atoi(gid); err != nil {
		return ""
	}

	return fmt.Sprintf("%s:%s", uid, gid)
}

// normalizePathForDockerCompose converts paths to be suitable for docker-compose volume mounts.
// When running inside a container (e.g., cbdc-tool), paths like /workspace/out/... need to be
// converted to relative paths ./local-deployment/... so they work correctly when docker-compose runs on the host.
// When running on the host, absolute paths need to be converted to relative paths based on the
// docker-compose.yaml location (which is in the out/ directory).
// This function:
// - Converts /workspace/out/ prefix to ./ (container path to relative path, docker-compose.yaml is in out/)
// - Converts absolute paths containing /out/local-deployment/ to ./local-deployment/
// - Ensures other relative paths start with ./
func normalizePathForDockerCompose(path string) string {
	// Remove /workspace/out prefix and convert to relative path (for container execution)
	// Since docker-compose.yaml is in the out/ directory, we need to remove both /workspace and /out
	if strings.HasPrefix(path, "/workspace/out/") {
		return "." + strings.TrimPrefix(path, "/workspace/out")
	}

	// Also handle /workspace/ prefix without /out (legacy support)
	if strings.HasPrefix(path, "/workspace/") {
		remainder := strings.TrimPrefix(path, "/workspace/")
		// If it starts with out/, remove that too since docker-compose.yaml is in out/
		if strings.HasPrefix(remainder, "out/") {
			return "./" + strings.TrimPrefix(remainder, "out/")
		}
		return "./" + remainder
	}

	// For absolute paths, try to convert to relative path based on common patterns
	if filepath.IsAbs(path) {
		// Look for /out/local-deployment/ pattern and convert to relative path
		// Since docker-compose.yaml is in out/, we need to extract only the part after out/
		if idx := strings.Index(path, "/out/local-deployment/"); idx >= 0 {
			// Extract the part after /out/ (skip both the leading / and out/)
			relativePart := path[idx+5:] // Skip "/out/" (5 characters)
			return "./" + relativePart
		}
		// If no recognizable pattern, keep absolute path (fallback for safety)
		return path
	}

	// For relative paths starting with out/, remove the out/ prefix
	// since docker-compose.yaml is already in the out/ directory
	if strings.HasPrefix(path, "out/local-deployment/") {
		return "./" + strings.TrimPrefix(path, "out/")
	}

	// For relative paths, ensure they start with ./
	if !strings.HasPrefix(path, "./") && !strings.HasPrefix(path, "../") {
		return "./" + path
	}
	return path
}

// buildOrdererService builds a service definition for an orderer component
func (g *Generator) buildOrdererService(serviceName string, org *config.OrdererOrg, orderer *config.Node, outputDir string) Service {
	configDir := filepath.Join(outputDir, "local-deployment", serviceName, "config")

	service := Service{
		Image:         g.config.Docker.OrdererImage,
		ContainerName: serviceName,
		Hostname:      serviceName,
		Networks:      []string{g.config.Docker.Network},
		Volumes:       []string{},
		Environment:   []string{},
		WorkingDir:    "/config",
		DependsOn:     make(map[string]DependsOnCondition),
	}

	// Add extra_hosts for Linux/WSL2 to make host.docker.internal work
	// On Linux, Docker doesn't automatically add host.docker.internal, so we use host-gateway
	if runtime.GOOS == "linux" {
		service.ExtraHosts = []string{"host.docker.internal:host-gateway"}
	}

	// Set user to run as host user (matches Ansible's container_run_as_host_user: true)
	// Ansible uses: container_user: "{{ ansible_facts.user_uid ~ ':' ~ ansible_facts.user_gid if container_run_as_host_user else '' }}"
	if userUIDGID := getCurrentUserUIDGID(); userUIDGID != "" {
		service.User = userUIDGID
	}

	// Note: Ansible does not use Docker healthchecks for orderer components
	// It uses ansible.builtin.wait_for from the host to check ports instead
	// So we skip healthcheck configuration here to match Ansible behavior

	// Mount config directory
	// Normalize path for docker-compose (convert /workspace/ to ./ for container-generated configs)
	service.Volumes = append(service.Volumes, fmt.Sprintf("%s:/config", normalizePathForDockerCompose(configDir)))

	// Add KMS environment variables if KMS is enabled
	if g.config.KMS != nil && g.config.KMS.Enabled {
		kmsEndpoint := g.config.KMS.Endpoint
		tokenLabel := g.config.KMS.TokenLabel
		service.Environment = append(service.Environment,
			fmt.Sprintf("SIGN_KMS_ENDPOINT=%s", kmsEndpoint),
			fmt.Sprintf("KMS_TOKEN_LABEL=%s", tokenLabel),
		)
	}

	// Add port mapping
	if orderer.Port > 0 {
		service.Ports = append(service.Ports, fmt.Sprintf("%d:%d", orderer.Port, orderer.Port))
	}

	// Set command based on orderer type
	// Ansible uses different commands for different types:
	// - router: "router --config=..."
	// - batcher: "batcher --config=..."
	// - consenter: "consensus --config=..." (not "consenter")
	// - assembler: "assembler --config=..."
	commandType := orderer.Type
	if orderer.Type == "consenter" {
		commandType = "consensus" // Ansible uses "consensus" command, not "consenter"
	}
	service.Command = []string{
		commandType,
		"--config=/config/node_config.yaml",
	}

	return service
}

// buildCommitterService builds a service definition for a committer component
func (g *Generator) buildCommitterService(serviceName string, component *config.CommitterNode, outputDir string) Service {
	configDir := filepath.Join(outputDir, "local-deployment", serviceName, "config")
	configFile := fmt.Sprintf("config-%s.yml", component.Type)

	// Default committer image if not set (use a public tag if available)
	committerImage := g.config.Docker.CommitterImage
	if committerImage == "" {
		// Try to use a public image tag, fallback to local if needed
		committerImage = "hyperledger/fabric-x-committer:0.0.19"
	}

	service := Service{
		Image:         committerImage,
		ContainerName: serviceName,
		Hostname:      serviceName,
		Networks:      []string{g.config.Docker.Network},
		Volumes:       []string{},
		Environment:   []string{},
		WorkingDir:    "/config", // Default working dir, will be overridden for db type
		DependsOn:     make(map[string]DependsOnCondition),
	}

	// Add extra_hosts for Linux/WSL2 to make host.docker.internal work
	// On Linux, Docker doesn't automatically add host.docker.internal, so we use host-gateway
	if runtime.GOOS == "linux" {
		service.ExtraHosts = []string{"host.docker.internal:host-gateway"}
	}

	// Set user to run as host user (matches Ansible's container_run_as_host_user: true)
	// Ansible uses: container_user: "{{ ansible_facts.user_uid ~ ':' ~ ansible_facts.user_gid if container_run_as_host_user else '' }}"
	// Note: PostgreSQL container should run as postgres user, not host user
	if component.Type != "db" {
		if userUIDGID := getCurrentUserUIDGID(); userUIDGID != "" {
			service.User = userUIDGID
		}
	}

	// Mount config directory (skip for db type - db doesn't need config file)
	// Ansible does not generate config file for db component
	if component.Type != "db" {
		// Normalize path for docker-compose (convert /workspace/ to ./ for container-generated configs)
		service.Volumes = append(service.Volumes, fmt.Sprintf("%s:/config", normalizePathForDockerCompose(configDir)))
	}

	// Set command based on component type
	switch component.Type {
	case "db":
		// Database component (PostgreSQL)
		// Ansible mounts: {{ postgres_pgdata_dir }}:/var/lib/postgresql/data:Z
		// Ansible sets: PGDATA: /var/lib/postgresql/data/pgdata
		service.Image = "postgres:15"
		service.Environment = append(service.Environment,
			fmt.Sprintf("POSTGRES_USER=%s", component.PostgresUser),
			fmt.Sprintf("POSTGRES_PASSWORD=%s", component.PostgresPassword),
			fmt.Sprintf("POSTGRES_DB=%s", component.PostgresDB),
			"PGDATA=/var/lib/postgresql/data/pgdata", // Match Ansible configuration
		)
		// Database doesn't need a config file command
		service.Command = nil
		// PostgreSQL listens on port 5432 inside container by default
		// Map host port (from config) to container port 5432
		if component.Port > 0 {
			service.Ports = append(service.Ports, fmt.Sprintf("%d:5432", component.Port))
		}
		// Mount named volume for PostgreSQL data
		// Using a named volume instead of bind mount to avoid permission issues
		// Docker manages permissions automatically for named volumes
		// service.Volumes = []string{"committer-db-data:/var/lib/postgresql/data"}
		// PostgreSQL container should not have working_dir set (Ansible doesn't set it)
		service.WorkingDir = ""
		// Add healthcheck for PostgreSQL
		service.HealthCheck = &HealthCheck{
			Test:        []string{"CMD-SHELL", fmt.Sprintf("pg_isready -U %s -d %s", component.PostgresUser, component.PostgresDB)},
			Interval:    "10s",
			Timeout:     "5s",
			Retries:     5,
			StartPeriod: "10s",
		}
	case "validator":
		service.Command = []string{
			"committer",
			"start-vc",
			"--config", fmt.Sprintf("/config/%s", configFile),
		}
		// Note: Ansible does not use Docker healthchecks for committer components
		// It uses ansible.builtin.wait_for from the host to check ports instead
	case "verifier":
		service.Command = []string{
			"committer",
			"start-verifier",
			"--config", fmt.Sprintf("/config/%s", configFile),
		}
		// Note: Ansible does not use Docker healthchecks for committer components
	case "coordinator":
		service.Command = []string{
			"committer",
			"start-coordinator",
			"--config", fmt.Sprintf("/config/%s", configFile),
		}
		// Note: Ansible does not use Docker healthchecks for committer components
	case "sidecar":
		service.Command = []string{
			"committer",
			"start-sidecar",
			"--config", fmt.Sprintf("/config/%s", configFile),
		}
		// Note: Ansible does not use Docker healthchecks for committer components
	case "query-service":
		service.Command = []string{
			"committer",
			"start-query",
			"--config", fmt.Sprintf("/config/%s", configFile),
		}
		// Note: Ansible does not use Docker healthchecks for committer components
	}

	// Add KMS environment variables if KMS is enabled (for non-db components)
	if component.Type != "db" && g.config.KMS != nil && g.config.KMS.Enabled {
		kmsEndpoint := g.config.KMS.Endpoint
		if kmsEndpoint == "" {
			kmsEndpoint = "http://host.docker.internal:9200"
		}
		tokenLabel := g.config.KMS.TokenLabel
		if tokenLabel == "" {
			tokenLabel = "tk"
		}
		service.Environment = append(service.Environment,
			fmt.Sprintf("SIGN_KMS_ENDPOINT=%s", kmsEndpoint),
			fmt.Sprintf("KMS_TOKEN_LABEL=%s", tokenLabel),
		)
	}

	// Add port mapping for non-db components (db port mapping is handled in the switch above)
	if component.Type != "db" && component.Port > 0 {
		service.Ports = append(service.Ports, fmt.Sprintf("%d:%d", component.Port, component.Port))
	}

	// Add dependencies with conditions
	// Find DB name for dependency
	var dbName string
	var coordinatorName string
	for _, comp := range g.config.Committer.Components {
		if comp.Type == "db" {
			dbName = comp.Name
		}
		if comp.Type == "coordinator" {
			coordinatorName = comp.Name
		}
	}

	switch component.Type {
	case "sidecar":
		// Sidecar depends on coordinator being started
		// Use service_started because coordinator has no healthcheck
		if coordinatorName != "" {
			service.DependsOn[coordinatorName] = DependsOnCondition{Condition: "service_started"}
		}
	case "validator", "query-service":
		// Validator and query-service depend on database being healthy
		// Use service_healthy because database has healthcheck configured
		if dbName != "" {
			service.DependsOn[dbName] = DependsOnCondition{Condition: "service_healthy"}
		}
	case "verifier":
		// Verifier depends on validator being started
		// Use service_started because validator has no healthcheck
		for _, comp := range g.config.Committer.Components {
			if comp.Type == "validator" {
				service.DependsOn[comp.Name] = DependsOnCondition{Condition: "service_started"}
				break
			}
		}
	case "coordinator":
		// Coordinator depends on verifier being started
		// Use service_started because verifier has no healthcheck
		for _, comp := range g.config.Committer.Components {
			if comp.Type == "verifier" {
				service.DependsOn[comp.Name] = DependsOnCondition{Condition: "service_started"}
				break
			}
		}
	}

	return service
}

// log prints a message if verbose mode is enabled
func (g *Generator) log(format string, args ...interface{}) {
	if g.verbose {
		fmt.Printf("[compose] "+format+"\n", args...)
	}
}
