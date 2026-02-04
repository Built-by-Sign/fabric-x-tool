package armageddon

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"text/template"

	"github.com/ethsign/cbdc-chain/cbdc-network/config-builder/internal/config"
	"golang.org/x/text/cases"
	"golang.org/x/text/language"
)

const (
	armageddonSourcePath = "cmd/armageddon" // Relative to fabric-x-orderer repo
	armageddonBinName    = "armageddon"
	sharedConfigFileName = "shared_config.yaml"
	sharedConfigBinName  = "shared_config.binpb"
	armageddonPackage    = "github.com/hyperledger/fabric-x-orderer/cmd/armageddon@v0.0.19"
)

// Generator handles the generation of shared_config.binpb
type Generator struct {
	config     *config.NetworkConfig
	outputDir  string
	verbose    bool
	armageddon string // path to armageddon binary
}

// NewGenerator creates a new armageddon generator
func NewGenerator(cfg *config.NetworkConfig, outputDir string, verbose bool) *Generator {
	return &Generator{
		config:    cfg,
		outputDir: outputDir,
		verbose:   verbose,
	}
}

// Generate orchestrates the shared_config.binpb generation process
func (g *Generator) Generate() error {
	// Step 1: Generate shared_config.yaml
	sharedConfigYamlPath, err := g.generateSharedConfigYaml()
	if err != nil {
		return fmt.Errorf("failed to generate shared_config.yaml: %w", err)
	}

	// Step 2: Try to find or build armageddon
	armageddonPath, err := g.findArmageddon()
	if err != nil {
		// If armageddon is not available, just generate shared_config.yaml
		g.log("armageddon not found, only shared_config.yaml was generated")
		g.log("  To generate shared_config.binpb, please install armageddon or build it from source")
		return nil
	}
	g.armageddon = armageddonPath

	// Step 3: Run armageddon to generate shared_config.binpb
	if err := g.runArmageddon(sharedConfigYamlPath); err != nil {
		return fmt.Errorf("failed to run armageddon: %w", err)
	}

	return nil
}

// findArmageddon locates the armageddon binary
// It tries multiple methods in order:
// 1. Check if already built in output directory
// 2. Try to build from local source (if fabric-x-orderer repo exists)
// 3. Try to install using go install from GitHub (like Ansible does)
// 4. Try to find in PATH
func (g *Generator) findArmageddon() (string, error) {
	absOutputDir, err := filepath.Abs(g.outputDir)
	if err != nil {
		return "", fmt.Errorf("failed to get absolute output path: %w", err)
	}

	// Target location for built armageddon
	cliDir := filepath.Join(absOutputDir, "cli")
	targetPath := filepath.Join(cliDir, armageddonBinName)

	// Method 1: Check if already built
	if _, err := os.Stat(targetPath); err == nil {
		g.log("Found armageddon at: %s", targetPath)
		return targetPath, nil
	}

	// Method 2: Try to build from local source (if fabric-x-orderer repo exists)
	armageddonSourceDir := filepath.Join(g.config.ProjectDir, "fabric-x-orderer", armageddonSourcePath)
	if _, err := os.Stat(filepath.Join(armageddonSourceDir, "main.go")); err == nil {
		g.log("Attempting to build armageddon from local source: %s", armageddonSourceDir)
		if err := g.buildArmageddon(armageddonSourceDir, targetPath); err != nil {
			g.log("Failed to build from source: %v", err)
		} else {
			if _, err := os.Stat(targetPath); err == nil {
				return targetPath, nil
			}
		}
	}

	// Method 3: Try to install using go install (like Ansible bin/install)
	g.log("Attempting to install armageddon using go install...")
	if err := g.installArmageddon(targetPath); err != nil {
		g.log("Failed to install using go install: %v", err)
	} else {
		if _, err := os.Stat(targetPath); err == nil {
			return targetPath, nil
		}
	}

	// Method 4: Try to find in PATH
	path, err := exec.LookPath(armageddonBinName)
	if err == nil {
		g.log("Found armageddon in PATH: %s", path)
		return path, nil
	}

	return "", fmt.Errorf("armageddon not found. Tried: go install, PATH")
}

