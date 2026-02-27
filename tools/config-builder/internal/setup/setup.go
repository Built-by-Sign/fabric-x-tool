package setup

import (
	"fmt"
	"os"
	"path/filepath"

	"config-builder/internal/armageddon"
	"config-builder/internal/config"
	"config-builder/internal/crypto"
	"config-builder/internal/genesis"
	"config-builder/internal/template"
)

// Options contains setup command options
type Options struct {
	ConfigFile    string
	OutputDir     string
	LogLevel      string // quiet, info, verbose, debug
	ShowProgress  bool
	UseLocalTools *bool // Override config file setting (nil = use config file value)
}

// Runner handles the setup process
type Runner struct {
	opts   *Options
	config *config.NetworkConfig
}

// NewRunner creates a new setup runner
func NewRunner(opts *Options) *Runner {
	return &Runner{opts: opts}
}

// Run executes the full setup process
func (r *Runner) Run() error {
	// Step 0: Setup log level
	r.setupLogLevel()

	// Step 1: Load and validate configuration
	if err := r.loadConfig(); err != nil {
		return fmt.Errorf("failed to load configuration: %w", err)
	}

	// Step 2: Create output directories
	if err := r.createDirectories(); err != nil {
		return fmt.Errorf("failed to create directories: %w", err)
	}

	// Step 3: Generate crypto materials
	if err := r.generateCryptoMaterials(); err != nil {
		return fmt.Errorf("failed to generate crypto materials: %w", err)
	}

	// Step 5: Generate shared_config.binpb (armageddon)
	if err := r.generateSharedConfig(); err != nil {
		return fmt.Errorf("failed to generate shared config: %w", err)
	}

	// Step 6: Generate genesis block
	if err := r.generateGenesisBlock(); err != nil {
		return fmt.Errorf("failed to generate genesis block: %w", err)
	}

	// Step 7: Generate node configurations
	if err := r.generateNodeConfigs(); err != nil {
		return fmt.Errorf("failed to generate node configurations: %w", err)
	}

	// Note: fxconfig is no longer built locally.
	// It should be run via Docker using the DOCKER_TOOLS_IMAGE.
	// See Makefile targets: create-ns, list-ns

	r.log("Setup completed successfully!")
	return nil
}

// loadConfig loads and validates the network configuration
func (r *Runner) loadConfig() error {
	r.log("Loading configuration from %s...", r.opts.ConfigFile)

	cfg, err := config.Load(r.opts.ConfigFile)
	if err != nil {
		return err
	}

	// Override output directory if specified via command line
	if r.opts.OutputDir != "" {
		cfg.OutputDir = r.opts.OutputDir
	}

	// Override use_local_tools if specified via command line
	if r.opts.UseLocalTools != nil {
		cfg.Docker.UseLocalTools = *r.opts.UseLocalTools
	}

	// Output tool mode information
	if cfg.Docker.UseLocalTools {
		r.log("Using local tools mode (tools will be executed directly)")
	} else {
		r.log("Using Docker mode (tools will be executed in containers)")
	}

	// Configuration validation is done during Load()

	r.config = cfg
	r.logDetails("Configuration loaded successfully")
	r.logDetails("  Channel ID: %s", cfg.ChannelID)
	r.logDetails("  Output Dir: %s", cfg.OutputDir)
	r.logDetails("  Use Local Tools: %v", cfg.Docker.UseLocalTools)
	r.logDetails("  Orderer Orgs: %d", len(cfg.OrdererOrgs))
	r.logDetails("  Peer Orgs: %d", len(cfg.PeerOrgs))

	return nil
}

// createDirectories creates the required output directories
func (r *Runner) createDirectories() error {
	r.log("Creating output directories...")

	dirs := []string{
		r.config.OutputDir,
		filepath.Join(r.config.OutputDir, "build", "config", "cryptogen-artifacts"),
		filepath.Join(r.config.OutputDir, "build", "config", "configtxgen-artifacts"),
		filepath.Join(r.config.OutputDir, "build", "config", "armageddon-artifacts"),
		filepath.Join(r.config.OutputDir, "local-deployment"),
	}

	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("failed to create directory %s: %w", dir, err)
		}
		r.logDetails("  Created: %s", dir)
	}

	return nil
}

