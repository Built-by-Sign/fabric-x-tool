package cmd

import (
	"fmt"

	"github.com/ethsign/cbdc-chain/cbdc-network/config-builder/internal/compose"
	"github.com/ethsign/cbdc-chain/cbdc-network/config-builder/internal/config"
	"github.com/ethsign/cbdc-chain/cbdc-network/config-builder/internal/crypto"
	"github.com/ethsign/cbdc-chain/cbdc-network/config-builder/internal/setup"
	"github.com/spf13/cobra"
)

var (
	// Global flags
	configFile    string
	outputDir     string
	logLevel      string
	showProgress  bool
	useLocalTools bool
)

// rootCmd represents the base command when called without any subcommands
var rootCmd = &cobra.Command{
	Use:   "config-builder",
	Short: "Fabric-X Network Builder",
	Long: `config-builder is a CLI tool to build, configure, and manage Fabric-X networks.

It provides commands to:
  - setup    : Generate crypto materials, genesis block, and node configurations
  - start    : Start the network using docker-compose
  - stop     : Stop the running network
  - teardown : Remove all containers and clean up resources
  - generate : Generate specific configuration files

Example:
  network-builder setup -c network.yaml -o ./out
  network-builder start
  network-builder stop
  network-builder teardown`,
	Version: "0.1.0",
}

// Execute adds all child commands to the root command and sets flags appropriately.
func Execute() error {
	return rootCmd.Execute()
}

func init() {
	// Global flags
	rootCmd.PersistentFlags().StringVarP(&configFile, "config", "c", "network.yaml", "network configuration file")
	rootCmd.PersistentFlags().StringVarP(&outputDir, "output", "o", "./out", "output directory for generated files")
	rootCmd.PersistentFlags().StringVar(&logLevel, "log-level", "info", "log level: quiet, info, verbose, debug")
	rootCmd.PersistentFlags().BoolVar(&showProgress, "progress", true, "show progress information")
	rootCmd.PersistentFlags().BoolVar(&useLocalTools, "use-local-tools", false, "use local tools instead of Docker (requires tools in PATH)")

	// Add subcommands
	rootCmd.AddCommand(setupCmd)
	rootCmd.AddCommand(genComposeCmd)
	rootCmd.AddCommand(generateCmd)
}

// setupCmd represents the setup command
var setupCmd = &cobra.Command{
	Use:   "setup",
	Short: "Setup the network (generate all artifacts)",
	Long: `Generate all required artifacts for the Fabric-X network:
	 - Crypto materials (certificates and keys)
	 - Genesis block
	 - Node configuration files

Note: This command does NOT generate docker-compose.yaml. Use 'gen-compose' command to generate it.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		opts := &setup.Options{
			ConfigFile:   configFile,
			OutputDir:    outputDir,
			LogLevel:     logLevel,
			ShowProgress: showProgress,
		}

		// Apply command-line override for use_local_tools if flag was set
		if cmd.Flags().Changed("use-local-tools") {
			opts.UseLocalTools = &useLocalTools
		}

		runner := setup.NewRunner(opts)
		return runner.Run()
	},
}

// genComposeCmd represents the gen-compose command
var genComposeCmd = &cobra.Command{
	Use:   "gen-compose",
	Short: "Generate docker-compose.yaml file",
	Long: `Generate docker-compose.yaml file for the Fabric-X network.

This command loads the network configuration and generates a docker-compose.yaml
file that can be used to start the network. The setup command must be run first
to generate all required artifacts.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		// Load configuration
		cfg, err := config.Load(configFile)
		if err != nil {
			return fmt.Errorf("failed to load config: %w", err)
		}

		// Override output directory if specified via command line
		if outputDir != "" {
			cfg.OutputDir = outputDir
		}

		verbose := logLevel == "verbose" || logLevel == "debug"
		if verbose {
			fmt.Printf("Generating docker-compose.yaml...\n")
			fmt.Printf("  Config file: %s\n", configFile)
			fmt.Printf("  Output dir: %s\n", cfg.OutputDir)
		}

		// Generate docker-compose.yaml
		generator := compose.NewGenerator(cfg, cfg.OutputDir, verbose)
		if err := generator.Generate(); err != nil {
			return fmt.Errorf("failed to generate docker-compose: %w", err)
		}

		if verbose {
			fmt.Println("Docker-compose.yaml generated successfully")
		}

		return nil
	},
}

// generateCmd represents the generate command
var generateCmd = &cobra.Command{
	Use:   "generate [type]",
	Short: "Generate specific configuration files",
	Long: `Generate specific configuration files without running full setup.

Types:
  crypto-config  - Generate crypto-config.yaml for cryptogen
  configtx       - Generate configtx.yaml for configtxgen
  node-config    - Generate node configuration files
  docker-compose - Generate docker-compose.yaml`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		genType := args[0]

		// Load configuration first
		cfg, err := config.Load(configFile)
		if err != nil {
			return fmt.Errorf("failed to load config: %w", err)
		}

		if outputDir != "" {
			cfg.OutputDir = outputDir
		}

		fmt.Printf("Generating %s...\n", genType)

		switch genType {
		case "crypto-config":
			verbose := logLevel == "verbose" || logLevel == "debug"
			generator := crypto.NewGenerator(cfg, cfg.OutputDir, verbose)
			configPath, err := generator.GenerateCryptoConfigOnly()
			if err != nil {
				return err
			}
			fmt.Printf("Generated crypto-config.yaml at: %s\n", configPath)
		case "configtx":
			// TODO: Generate configtx.yaml
			fmt.Println("configtx generation will be implemented in genesis-generator step")
		case "node-config":
			// TODO: Generate node configurations
			fmt.Println("node-config generation will be implemented in template-engine step")
		case "docker-compose":
			// TODO: Generate docker-compose.yaml
			fmt.Println("docker-compose generation will be implemented in compose-generator step")
		default:
			return fmt.Errorf("unknown generation type: %s", genType)
		}

		return nil
	},
}