// installArmageddon installs armageddon using go install (like Ansible does)
func (g *Generator) installArmageddon(targetPath string) error {
	// Create output directory
	if err := os.MkdirAll(filepath.Dir(targetPath), 0755); err != nil {
		return fmt.Errorf("failed to create output directory: %w", err)
	}

	// Use go install with GOBIN set to output directory
	cmd := exec.Command("go", "install", armageddonPackage)
	cmd.Env = os.Environ()
	cmd.Env = append(cmd.Env, fmt.Sprintf("GOBIN=%s", filepath.Dir(targetPath)))

	g.log("Running: GOBIN=%s go install %s", filepath.Dir(targetPath), armageddonPackage)

	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("go install failed: %w\nOutput: %s", err, string(output))
	}

	// Verify the binary was installed
	if _, err := os.Stat(targetPath); os.IsNotExist(err) {
		return fmt.Errorf("armageddon was not installed to expected location: %s", targetPath)
	}

	g.log("Successfully installed armageddon at: %s", targetPath)
	return nil
}

// buildArmageddon builds the armageddon binary from local source
func (g *Generator) buildArmageddon(sourceDir, targetPath string) error {
	// Create output directory
	if err := os.MkdirAll(filepath.Dir(targetPath), 0755); err != nil {
		return fmt.Errorf("failed to create output directory: %w", err)
	}

	// Build command
	cmd := exec.Command("go", "build", "-o", targetPath, ".")
	cmd.Dir = sourceDir
	cmd.Env = os.Environ()

	g.log("Running: go build -o %s .", targetPath)

	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("go build failed: %w\nOutput: %s", err, string(output))
	}

	g.log("Successfully built armageddon at: %s", targetPath)
	return nil
}

// generateSharedConfigYaml generates the shared_config.yaml file
func (g *Generator) generateSharedConfigYaml() (string, error) {
	absOutputDir, err := filepath.Abs(g.outputDir)
	if err != nil {
		return "", fmt.Errorf("failed to get absolute output path: %w", err)
	}

	configDir := filepath.Join(absOutputDir, "build", "config", "armageddon-artifacts")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create config directory: %w", err)
	}

	configPath := filepath.Join(configDir, sharedConfigFileName)

	// Build template data
	data := g.buildTemplateData()

	// Parse and execute template
	caser := cases.Title(language.English)
	tmpl, err := template.New("shared_config").Funcs(template.FuncMap{
		"title": caser.String,
	}).Parse(sharedConfigTemplate)
	if err != nil {
		return "", fmt.Errorf("failed to parse template: %w", err)
	}

	file, err := os.Create(configPath)
	if err != nil {
		return "", fmt.Errorf("failed to create output file: %w", err)
	}
	defer file.Close()

	if err := tmpl.Execute(file, data); err != nil {
		return "", fmt.Errorf("failed to execute template: %w", err)
	}

	g.log("Generated shared_config.yaml at: %s", configPath)
	return configPath, nil
}

// runArmageddon executes the armageddon binary to create shared_config.binpb
func (g *Generator) runArmageddon(sharedConfigYamlPath string) error {
	absOutputDir, err := filepath.Abs(g.outputDir)
	if err != nil {
		return fmt.Errorf("failed to get absolute output path: %w", err)
	}
	outputDir := filepath.Join(absOutputDir, "build", "config", "armageddon-artifacts")

	args := []string{
		"createSharedConfigProto",
		"--sharedConfigYaml=" + sharedConfigYamlPath,
		"--output=" + outputDir,
	}

	cmd := exec.Command(g.armageddon, args...)
	cmd.Dir = g.config.ProjectDir
	cmd.Env = os.Environ()

	g.log("Running armageddon: %s %v", g.armageddon, args)

	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("armageddon failed: %w\nOutput: %s", err, string(output))
	}

	if g.verbose {
		fmt.Println(string(output))
	}

	// Log the generated shared_config.binpb location
	sharedConfigBinPath := filepath.Join(outputDir, sharedConfigBinName)
	if absPath, err := filepath.Abs(sharedConfigBinPath); err == nil {
		if info, err := os.Stat(absPath); err == nil {
			fmt.Printf("Shared config generated successfully!\n")
			fmt.Printf("  Location: %s\n", absPath)
			fmt.Printf("  Size: %s\n", formatBytes(info.Size()))
		} else {
			fmt.Printf("Shared config generated successfully at: %s\n", absPath)
		}
	} else {
		fmt.Printf("Shared config generated successfully at: %s\n", sharedConfigBinPath)
	}

	return nil
}

// log prints a message
func (g *Generator) log(format string, args ...interface{}) {
	if g.verbose {
		fmt.Printf("  [armageddon] "+format+"\n", args...)
	}
}

// formatBytes formats bytes to human-readable format
func formatBytes(bytes int64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}
	div, exp := int64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(bytes)/float64(div), "KMGTPE"[exp])
}
