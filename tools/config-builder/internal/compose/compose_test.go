package compose

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"config-builder/internal/config"
)

func TestPrepareDefaultRuntimeDataDirs(t *testing.T) {
	t.Setenv("FABRIC_X_RUNTIME_DATA_ROOT", "")
	outputDir := t.TempDir()
	g := NewGenerator(&config.NetworkConfig{}, outputDir, false)
	compose := &Compose{Services: map[string]Service{
		"orderer": {Volumes: []string{runtimeDataMount("orderer-router-1", "/runtime")}},
		"db":      {Volumes: []string{runtimeDataMount("committer-db", "/var/lib/postgresql")}},
	}}

	if err := g.prepareDefaultRuntimeDataDirs(compose, outputDir); err != nil {
		t.Fatal(err)
	}
	for _, relative := range []string{"orderer-router-1", "committer-db"} {
		if info, err := os.Stat(filepath.Join(outputDir, "runtime-data", relative)); err != nil || !info.IsDir() {
			t.Fatalf("default runtime data directory %q was not created", relative)
		}
	}
}

func TestPrepareDefaultRuntimeDataDirsSkipsCustomRoot(t *testing.T) {
	t.Setenv("FABRIC_X_RUNTIME_DATA_ROOT", "/custom/runtime-data")
	outputDir := t.TempDir()
	g := NewGenerator(&config.NetworkConfig{}, outputDir, false)
	compose := &Compose{Services: map[string]Service{
		"orderer": {Volumes: []string{runtimeDataMount("orderer-router-1", "/runtime")}},
	}}

	if err := g.prepareDefaultRuntimeDataDirs(compose, outputDir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(outputDir, "runtime-data")); !os.IsNotExist(err) {
		t.Fatal("default runtime data root was created for a custom root")
	}
}

func TestBuildCommitterServiceDBMountsLocalDataDir(t *testing.T) {
	g := NewGenerator(&config.NetworkConfig{
		Docker: config.DockerConfig{
			Network:       "fx-net",
			NetworkDriver: "bridge",
			PostgresImage: "postgres:16",
		},
	}, t.TempDir(), false)

	service, err := g.buildCommitterService("committer-db", &config.CommitterNode{
		Name:             "committer-db",
		Type:             "db",
		Port:             15432,
		PostgresUser:     "postgres",
		PostgresPassword: "secret",
		PostgresDB:       "fxdb",
	}, t.TempDir())
	if err != nil {
		t.Fatalf("buildCommitterService returned error: %v", err)
	}

	if service.Image != "postgres:16" {
		t.Fatalf("unexpected image: %s", service.Image)
	}
	if len(service.Volumes) != 1 || !strings.HasSuffix(service.Volumes[0], ":/var/lib/postgresql") {
		t.Fatalf("expected db data volume mount, got %#v", service.Volumes)
	}
	if !strings.Contains(service.Volumes[0], "${FABRIC_X_RUNTIME_DATA_ROOT:-./runtime-data}/committer-db") {
		t.Fatalf("expected runtime data dir mount, got %#v", service.Volumes)
	}
	if service.WorkingDir != "" {
		t.Fatalf("db service should not set working_dir, got %q", service.WorkingDir)
	}
	if service.HealthCheck == nil {
		t.Fatal("db service should include healthcheck")
	}
}

func TestBuildCommitterServiceDBEnablesPostgresTLS(t *testing.T) {
	g := NewGenerator(&config.NetworkConfig{
		TLS: &config.TLSConfig{Enabled: true},
		Docker: config.DockerConfig{
			Network:       "fx-net",
			NetworkDriver: "bridge",
			PostgresImage: "postgres:16",
		},
	}, t.TempDir(), false)

	service, err := g.buildCommitterService("committer-db", &config.CommitterNode{
		Name:             "committer-db",
		Type:             "db",
		Port:             15432,
		PostgresUser:     "postgres",
		PostgresPassword: "secret",
		PostgresDB:       "fxdb",
	}, t.TempDir())
	if err != nil {
		t.Fatalf("buildCommitterService returned error: %v", err)
	}

	command, ok := service.Command.([]string)
	if !ok {
		t.Fatalf("expected command list, got %#v", service.Command)
	}
	joinedCommand := strings.Join(command, " ")
	for _, want := range []string{
		// Shell wrapper that copies the TLS key, sets ownership/mode, and execs postgres.
		// This keeps the host-side key world-accessible while satisfying Postgres's
		// strict requirements regardless of container UID mapping.
		"cp /var/lib/postgresql/config/tls/server.key /var/lib/postgresql/server.key",
		"chown postgres:postgres /var/lib/postgresql/server.key",
		"chmod 600 /var/lib/postgresql/server.key",
		"exec docker-entrypoint.sh postgres",
		"port=15432",
		"ssl=on",
		"ssl_key_file=/var/lib/postgresql/server.key",
		"ssl_cert_file=/var/lib/postgresql/config/tls/server.crt",
	} {
		if !strings.Contains(joinedCommand, want) {
			t.Fatalf("expected postgres TLS command to contain %q, got %#v", want, command)
		}
	}
	if !containsString(service.Ports, "15432:15432") {
		t.Fatalf("expected Ansible-style same-port mapping, got %#v", service.Ports)
	}
	if !containsVolumeSuffix(service.Volumes, "/var/lib/postgresql/config:ro") {
		t.Fatalf("expected postgres config volume, got %#v", service.Volumes)
	}
	if service.HealthCheck == nil || !strings.Contains(strings.Join(service.HealthCheck.Test, " "), "-p 15432") {
		t.Fatalf("expected healthcheck to use configured postgres port, got %#v", service.HealthCheck)
	}
}

