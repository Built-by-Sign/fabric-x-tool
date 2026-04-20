package compose

import (
	"strings"
	"testing"

	"config-builder/internal/config"
)

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
	if len(service.Volumes) != 1 || !strings.Contains(service.Volumes[0], "/var/lib/postgresql/data") {
		t.Fatalf("expected db data volume mount, got %#v", service.Volumes)
	}
	if !strings.Contains(service.Volumes[0], "local-deployment/committer-db/data") {
		t.Fatalf("expected local deployment data dir mount, got %#v", service.Volumes)
	}
	if service.WorkingDir != "" {
		t.Fatalf("db service should not set working_dir, got %q", service.WorkingDir)
	}
	if service.HealthCheck == nil {
		t.Fatal("db service should include healthcheck")
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

func boolPtr(v bool) *bool {
	return &v
}