// generateCryptoMaterials generates certificates and keys using cryptogen or fabric-ca-client
func (r *Runner) generateCryptoMaterials() error {
	r.log("Generating crypto materials...")

	// Use the factory function to get the appropriate generator
	// It will automatically choose between cryptogen and fabric-ca-client based on KMS config
	generator := crypto.NewCryptoGenerator(r.config, r.config.OutputDir, r.opts.LogLevel, r.opts.ShowProgress)
	if err := generator.Generate(); err != nil {
		return err
	}

	r.log("Crypto materials generated successfully")
	return nil
}

// generateSharedConfig generates shared_config.binpb using armageddon
func (r *Runner) generateSharedConfig() error {
	r.log("Generating shared config...")

	generator := armageddon.NewGenerator(r.config, r.config.OutputDir, r.shouldShowDetails())
	if err := generator.Generate(); err != nil {
		return err
	}

	r.log("Shared config generated successfully")
	return nil
}

// generateGenesisBlock generates the genesis block using configtxgen
func (r *Runner) generateGenesisBlock() error {
	r.log("Generating genesis block...")

	generator := genesis.NewGenerator(r.config, r.config.OutputDir, r.shouldShowDetails())
	if err := generator.Generate(); err != nil {
		return err
	}

	r.log("Genesis block generated successfully")
	return nil
}

// generateNodeConfigs generates configuration files for all nodes
func (r *Runner) generateNodeConfigs() error {
	r.log("Generating node configurations...")
	engine := template.NewEngine(r.config, r.config.OutputDir, r.shouldShowDetails())
	if err := engine.GenerateNodeConfigs(); err != nil {
		return err
	}
	r.log("Node configurations generated successfully")
	return nil
}

// setupLogLevel sets up environment variables based on log level
func (r *Runner) setupLogLevel() {
	logLevel := r.opts.LogLevel
	if logLevel == "" {
		logLevel = "info"
	}

	switch logLevel {
	case "quiet":
		// Quiet mode: disable all debug logs
		os.Unsetenv("KMS_SO_DEBUG")
		os.Unsetenv("GRPC_HELPER_DEBUG")
	case "info":
		// Info mode (default): disable debug logs, show key steps
		os.Unsetenv("KMS_SO_DEBUG")
		os.Unsetenv("GRPC_HELPER_DEBUG")
	case "verbose":
		// Verbose mode: disable debug logs, show detailed steps
		os.Unsetenv("KMS_SO_DEBUG")
		os.Unsetenv("GRPC_HELPER_DEBUG")
	case "debug":
		// Debug mode: enable all debug logs
		os.Setenv("KMS_SO_DEBUG", "1")
		os.Setenv("GRPC_HELPER_DEBUG", "1")
	default:
		// Unknown log level, use info as default
		os.Unsetenv("KMS_SO_DEBUG")
		os.Unsetenv("GRPC_HELPER_DEBUG")
	}
}

// shouldShowDetails returns true if log level is verbose or debug
func (r *Runner) shouldShowDetails() bool {
	return r.opts.LogLevel == "verbose" || r.opts.LogLevel == "debug"
}

// shouldLog returns true if log level is not quiet
func (r *Runner) shouldLog() bool {
	return r.opts.LogLevel != "quiet"
}

// log prints a message for info level and above (not quiet)
func (r *Runner) log(format string, args ...interface{}) {
	if r.shouldLog() {
		fmt.Printf(format+"\n", args...)
	}
}

// logDetails prints a message only when details should be shown (verbose or debug)
func (r *Runner) logDetails(format string, args ...interface{}) {
	if r.shouldShowDetails() {
		fmt.Printf(format+"\n", args...)
	}
}

// GetConfig returns the loaded configuration (for testing)
func (r *Runner) GetConfig() *config.NetworkConfig {
	return r.config
}