func TestBuildCommitterServiceMountsAnsibleStyleConfigDir(t *testing.T) {
	g := NewGenerator(&config.NetworkConfig{
		Docker: config.DockerConfig{
			Network:        "fx-net",
			NetworkDriver:  "bridge",
			CommitterImage: "hyperledger/fabric-x-committer:0.0.19",
		},
		Committer: &config.CommitterConfig{
			Components: []config.CommitterNode{{Name: "validator", Type: "validator", Port: 5100}},
		},
	}, t.TempDir(), false)

	service, err := g.buildCommitterService("validator", &config.CommitterNode{
		Name: "validator",
		Type: "validator",
		Port: 5100,
	}, t.TempDir())
	if err != nil {
		t.Fatalf("buildCommitterService returned error: %v", err)
	}

	if len(service.Volumes) != 1 {
		t.Fatalf("expected one config volume, got %#v", service.Volumes)
	}
	if !strings.Contains(service.Volumes[0], "local-deployment/committer-validator/config:/config") {
		t.Fatalf("expected committer-validator config mount, got %#v", service.Volumes)
	}
}

func TestBuildCommitterServiceMountsUniqueDirForRepeatedType(t *testing.T) {
	cfg := &config.NetworkConfig{
		Docker: config.DockerConfig{
			Network:        "fx-net",
			NetworkDriver:  "bridge",
			CommitterImage: "hyperledger/fabric-x-committer:0.0.19",
		},
		Committer: &config.CommitterConfig{
			Components: []config.CommitterNode{
				{Name: "validator-a", Type: "validator", Port: 5100},
				{Name: "validator-b", Type: "validator", Port: 5200},
			},
		},
	}
	g := NewGenerator(cfg, t.TempDir(), false)

	service, err := g.buildCommitterService("validator-b", &cfg.Committer.Components[1], t.TempDir())
	if err != nil {
		t.Fatalf("buildCommitterService returned error: %v", err)
	}

	if len(service.Volumes) != 1 {
		t.Fatalf("expected one config volume, got %#v", service.Volumes)
	}
	if !strings.Contains(service.Volumes[0], "local-deployment/committer-validator-b/config:/config") {
		t.Fatalf("expected unique validator-b config mount, got %#v", service.Volumes)
	}
}

func TestBuildComposeExternalDatabaseAddsNoLocalDBDependency(t *testing.T) {
	g := NewGenerator(&config.NetworkConfig{
		Docker: config.DockerConfig{
			Name:           "fx-network",
			Network:        "fx-net",
			NetworkDriver:  "bridge",
			CommitterImage: "hyperledger/fabric-x-committer:0.0.19",
		},
		OrdererOrgs: []config.OrdererOrg{{
			Name:   "Orderer",
			Domain: "example.com",
			Orderers: []config.Node{{
				Name: "orderer0",
				Type: "router",
				Port: 7050,
			}},
		}},
		PeerOrgs: []config.PeerOrg{{Name: "Org1", Domain: "org1.example.com"}},
		Committer: &config.CommitterConfig{
			Database: &config.CommitterDatabase{
				Type: "yugabyte",
				Endpoints: []config.DatabaseEndpoint{
					{Host: "yb-1.example.com", Port: 5433},
					{Host: "yb-2.example.com", Port: 5433},
				},
				Username:    "fx",
				Password:    "secret",
				Database:    "fxdb",
				LoadBalance: boolPtr(true),
			},
			Components: []config.CommitterNode{
				{Name: "validator", Type: "validator", Port: 5100},
				{Name: "query", Type: "query-service", Port: 5140},
			},
		},
	}, t.TempDir(), false)

	compose, err := g.buildCompose(t.TempDir())
	if err != nil {
		t.Fatalf("buildCompose returned error: %v", err)
	}

	if _, exists := compose.Services["db"]; exists {
		t.Fatalf("did not expect a local db service in compose: %#v", compose.Services)
	}
	for _, serviceName := range []string{"validator", "query"} {
		if _, exists := compose.Services[serviceName]; !exists {
			t.Fatalf("expected service %q to exist", serviceName)
		}
		if len(compose.Services[serviceName].DependsOn) != 0 {
			t.Fatalf("external database should not create local db dependency for %s: %#v", serviceName, compose.Services[serviceName].DependsOn)
		}
	}
}

func TestBuildComposeLocalPostgresAddsHealthyDependency(t *testing.T) {
	g := NewGenerator(&config.NetworkConfig{
		Docker: config.DockerConfig{
			Name:           "fx-network",
			Network:        "fx-net",
			NetworkDriver:  "bridge",
			CommitterImage: "hyperledger/fabric-x-committer:0.0.19",
			PostgresImage:  "postgres:16",
		},
		OrdererOrgs: []config.OrdererOrg{{
			Name:   "Orderer",
			Domain: "example.com",
			Orderers: []config.Node{{
				Name: "orderer0",
				Type: "router",
				Port: 7050,
			}},
		}},
		PeerOrgs: []config.PeerOrg{{Name: "Org1", Domain: "org1.example.com"}},
		Committer: &config.CommitterConfig{
			UsePostgres: true,
			Components: []config.CommitterNode{
				{Name: "db", Type: "db", Port: 15432, PostgresUser: "postgres", PostgresPassword: "secret", PostgresDB: "fxdb"},
				{Name: "validator", Type: "validator", Port: 5100},
			},
		},
	}, t.TempDir(), false)

	compose, err := g.buildCompose(t.TempDir())
	if err != nil {
		t.Fatalf("buildCompose returned error: %v", err)
	}

	dep, ok := compose.Services["validator"].DependsOn["db"]
	if !ok {
		t.Fatalf("validator should depend on local db: %#v", compose.Services["validator"].DependsOn)
	}
	if dep.Condition != "service_healthy" {
		t.Fatalf("expected service_healthy dependency, got %#v", dep)
	}
}

func TestBuildOrdererServiceUsesDefaultPort(t *testing.T) {
	g := NewGenerator(&config.NetworkConfig{
		Docker: config.DockerConfig{
			Network:       "fx-net",
			NetworkDriver: "bridge",
			OrdererImage:  "hyperledger/fabric-x-orderer:local",
		},
	}, t.TempDir(), false)

	service, err := g.buildOrdererService("orderer-assembler-1", &config.OrdererOrg{
		Name:   "Orderer",
		Domain: "example.com",
	}, &config.Node{
		Name: "assembler0",
		Type: "assembler",
	}, t.TempDir())
	if err != nil {
		t.Fatalf("buildOrdererService returned error: %v", err)
	}

	foundServicePort := false
	foundMonitoringPort := false
	for _, port := range service.Ports {
		if port == "7053:7053" {
			foundServicePort = true
		}
		if port == "7063:7063" {
			foundMonitoringPort = true
		}
	}
	if !foundServicePort || !foundMonitoringPort {
		t.Fatalf("expected default service and monitoring ports, got %#v", service.Ports)
	}
	if !containsString(service.Volumes, "${FABRIC_X_RUNTIME_DATA_ROOT:-./runtime-data}/orderer-assembler-1:/runtime") {
		t.Fatalf("expected orderer store outside the config tree, got %#v", service.Volumes)
	}
}

func TestBuildConsensusAndSidecarMountRuntimeDataOutsideConfig(t *testing.T) {
	g := NewGenerator(&config.NetworkConfig{
		Docker: config.DockerConfig{
			Network:        "fx-net",
			NetworkDriver:  "bridge",
			OrdererImage:   "hyperledger/fabric-x-orderer:local",
			CommitterImage: "hyperledger/fabric-x-committer:local",
		},
	}, t.TempDir(), false)

	consensus, err := g.buildOrdererService("orderer-consensus-1", &config.OrdererOrg{
		Name: "Orderer",
	}, &config.Node{Type: "consensus"}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	want := "${FABRIC_X_RUNTIME_DATA_ROOT:-./runtime-data}/orderer-consensus-1:/runtime"
	if !containsString(consensus.Volumes, want) {
		t.Fatalf("consensus runtime mount missing %q: %#v", want, consensus.Volumes)
	}

	sidecar, err := g.buildCommitterService("committer-sidecar", &config.CommitterNode{
		Name: "committer-sidecar",
		Type: "sidecar",
	}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	want = "${FABRIC_X_RUNTIME_DATA_ROOT:-./runtime-data}/committer-sidecar:/runtime"
	if !containsString(sidecar.Volumes, want) {
		t.Fatalf("sidecar runtime mount missing %q: %#v", want, sidecar.Volumes)
	}
}

func boolPtr(v bool) *bool {
	return &v
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func containsVolumeSuffix(values []string, suffix string) bool {
	for _, value := range values {
		if strings.HasSuffix(value, suffix) {
			return true
		}
	}
	return false
}
