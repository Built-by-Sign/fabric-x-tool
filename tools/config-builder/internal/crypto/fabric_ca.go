package crypto

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"github.com/ethsign/cbdc-chain/cbdc-network/config-builder/internal/config"
	"golang.org/x/term"
)

// FabricCAGenerator handles certificate generation using Fabric CA client with KMS
type FabricCAGenerator struct {
	config         *config.NetworkConfig
	outputDir      string
	logLevel       string
	showProgress   bool
	currentStep    int
	totalSteps     int
	toolsImage     string     // Docker image containing fabric-ca-client and cbdc-tool
	isTTY          bool       // Whether stdout is a TTY (for progress bar)
	progressMutex  sync.Mutex // Protects progress bar updates
	maxConcurrency int        // Maximum concurrent certificate generations
}

// NodeInfo contains information about a node for certificate generation
type NodeInfo struct {
	Name    string
	UserPin string // Per-node PIN for KMS access
}

// NewFabricCAGenerator creates a new Fabric CA certificate generator
func NewFabricCAGenerator(cfg *config.NetworkConfig, outputDir string, logLevel string, showProgress bool) *FabricCAGenerator {
	toolsImage := cfg.Docker.ToolsImage

	// Check if stdout is a TTY for progress bar support
	isTTY := term.IsTerminal(int(os.Stdout.Fd()))

	// Set default concurrency level (can be made configurable via environment variable)
	// Restored to 8 after fixing gRPC client and KMS server concurrency issues
	// Can be adjusted via CRYPTO_MAX_CONCURRENCY environment variable
	maxConcurrency := 8
	if concurrency := os.Getenv("CRYPTO_MAX_CONCURRENCY"); concurrency != "" {
		if n, err := fmt.Sscanf(concurrency, "%d", &maxConcurrency); err == nil && n == 1 && maxConcurrency > 0 {
			// Use environment variable value
		} else {
			maxConcurrency = 16 // Reset to default if invalid
		}
	}

	return &FabricCAGenerator{
		config:         cfg,
		outputDir:      outputDir,
		logLevel:       logLevel,
		showProgress:   showProgress,
		toolsImage:     toolsImage,
		isTTY:          isTTY,
		maxConcurrency: maxConcurrency,
	}
}

// Generate generates all crypto materials using Fabric CA
func (g *FabricCAGenerator) Generate() error {
	g.log("Generating crypto materials using Fabric CA with KMS...")

	// Validate KMS configuration
	if g.config.KMS == nil || !g.config.KMS.Enabled {
		return fmt.Errorf("KMS configuration is required for Fabric CA generator")
	}

	if g.config.KMS.Endpoint == "" {
		return fmt.Errorf("KMS endpoint is required")
	}

	if g.config.KMS.CAURL == "" {
		return fmt.Errorf("Fabric CA URL is required")
	}

	// Calculate total steps for progress tracking
	g.totalSteps = 0
	for _, org := range g.config.OrdererOrgs {
		// For each orderer org: nodes + TLS + Admin + org MSP
		g.totalSteps += len(org.Orderers)*2 + 2
	}
	for _, org := range g.config.PeerOrgs {
		// For each peer org: nodes + TLS + Admin + users + org MSP
		g.totalSteps += len(org.Peers)*2 + 1 + len(org.Users) + 1
	}
	g.currentStep = 0

	// Generate certificates for orderer organizations
	for _, org := range g.config.OrdererOrgs {
		if err := g.GenerateOrdererOrgCrypto(org); err != nil {
			return fmt.Errorf("failed to generate crypto for orderer org %s: %w", org.Name, err)
		}
	}

	// Generate certificates for peer organizations
	for _, org := range g.config.PeerOrgs {
		if err := g.GeneratePeerOrgCrypto(org); err != nil {
			return fmt.Errorf("failed to generate crypto for peer org %s: %w", org.Name, err)
		}
	}

	g.log("Crypto materials generated successfully using Fabric CA")
	return nil
}

// GenerateOrdererOrgCrypto generates crypto materials for an orderer organization
func (g *FabricCAGenerator) GenerateOrdererOrgCrypto(org config.OrdererOrg) error {
	g.log("Generating crypto for orderer org: %s", org.Name)

	// Determine organization-level user PIN
	// Priority: org.KMSUserPin (new) > node.UserPin (old, for backward compatibility)
	orgUserPin := org.KMSUserPin

	// Convert orderer nodes to NodeInfo
	nodes := make([]NodeInfo, len(org.Orderers))
	for i, orderer := range org.Orderers {
		// Use org-level PIN if available, otherwise use node-level PIN for backward compatibility
		userPin := orgUserPin
		if userPin == "" {
			userPin = orderer.UserPin
		}

		nodes[i] = NodeInfo{
			Name:    orderer.Name,
			UserPin: userPin,
		}
	}

	// Determine token label for this org
	tokenLabel := g.getTokenLabel(org.Name, org.KMSTokenLabel)

	// Generate crypto materials for the organization
	return g.GenerateOrgCrypto(org.Name, org.Domain, g.config.KMS.CAURL, tokenLabel, nodes, "orderer")
}

// GeneratePeerOrgCrypto generates crypto materials for a peer organization
func (g *FabricCAGenerator) GeneratePeerOrgCrypto(org config.PeerOrg) error {
	g.log("Generating crypto for peer org: %s", org.Name)

	// Determine organization-level user PIN
	// Priority: org.KMSUserPin (new) > node.UserPin (old, for backward compatibility)
	orgUserPin := org.KMSUserPin

	// Convert peer nodes to NodeInfo
	nodes := make([]NodeInfo, len(org.Peers))
	for i, peer := range org.Peers {
		// Use org-level PIN if available, otherwise use node-level PIN for backward compatibility
		userPin := orgUserPin
		if userPin == "" {
			userPin = peer.UserPin
		}

		nodes[i] = NodeInfo{
			Name:    peer.Name,
			UserPin: userPin,
		}
	}

	// Determine token label for this org
	tokenLabel := g.getTokenLabel(org.Name, org.KMSTokenLabel)

	// Generate crypto materials for peer nodes
	if err := g.GenerateOrgCrypto(org.Name, org.Domain, g.config.KMS.CAURL, tokenLabel, nodes, "peer"); err != nil {
		return err
	}

	// Generate crypto materials for users (Admin, channel_admin, endorser, etc.)
	if len(org.Users) > 0 {
		g.log("Generating crypto for %d users in peer org: %s (concurrent mode with max %d workers)",
			len(org.Users), org.Name, g.maxConcurrency)

		// Get absolute output directory
		absOutputDir, err := filepath.Abs(g.outputDir)
		if err != nil {
			return fmt.Errorf("failed to get absolute output path: %w", err)
		}

		cryptoDir := filepath.Join(absOutputDir, "build", "config", "cryptogen-artifacts", "crypto")

		// Use first peer's PIN as default for users if not specified
		defaultUserPin := "1234567"
		if len(nodes) > 0 && nodes[0].UserPin != "" {
			defaultUserPin = nodes[0].UserPin
		}

		// Cache CA certificates to avoid repeated reads
		orgMSPDir := filepath.Join(cryptoDir, "peerOrganizations", org.Domain, "msp")
		caCertData, tlsCACertData, err := g.readOrgCACerts(orgMSPDir)
		if err != nil {
			g.logDetails("Warning: Could not pre-read CA certificates: %v (will read per-user)", err)
		}

		// Generate certificates for each user concurrently
		if err := g.generateUsersParallel(org.Domain, g.config.KMS.CAURL, tokenLabel,
			defaultUserPin, org.Users, cryptoDir, caCertData, tlsCACertData); err != nil {
			return err
		}
	}

	return nil
}

// GenerateOrgCrypto generates crypto materials for an organization using fabric-ca-client
// This method uses docker run to execute cbdc-tool image with fabric-ca-client
// orgType should be "orderer" or "peer"
func (g *FabricCAGenerator) GenerateOrgCrypto(orgName, domain, caURL, tokenLabel string, nodes []NodeInfo, orgType string) error {
	absOutputDir, err := filepath.Abs(g.outputDir)
	if err != nil {
		return fmt.Errorf("failed to get absolute output path: %w", err)
	}

	// Create output directory structure matching cryptogen layout
	// For orderer orgs: ordererOrganizations/<domain>/orderers/<node>.<domain>/msp
	// For peer orgs: peerOrganizations/<domain>/peers/<node>.<domain>/msp
	cryptoDir := filepath.Join(absOutputDir, "build", "config", "cryptogen-artifacts", "crypto")

	// Determine organization directory based on type
	var orgDirType string
	if orgType == "peer" {
		orgDirType = "peerOrganizations"
	} else {
		orgDirType = "ordererOrganizations"
	}

	orgDir := filepath.Join(cryptoDir, orgDirType, domain)
	if err := os.MkdirAll(orgDir, 0755); err != nil {
		return fmt.Errorf("failed to create org directory: %w", err)
	}

	// Generate certificates for each node (in parallel for better performance)
	if err := g.generateNodesParallel(orgName, domain, caURL, tokenLabel, nodes, cryptoDir, orgType); err != nil {
		return err
	}

	// 3. Generate Admin user (using KMS)
	adminNode := NodeInfo{
		Name:    "Admin",
		UserPin: nodes[0].UserPin, // Use first node's PIN for admin
	}
	if err := g.GenerateAdminUser(domain, caURL, tokenLabel, adminNode, cryptoDir, orgType); err != nil {
		return fmt.Errorf("failed to generate admin user: %w", err)
	}

	// 4. Create organization-level MSP directory structure
	// This is required by armageddon tool to find CA certificates
	if err := g.createOrgMSP(domain, cryptoDir, orgType); err != nil {
		return fmt.Errorf("failed to create org MSP structure: %w", err)
	}

	return nil
}

// generateNodeCrypto generates crypto materials for a single node
func (g *FabricCAGenerator) generateNodeCrypto(orgName, domain, caURL, tokenLabel string, node NodeInfo, cryptoDir string, orgType string) error {
	g.logDetails("  Generating crypto for node: %s.%s", node.Name, domain)

	// Determine organization and node directory based on type
	var orgDirType, nodeType string
	if orgType == "peer" {
		orgDirType = "peerOrganizations"
		nodeType = "peers"
	} else {
		orgDirType = "ordererOrganizations"
		nodeType = "orderers"
	}

	// Determine node directory
	nodeDir := filepath.Join(cryptoDir, orgDirType, domain, nodeType, fmt.Sprintf("%s.%s", node.Name, domain), "msp")
	if err := os.MkdirAll(nodeDir, 0755); err != nil {
		return fmt.Errorf("failed to create node directory: %w", err)
	}

	// Use node-specific PIN if provided, otherwise use default
	userPin := node.UserPin
	if userPin == "" {
		userPin = "1234" // Default PIN
	}

	// Run fabric-ca-client enroll (Docker or local)
	if err := g.runFabricCAClientEnroll(nodeDir, caURL, tokenLabel, userPin); err != nil {
		return fmt.Errorf("fabric-ca-client enroll failed for node %s: %w", node.Name, err)
	}

	g.logProgress("Generated identity certificate for %s.%s", node.Name, domain)

	// Rename signcerts/cert.pem to signcerts/{node}.{domain}-cert.pem
	// This is required by armageddon tool which expects the specific naming format
	signcertsDir := filepath.Join(nodeDir, "signcerts")
	srcCertPath := filepath.Join(signcertsDir, "cert.pem")
	nodeFQDN := fmt.Sprintf("%s.%s", node.Name, domain)
	dstCertPath := filepath.Join(signcertsDir, fmt.Sprintf("%s-cert.pem", nodeFQDN))

	// Check if source certificate file exists
	if _, err := os.Stat(srcCertPath); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("certificate file not found: %s", srcCertPath)
		}
		return fmt.Errorf("failed to check certificate file: %w", err)
	}

	// Rename the certificate file
	if err := os.Rename(srcCertPath, dstCertPath); err != nil {
		return fmt.Errorf("failed to rename certificate from %s to %s: %w", srcCertPath, dstCertPath, err)
	}

	g.logDetails("  Renamed certificate: %s -> %s", srcCertPath, dstCertPath)

	// Rename cacerts CA certificate to standard format: ca.{domain}-cert.pem
	// This matches cryptogen behavior and ensures config.yaml references work correctly
	// fabric-ca-client generates certificates with names based on CA URL (e.g., host-docker-internal-7054.pem)
	// but config.yaml expects ca.{domain}-cert.pem format
	cacertsDir := filepath.Join(nodeDir, "cacerts")
	if entries, err := os.ReadDir(cacertsDir); err == nil && len(entries) > 0 {
		// Find the first .pem file (should be the CA certificate)
		for _, entry := range entries {
			if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".pem") {
				srcCACertPath := filepath.Join(cacertsDir, entry.Name())
				dstCACertPath := filepath.Join(cacertsDir, fmt.Sprintf("ca.%s-cert.pem", domain))

				// Only rename if it's not already in the correct format
				if entry.Name() != fmt.Sprintf("ca.%s-cert.pem", domain) {
					if err := os.Rename(srcCACertPath, dstCACertPath); err != nil {
						return fmt.Errorf("failed to rename CA certificate from %s to %s: %w", srcCACertPath, dstCACertPath, err)
					}
					g.logDetails("  Renamed CA certificate: %s -> %s", entry.Name(), fmt.Sprintf("ca.%s-cert.pem", domain))
				}
				break
			}
		}
	}

	// WORKAROUND: Create priv_sk file in keystore for fabric-x-orderer compatibility
	// fabric-x-orderer's ExtractConsenterConfig() hardcodes reading /config/msp/keystore/priv_sk
	// In PKCS11 mode, we create an empty marker file so the code doesn't fail
	// The actual private key operations will use BCCSP/PKCS11 via the node config
	keystoreDir := filepath.Join(nodeDir, "keystore")

	// SECURITY: Delete any real private key files generated by fabric-ca-client
	// In KMS mode, private keys should ONLY exist in KMS, not on local filesystem
	// fabric-ca-client generates *_sk files even in PKCS11 mode, we must remove them
	if entries, err := os.ReadDir(keystoreDir); err == nil {
		for _, entry := range entries {
			if !entry.IsDir() && strings.HasSuffix(entry.Name(), "_sk") && entry.Name() != "priv_sk" {
				realKeyPath := filepath.Join(keystoreDir, entry.Name())
				if err := os.Remove(realKeyPath); err != nil {
					g.logDetails("  Warning: failed to remove real private key file %s: %v", entry.Name(), err)
				} else {
					g.logDetails("  Removed real private key file (KMS mode): %s", entry.Name())
				}
			}
		}
	}

	privSkPath := filepath.Join(keystoreDir, "priv_sk")

	// Create an empty priv_sk file as a marker
	// The file content doesn't matter because BCCSP will use KMS for actual signing
	markerContent := []byte("# This is a marker file for PKCS11 mode\n# Actual private key is stored in KMS\n# SKI will be derived from the certificate\n")
	if err := os.WriteFile(privSkPath, markerContent, 0600); err != nil {
		return fmt.Errorf("failed to create priv_sk marker file: %w", err)
	}

	g.logDetails("  Created priv_sk marker file for PKCS11 compatibility")

	// Generate config.yaml for the node's MSP directory
	// This is required for NodeOUs support to identify admin roles
	if err := g.generateMSPConfig(nodeDir, domain); err != nil {
		return fmt.Errorf("failed to generate MSP config for node: %w", err)
	}

	g.logDetails("  Successfully generated identity crypto for node: %s.%s", node.Name, domain)
	return nil
}

// GenerateNodeTLS generates TLS certificates for a node using fabric-ca-client
// Reference: cbdc-biz/scripts/gen_crypto.sh:74
// This uses software mode (no KMS) with --enrollment.profile tls
// Command: fabric-ca-client enroll -u "URL" -m "node.domain" --enrollment.profile tls -M "dir/node"
func (g *FabricCAGenerator) GenerateNodeTLS(domain, caURL string, node NodeInfo, cryptoDir string, orgType string) error {
	g.logDetails("  Generating TLS certificate for node: %s.%s", node.Name, domain)

	// Determine organization and node directory based on type
	var orgDirType, nodeType string
	if orgType == "peer" {
		orgDirType = "peerOrganizations"
		nodeType = "peers"
	} else {
		orgDirType = "ordererOrganizations"
		nodeType = "orderers"
	}

	// Get node directory and keys directory
	nodeDir := filepath.Join(cryptoDir, orgDirType, domain, nodeType, fmt.Sprintf("%s.%s", node.Name, domain))
	keysDir := filepath.Join(nodeDir, "keys")
	if err := os.MkdirAll(keysDir, 0755); err != nil {
		return fmt.Errorf("failed to create keys directory: %w", err)
	}

	// Build docker run command to execute fabric-ca-client enroll with TLS profile
	// Note: No KMS configuration (-c parameter) is used for TLS certificates
	// Mount the keys directory and use -M parameter with absolute path in container
	// Set working directory to /app to ensure fabric-ca-client uses mounted directory
	nodeFQDN := fmt.Sprintf("%s.%s", node.Name, domain)

	// Build CSR hosts list to include both FQDN and host.docker.internal
	// This fixes TLS certificate validation when connecting via host.docker.internal
	csrHosts := fmt.Sprintf("%s,host.docker.internal,localhost,0.0.0.0,127.0.0.1,::1", nodeFQDN)

	// Run fabric-ca-client TLS enroll (Docker or local)
	// The enrollment creates: keys/node/{keystore,signcerts,cacerts}
	tlsTempDir := filepath.Join(keysDir, "node")
	if err := g.runFabricCAClientEnrollTLS(tlsTempDir, caURL, nodeFQDN, csrHosts); err != nil {
		return fmt.Errorf("fabric-ca-client TLS enroll failed for node %s: %w", node.Name, err)
	}

	g.logProgress("Generated TLS certificate for %s.%s", node.Name, domain)

	// Rename and reorganize TLS files to standard format
	tlsDir := filepath.Join(nodeDir, "tls")
	if err := os.MkdirAll(tlsDir, 0755); err != nil {
		return fmt.Errorf("failed to create TLS directory: %w", err)
	}

	// Copy and rename: keystore/*_sk -> server.key
	keystoreDir := filepath.Join(tlsTempDir, "keystore")
	keyFiles, err := os.ReadDir(keystoreDir)
	if err != nil {
		return fmt.Errorf("failed to read keystore directory: %w", err)
	}
	if len(keyFiles) == 0 {
		return fmt.Errorf("no private key found in keystore")
	}
	srcKey := filepath.Join(keystoreDir, keyFiles[0].Name())
	dstKey := filepath.Join(tlsDir, "server.key")
	if err := g.copyFile(srcKey, dstKey); err != nil {
		return fmt.Errorf("failed to copy TLS private key: %w", err)
	}

	// Get CA certificate path first (needed for creating certificate chain)
	tlscacertsDir := filepath.Join(tlsTempDir, "tlscacerts")
	caFiles, err := os.ReadDir(tlscacertsDir)
	if err != nil {
		return fmt.Errorf("failed to read tlscacerts directory: %w", err)
	}
	if len(caFiles) == 0 {
		return fmt.Errorf("no TLS CA certificate found in tlscacerts")
	}
	srcCA := filepath.Join(tlscacertsDir, caFiles[0].Name())
	dstCA := filepath.Join(tlsDir, "ca.crt")
	if err := g.copyFile(srcCA, dstCA); err != nil {
		return fmt.Errorf("failed to copy TLS CA certificate: %w", err)
	}

	// Create server.crt with complete certificate chain (server cert + CA cert)
	// This ensures the server sends the complete certificate chain during TLS handshake
	// which is required for clients to verify the certificate
	srcCert := filepath.Join(tlsTempDir, "signcerts", "cert.pem")
	dstCert := filepath.Join(tlsDir, "server.crt")

	// Read server certificate
	serverCertData, err := os.ReadFile(srcCert)
	if err != nil {
		return fmt.Errorf("failed to read TLS server certificate: %w", err)
	}

	// Read CA certificate
	caCertData, err := os.ReadFile(srcCA)
	if err != nil {
		return fmt.Errorf("failed to read TLS CA certificate: %w", err)
	}

	// Write complete certificate chain: server cert + CA cert
	// The order is important: leaf certificate first, then intermediate/root CA
	certChain := append(serverCertData, caCertData...)
	if err := os.WriteFile(dstCert, certChain, 0644); err != nil {
		return fmt.Errorf("failed to write TLS certificate chain: %w", err)
	}

	g.logDetails("  Created TLS certificate chain in server.crt (server cert + CA cert)")

	// Also copy to keys directory for compatibility with gen_crypto.sh
	// gen_crypto.sh copies: ${dir}/node/keystore/* -> ${dir}/node.key
	//                       ${dir}/node/signcerts/cert.pem -> ${dir}/node.crt
	dstNodeKey := filepath.Join(keysDir, "node.key")
	if err := g.copyFile(srcKey, dstNodeKey); err != nil {
		return fmt.Errorf("failed to copy node.key: %w", err)
	}
	dstNodeCrt := filepath.Join(keysDir, "node.crt")
	if err := g.copyFile(srcCert, dstNodeCrt); err != nil {
		return fmt.Errorf("failed to copy node.crt: %w", err)
	}

	// Remove temporary node directory
	if err := os.RemoveAll(tlsTempDir); err != nil {
		g.logDetails("Warning: failed to remove TLS temp directory: %v", err)
	}

	g.logDetails("  Successfully generated TLS certificate for node: %s.%s", node.Name, domain)
	return nil
}

// GenerateAdminUser generates Admin user certificates using KMS
// Creates users/Admin@{domain}/msp/ directory structure
func (g *FabricCAGenerator) GenerateAdminUser(domain, caURL, tokenLabel string, admin NodeInfo, cryptoDir string, orgType string) error {
	g.logDetails("  Generating Admin user for domain: %s", domain)

	// Determine organization directory based on type
	var orgDirType string
	if orgType == "peer" {
		orgDirType = "peerOrganizations"
	} else {
		orgDirType = "ordererOrganizations"
	}

	// Create Admin user directory: users/Admin@{domain}/msp
	adminDir := filepath.Join(cryptoDir, orgDirType, domain, "users", fmt.Sprintf("Admin@%s", domain), "msp")
	if err := os.MkdirAll(adminDir, 0755); err != nil {
		return fmt.Errorf("failed to create admin directory: %w", err)
	}

	// Use admin-specific PIN if provided, otherwise use default
	userPin := admin.UserPin
	if userPin == "" {
		userPin = "1234" // Default PIN
	}

	// Run fabric-ca-client enroll for Admin (Docker or local)
	if err := g.runFabricCAClientEnroll(adminDir, caURL, tokenLabel, userPin); err != nil {
		return fmt.Errorf("fabric-ca-client enroll failed for Admin user: %w", err)
	}

	g.logProgress("Generated Admin user certificate for %s", domain)

	// WORKAROUND: Create priv_sk marker file for Admin user as well
	keystoreDir := filepath.Join(adminDir, "keystore")
	privSkPath := filepath.Join(keystoreDir, "priv_sk")
	markerContent := []byte("# This is a marker file for PKCS11 mode\n# Actual private key is stored in KMS\n# SKI will be derived from the certificate\n")
	if err := os.WriteFile(privSkPath, markerContent, 0600); err != nil {
		return fmt.Errorf("failed to create priv_sk marker file for Admin: %w", err)
	}

	g.logDetails("  Created priv_sk marker file for Admin user")

	// Generate config.yaml for the Admin user's MSP directory
	// This is required for NodeOUs support to identify admin roles
	if err := g.generateMSPConfig(adminDir, domain); err != nil {
		return fmt.Errorf("failed to generate MSP config for Admin user: %w", err)
	}

	g.logDetails("  Successfully generated Admin user for domain: %s", domain)
	return nil
}

// GeneratePeerUser generates user certificates for peer organization using KMS
// Creates users/{username}@{domain}/msp/ directory structure
func (g *FabricCAGenerator) GeneratePeerUser(domain, caURL, tokenLabel string, user NodeInfo, cryptoDir string) error {
	g.logDetails("  Generating user certificate for: %s@%s", user.Name, domain)

	// Create user directory: users/{username}@{domain}/msp
	userDir := filepath.Join(cryptoDir, "peerOrganizations", domain, "users", fmt.Sprintf("%s@%s", user.Name, domain), "msp")
	if err := os.MkdirAll(userDir, 0755); err != nil {
		return fmt.Errorf("failed to create user directory: %w", err)
	}

	// Use user-specific PIN if provided, otherwise use default
	userPin := user.UserPin
	if userPin == "" {
		userPin = "1234567" // Default PIN
	}

	// Run fabric-ca-client enroll for user (Docker or local)
	if err := g.runFabricCAClientEnroll(userDir, caURL, tokenLabel, userPin); err != nil {
		return fmt.Errorf("fabric-ca-client enroll failed for user %s: %w", user.Name, err)
	}

	g.logProgress("Generated user certificate for %s@%s", user.Name, domain)

	// Rename signcerts/cert.pem to signcerts/{username}@{domain}-cert.pem
	// This matches cryptogen's naming convention
	signcertsDir := filepath.Join(userDir, "signcerts")
	srcCertPath := filepath.Join(signcertsDir, "cert.pem")
	userFQDN := fmt.Sprintf("%s@%s", user.Name, domain)
	dstCertPath := filepath.Join(signcertsDir, fmt.Sprintf("%s-cert.pem", userFQDN))

	if _, err := os.Stat(srcCertPath); err == nil {
		if err := os.Rename(srcCertPath, dstCertPath); err != nil {
			return fmt.Errorf("failed to rename certificate: %w", err)
		}
		g.logDetails("  Renamed certificate: %s -> %s", srcCertPath, dstCertPath)
	}

	// Rename cacerts CA certificate to standard format: ca.{domain}-cert.pem
	cacertsDir := filepath.Join(userDir, "cacerts")
	if entries, err := os.ReadDir(cacertsDir); err == nil && len(entries) > 0 {
		for _, entry := range entries {
			if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".pem") {
				srcCACertPath := filepath.Join(cacertsDir, entry.Name())
				dstCACertPath := filepath.Join(cacertsDir, fmt.Sprintf("ca.%s-cert.pem", domain))
				if entry.Name() != fmt.Sprintf("ca.%s-cert.pem", domain) {
					if err := os.Rename(srcCACertPath, dstCACertPath); err != nil {
						return fmt.Errorf("failed to rename CA certificate: %w", err)
					}
					g.logDetails("  Renamed CA certificate: %s -> %s", entry.Name(), fmt.Sprintf("ca.%s-cert.pem", domain))
				}
				break
			}
		}
	}

	// Create admincerts directory and copy the user certificate
	// This is required by cryptogen-style MSP structure
	admincertsDir := filepath.Join(userDir, "admincerts")
	if err := os.MkdirAll(admincertsDir, 0755); err != nil {
		return fmt.Errorf("failed to create admincerts directory: %w", err)
	}

	adminCertPath := filepath.Join(admincertsDir, fmt.Sprintf("%s-cert.pem", userFQDN))
	if err := g.copyFile(dstCertPath, adminCertPath); err != nil {
		return fmt.Errorf("failed to copy certificate to admincerts: %w", err)
	}
	g.logDetails("  Created admincerts: %s", adminCertPath)

	// Copy CA certificate from org-level MSP to user's MSP
	// This is required for MSP validation
	orgMSPDir := filepath.Join(cryptoDir, "peerOrganizations", domain, "msp")
	orgCACertsDir := filepath.Join(orgMSPDir, "cacerts")
	userCACertsDir := filepath.Join(userDir, "cacerts")

	// Ensure user's cacerts directory exists
	if err := os.MkdirAll(userCACertsDir, 0755); err != nil {
		return fmt.Errorf("failed to create user cacerts directory: %w", err)
	}

	// Find and copy CA certificate
	caCertFiles, err := os.ReadDir(orgCACertsDir)
	if err != nil {
		return fmt.Errorf("failed to read org cacerts directory: %w", err)
	}

	if len(caCertFiles) > 0 {
		srcCACert := filepath.Join(orgCACertsDir, caCertFiles[0].Name())
		dstCACert := filepath.Join(userCACertsDir, caCertFiles[0].Name())
		if err := g.copyFile(srcCACert, dstCACert); err != nil {
			return fmt.Errorf("failed to copy CA certificate to user MSP: %w", err)
		}
		g.logDetails("  Copied CA certificate to user MSP: %s", dstCACert)
	}

	// Copy TLS CA certificate from org-level MSP
	orgTLSCACertsDir := filepath.Join(orgMSPDir, "tlscacerts")
	userTLSCACertsDir := filepath.Join(userDir, "tlscacerts")

	if err := os.MkdirAll(userTLSCACertsDir, 0755); err != nil {
		return fmt.Errorf("failed to create user tlscacerts directory: %w", err)
	}

	// Find and copy TLS CA certificate
	tlsCACertFiles, err := os.ReadDir(orgTLSCACertsDir)
	if err == nil && len(tlsCACertFiles) > 0 {
		srcTLSCACert := filepath.Join(orgTLSCACertsDir, tlsCACertFiles[0].Name())
		dstTLSCACert := filepath.Join(userTLSCACertsDir, tlsCACertFiles[0].Name())
		if err := g.copyFile(srcTLSCACert, dstTLSCACert); err != nil {
			return fmt.Errorf("failed to copy TLS CA certificate: %w", err)
		}
		g.logDetails("  Copied TLS CA certificate: %s", dstTLSCACert)
	}

	// WORKAROUND: Create priv_sk marker file for user as well
	keystoreDir := filepath.Join(userDir, "keystore")
	privSkPath := filepath.Join(keystoreDir, "priv_sk")
	markerContent := []byte("# This is a marker file for PKCS11 mode\n# Actual private key is stored in KMS\n# SKI will be derived from the certificate\n")
	if err := os.WriteFile(privSkPath, markerContent, 0600); err != nil {
		return fmt.Errorf("failed to create priv_sk marker file for user: %w", err)
	}

	g.logDetails("  Created priv_sk marker file for user: %s", user.Name)

	// Generate config.yaml for the user's MSP directory
	// This is required for NodeOUs support to identify admin roles
	if err := g.generateMSPConfig(userDir, domain); err != nil {
		return fmt.Errorf("failed to generate MSP config for user: %w", err)
	}

	g.logDetails("  Successfully generated certificate for user: %s@%s", user.Name, domain)
	return nil
}

// generateMSPConfig generates config.yaml for MSP with NodeOUs configuration
func (g *FabricCAGenerator) generateMSPConfig(mspDir, domain string) error {
	configPath := filepath.Join(mspDir, "config.yaml")

	// Generate NodeOUs configuration
	// Reference: Fabric MSP config with NodeOUs enabled
	// IMPORTANT: Use spaces for indentation, not tabs (YAML requirement)
	configContent := fmt.Sprintf(`NodeOUs:
  Enable: true
  ClientOUIdentifier:
    Certificate: cacerts/ca.%s-cert.pem
    OrganizationalUnitIdentifier: client
  PeerOUIdentifier:
    Certificate: cacerts/ca.%s-cert.pem
    OrganizationalUnitIdentifier: peer
  AdminOUIdentifier:
    Certificate: cacerts/ca.%s-cert.pem
    OrganizationalUnitIdentifier: admin
  OrdererOUIdentifier:
    Certificate: cacerts/ca.%s-cert.pem
    OrganizationalUnitIdentifier: orderer
`, domain, domain, domain, domain)

	if err := os.WriteFile(configPath, []byte(configContent), 0644); err != nil {
		return fmt.Errorf("failed to write config.yaml: %w", err)
	}

	g.logDetails("  Generated MSP config.yaml: %s", configPath)
	return nil
}

// getTokenLabel returns the token label for an organization
func (g *FabricCAGenerator) getTokenLabel(orgName, customLabel string) string {
	if customLabel != "" {
		return customLabel
	}
	if g.config.KMS != nil && g.config.KMS.TokenLabel != "" {
		return g.config.KMS.TokenLabel
	}
	return "FabricToken"
}

// createOrgMSP creates organization-level MSP directory structure
// This copies the CA certificate from the first node to the org-level MSP
// Required by armageddon tool: ordererOrganizations/{domain}/msp/cacerts/ca.{domain}-cert.pem
// Also creates tlscacerts directory and config.yaml
func (g *FabricCAGenerator) createOrgMSP(domain, cryptoDir string, orgType string) error {
	g.logDetails("Creating organization-level MSP for domain: %s", domain)

	// Determine organization and node directory based on type
	var orgDirType, nodeType string
	if orgType == "peer" {
		orgDirType = "peerOrganizations"
		nodeType = "peers"
	} else {
		orgDirType = "ordererOrganizations"
		nodeType = "orderers"
	}

	// Define paths
	orgMSPDir := filepath.Join(cryptoDir, orgDirType, domain, "msp")
	orgCACertsDir := filepath.Join(orgMSPDir, "cacerts")
	orgTLSCACertsDir := filepath.Join(orgMSPDir, "tlscacerts")

	// Create org MSP directories
	if err := os.MkdirAll(orgCACertsDir, 0755); err != nil {
		return fmt.Errorf("failed to create org cacerts directory: %w", err)
	}
	if err := os.MkdirAll(orgTLSCACertsDir, 0755); err != nil {
		return fmt.Errorf("failed to create org tlscacerts directory: %w", err)
	}

	// Find the first node's CA certificate to copy
	nodesDir := filepath.Join(cryptoDir, orgDirType, domain, nodeType)
	entries, err := os.ReadDir(nodesDir)
	if err != nil {
		return fmt.Errorf("failed to read %s directory: %w", nodeType, err)
	}

	if len(entries) == 0 {
		return fmt.Errorf("no %s nodes found in %s", nodeType, nodesDir)
	}

	// Use the first node's CA certificate for identity
	firstNodeDir := filepath.Join(nodesDir, entries[0].Name(), "msp", "cacerts")
	caCertFiles, err := os.ReadDir(firstNodeDir)
	if err != nil {
		return fmt.Errorf("failed to read node cacerts directory: %w", err)
	}

	if len(caCertFiles) == 0 {
		return fmt.Errorf("no CA certificate found in %s", firstNodeDir)
	}

	// Copy the CA certificate to org-level MSP with standard naming
	srcCACert := filepath.Join(firstNodeDir, caCertFiles[0].Name())
	dstCACert := filepath.Join(orgCACertsDir, fmt.Sprintf("ca.%s-cert.pem", domain))

	if err := g.copyFile(srcCACert, dstCACert); err != nil {
		return fmt.Errorf("failed to copy CA certificate: %w", err)
	}

	g.logDetails("  Copied CA cert: %s", dstCACert)

	// Copy TLS CA certificate from first node's TLS directory
	firstNodeTLSDir := filepath.Join(nodesDir, entries[0].Name(), "tls")
	tlsCACertSrc := filepath.Join(firstNodeTLSDir, "ca.crt")

	// Check if TLS CA cert exists
	if _, err := os.Stat(tlsCACertSrc); err == nil {
		tlsCACertDst := filepath.Join(orgTLSCACertsDir, fmt.Sprintf("tlsca.%s-cert.pem", domain))
		if err := g.copyFile(tlsCACertSrc, tlsCACertDst); err != nil {
			return fmt.Errorf("failed to copy TLS CA certificate: %w", err)
		}
		g.logDetails("  Copied TLS CA cert: %s", tlsCACertDst)
	} else {
		g.logDetails("  Warning: TLS CA certificate not found at %s", tlsCACertSrc)
	}

	// Generate config.yaml for the organization MSP
	if err := g.generateMSPConfig(orgMSPDir, domain); err != nil {
		return fmt.Errorf("failed to generate MSP config: %w", err)
	}

	g.logDetails("  Created org MSP: %s", orgMSPDir)
	g.logProgress("Created organization MSP for %s", domain)

	return nil
}

// copyFile copies a file from src to dst
func (g *FabricCAGenerator) copyFile(src, dst string) error {
	sourceFile, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("failed to open source file: %w", err)
	}
	defer sourceFile.Close()

	destFile, err := os.Create(dst)
	if err != nil {
		return fmt.Errorf("failed to create destination file: %w", err)
	}
	defer destFile.Close()

	if _, err := io.Copy(destFile, sourceFile); err != nil {
		return fmt.Errorf("failed to copy file content: %w", err)
	}

	// Sync to ensure data is written to disk
	if err := destFile.Sync(); err != nil {
		return fmt.Errorf("failed to sync file: %w", err)
	}

	return nil
}

// shouldShowDetails returns true if log level is verbose or debug
func (g *FabricCAGenerator) shouldShowDetails() bool {
	return g.logLevel == "verbose" || g.logLevel == "debug"
}

// shouldLog returns true if log level is not quiet
func (g *FabricCAGenerator) shouldLog() bool {
	return g.logLevel != "quiet"
}

// logProgress prints progress information if showProgress is enabled
// In TTY mode, displays a progress bar that updates on the same line
// In non-TTY mode (CI/CD), prints each step on a new line
func (g *FabricCAGenerator) logProgress(format string, args ...interface{}) {
	if !g.showProgress || g.totalSteps == 0 {
		return
	}

	g.currentStep++
	message := fmt.Sprintf(format, args...)

	if g.isTTY {
		// TTY mode: Show progress bar on same line
		g.printProgressBar(message)
	} else {
		// Non-TTY mode: Print each step on new line (for CI/CD logs)
		fmt.Printf("[%d/%d] %s\n", g.currentStep, g.totalSteps, message)
	}
}

// printProgressBar prints a visual progress bar with percentage and current operation
// Format: Progress: [████████████░░░░░░░░] 60% (29/48) orderer-consenter-3
func (g *FabricCAGenerator) printProgressBar(message string) {
	const barWidth = 20 // Width of the progress bar in characters

	// Calculate progress percentage
	percentage := float64(g.currentStep) / float64(g.totalSteps) * 100
	filled := int(float64(g.currentStep) / float64(g.totalSteps) * float64(barWidth))

	// Build progress bar
	bar := strings.Repeat("█", filled) + strings.Repeat("░", barWidth-filled)

	// Truncate message if too long (keep it under 30 chars for readability)
	displayMsg := message
	if len(displayMsg) > 30 {
		displayMsg = displayMsg[:27] + "..."
	}

	// Print progress bar with \r to overwrite previous line
	// Use \033[K to clear from cursor to end of line
	fmt.Printf("\rProgress: [%s] %3.0f%% (%d/%d) %s\033[K",
		bar, percentage, g.currentStep, g.totalSteps, displayMsg)

	// Flush output to ensure immediate display
	os.Stdout.Sync()

	// If this is the last step, print a newline
	if g.currentStep >= g.totalSteps {
		fmt.Println()
	}
}

// log prints a message for info level and above (not quiet)
// In TTY mode with progress bar, ensures proper line handling
func (g *FabricCAGenerator) log(format string, args ...interface{}) {
	if !g.shouldLog() {
		return
	}

	// In verbose/debug mode, show detailed logs
	// In info mode with progress bar, only show important messages
	if g.shouldShowDetails() {
		// If showing progress bar in TTY mode, clear the line first
		if g.showProgress && g.isTTY && g.currentStep > 0 {
			fmt.Print("\r" + strings.Repeat(" ", 120) + "\r")
		}

		fmt.Printf("  [fabric-ca] "+format+"\n", args...)

		// Redraw progress bar if active (not yet complete)
		if g.showProgress && g.isTTY && g.currentStep > 0 && g.currentStep < g.totalSteps {
			g.printProgressBar(fmt.Sprintf("Step %d", g.currentStep))
		}
	}
}

// logDetails prints a detailed message for verbose and debug levels
// In TTY mode with progress bar, ensures proper line handling
func (g *FabricCAGenerator) logDetails(format string, args ...interface{}) {
	if !g.shouldShowDetails() {
		return
	}

	// If showing progress bar in TTY mode, clear the line first
	if g.showProgress && g.isTTY && g.currentStep > 0 && g.currentStep < g.totalSteps {
		fmt.Print("\r" + strings.Repeat(" ", 120) + "\r")
	}

	fmt.Printf("  [fabric-ca] "+format+"\n", args...)

	// Redraw progress bar if active
	if g.showProgress && g.isTTY && g.currentStep > 0 && g.currentStep < g.totalSteps {
		g.printProgressBar(fmt.Sprintf("Step %d", g.currentStep))
	}
}

// filterDockerOutput filters Docker output based on log level
func (g *FabricCAGenerator) filterDockerOutput(output []byte) string {
	if g.logLevel == "debug" {
		// Debug mode: show all output
		return string(output)
	}

	// Filter out DEBUG logs for other log levels
	lines := string(output)
	var filtered []string
	for _, line := range strings.Split(lines, "\n") {
		// Skip KMS_SO DEBUG and GRPC-HELPER debug logs
		if strings.Contains(line, "[KMS_SO DEBUG]") || strings.Contains(line, "[GRPC-HELPER]") {
			continue
		}
		if line != "" {
			filtered = append(filtered, line)
		}
	}
	return strings.Join(filtered, "\n")
}

// readOrgCACerts reads and caches the organization CA certificates
// Returns CA cert data and TLS CA cert data, or nil if not found
func (g *FabricCAGenerator) readOrgCACerts(orgMSPDir string) ([]byte, []byte, error) {
	var caCertData, tlsCACertData []byte

	// Read CA certificate
	orgCACertsDir := filepath.Join(orgMSPDir, "cacerts")
	caCertFiles, err := os.ReadDir(orgCACertsDir)
	if err == nil && len(caCertFiles) > 0 {
		srcCACert := filepath.Join(orgCACertsDir, caCertFiles[0].Name())
		caCertData, err = os.ReadFile(srcCACert)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to read CA certificate: %w", err)
		}
	}

	// Read TLS CA certificate
	orgTLSCACertsDir := filepath.Join(orgMSPDir, "tlscacerts")
	tlsCACertFiles, err := os.ReadDir(orgTLSCACertsDir)
	if err == nil && len(tlsCACertFiles) > 0 {
		srcTLSCACert := filepath.Join(orgTLSCACertsDir, tlsCACertFiles[0].Name())
		tlsCACertData, err = os.ReadFile(srcTLSCACert)
		if err != nil {
			g.logDetails("Warning: failed to read TLS CA certificate: %v", err)
		}
	}

	return caCertData, tlsCACertData, nil
}

// generateUsersParallel generates user certificates in parallel using a worker pool
func (g *FabricCAGenerator) generateUsersParallel(domain, caURL, tokenLabel, defaultUserPin string,
	users []config.User, cryptoDir string, caCertData, tlsCACertData []byte) error {

	// Create semaphore for controlling concurrency
	semaphore := make(chan struct{}, g.maxConcurrency)

	// Error channel to collect errors from goroutines
	errChan := make(chan error, len(users))

	// WaitGroup to wait for all goroutines to complete
	var wg sync.WaitGroup

	// Launch goroutines for each user
	for _, user := range users {
		wg.Add(1)
		go func(u config.User) {
			defer wg.Done()

			// Acquire semaphore
			semaphore <- struct{}{}
			defer func() { <-semaphore }() // Release semaphore

			userNode := NodeInfo{
				Name:    u.Name,
				UserPin: defaultUserPin,
			}

			// Generate user certificate
			if err := g.generatePeerUserOptimized(domain, caURL, tokenLabel, userNode, cryptoDir, caCertData, tlsCACertData); err != nil {
				errChan <- fmt.Errorf("failed to generate crypto for user %s: %w", u.Name, err)
				return
			}
		}(user)
	}

	// Wait for all goroutines to complete
	wg.Wait()
	close(errChan)

	// Check for errors
	for err := range errChan {
		if err != nil {
			return err
		}
	}

	return nil
}

// generatePeerUserOptimized is an optimized version of GeneratePeerUser that uses cached CA certificates
func (g *FabricCAGenerator) generatePeerUserOptimized(domain, caURL, tokenLabel string, user NodeInfo,
	cryptoDir string, caCertData, tlsCACertData []byte) error {

	g.logDetails("  Generating user certificate for: %s@%s", user.Name, domain)

	// Create user directory: users/{username}@{domain}/msp
	userDir := filepath.Join(cryptoDir, "peerOrganizations", domain, "users", fmt.Sprintf("%s@%s", user.Name, domain), "msp")
	if err := os.MkdirAll(userDir, 0755); err != nil {
		return fmt.Errorf("failed to create user directory: %w", err)
	}

	// Use user-specific PIN if provided, otherwise use default
	userPin := user.UserPin
	if userPin == "" {
		userPin = "1234567" // Default PIN
	}

	// Run fabric-ca-client enroll for user (Docker or local)
	if err := g.runFabricCAClientEnroll(userDir, caURL, tokenLabel, userPin); err != nil {
		return fmt.Errorf("fabric-ca-client enroll failed for user %s: %w", user.Name, err)
	}

	g.logProgressSafe("Generated user certificate for %s@%s", user.Name, domain)

	// Rename signcerts/cert.pem to signcerts/{username}@{domain}-cert.pem
	signcertsDir := filepath.Join(userDir, "signcerts")
	srcCertPath := filepath.Join(signcertsDir, "cert.pem")
	userFQDN := fmt.Sprintf("%s@%s", user.Name, domain)
	dstCertPath := filepath.Join(signcertsDir, fmt.Sprintf("%s-cert.pem", userFQDN))

	if _, err := os.Stat(srcCertPath); err == nil {
		if err := os.Rename(srcCertPath, dstCertPath); err != nil {
			return fmt.Errorf("failed to rename certificate: %w", err)
		}
		g.logDetails("  Renamed certificate: %s -> %s", srcCertPath, dstCertPath)
	}

	// Rename cacerts CA certificate to standard format: ca.{domain}-cert.pem
	cacertsDir := filepath.Join(userDir, "cacerts")
	if entries, err := os.ReadDir(cacertsDir); err == nil && len(entries) > 0 {
		for _, entry := range entries {
			if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".pem") {
				srcCACertPath := filepath.Join(cacertsDir, entry.Name())
				dstCACertPath := filepath.Join(cacertsDir, fmt.Sprintf("ca.%s-cert.pem", domain))
				if entry.Name() != fmt.Sprintf("ca.%s-cert.pem", domain) {
					if err := os.Rename(srcCACertPath, dstCACertPath); err != nil {
						return fmt.Errorf("failed to rename CA certificate: %w", err)
					}
					g.logDetails("  Renamed CA certificate: %s -> %s", entry.Name(), fmt.Sprintf("ca.%s-cert.pem", domain))
				}
				break
			}
		}
	}

	// Create admincerts directory and copy the user certificate
	admincertsDir := filepath.Join(userDir, "admincerts")
	if err := os.MkdirAll(admincertsDir, 0755); err != nil {
		return fmt.Errorf("failed to create admincerts directory: %w", err)
	}

	adminCertPath := filepath.Join(admincertsDir, fmt.Sprintf("%s-cert.pem", userFQDN))
	if err := g.copyFile(dstCertPath, adminCertPath); err != nil {
		return fmt.Errorf("failed to copy certificate to admincerts: %w", err)
	}
	g.logDetails("  Created admincerts: %s", adminCertPath)

	// Use cached CA certificates if available, otherwise read from disk
	userCACertsDir := filepath.Join(userDir, "cacerts")
	if err := os.MkdirAll(userCACertsDir, 0755); err != nil {
		return fmt.Errorf("failed to create user cacerts directory: %w", err)
	}

	if caCertData != nil {
		// Write cached CA certificate
		dstCACert := filepath.Join(userCACertsDir, fmt.Sprintf("ca.%s-cert.pem", domain))
		if err := os.WriteFile(dstCACert, caCertData, 0644); err != nil {
			return fmt.Errorf("failed to write CA certificate: %w", err)
		}
		g.logDetails("  Wrote cached CA certificate to user MSP: %s", dstCACert)
	} else {
		// Fallback: copy from org-level MSP
		orgMSPDir := filepath.Join(cryptoDir, "peerOrganizations", domain, "msp")
		orgCACertsDir := filepath.Join(orgMSPDir, "cacerts")
		caCertFiles, err := os.ReadDir(orgCACertsDir)
		if err != nil {
			return fmt.Errorf("failed to read org cacerts directory: %w", err)
		}
		if len(caCertFiles) > 0 {
			srcCACert := filepath.Join(orgCACertsDir, caCertFiles[0].Name())
			dstCACert := filepath.Join(userCACertsDir, caCertFiles[0].Name())
			if err := g.copyFile(srcCACert, dstCACert); err != nil {
				return fmt.Errorf("failed to copy CA certificate to user MSP: %w", err)
			}
			g.logDetails("  Copied CA certificate to user MSP: %s", dstCACert)
		}
	}

	// Use cached TLS CA certificate if available
	userTLSCACertsDir := filepath.Join(userDir, "tlscacerts")
	if err := os.MkdirAll(userTLSCACertsDir, 0755); err != nil {
		return fmt.Errorf("failed to create user tlscacerts directory: %w", err)
	}

	if tlsCACertData != nil {
		// Write cached TLS CA certificate
		dstTLSCACert := filepath.Join(userTLSCACertsDir, fmt.Sprintf("tlsca.%s-cert.pem", domain))
		if err := os.WriteFile(dstTLSCACert, tlsCACertData, 0644); err != nil {
			return fmt.Errorf("failed to write TLS CA certificate: %w", err)
		}
		g.logDetails("  Wrote cached TLS CA certificate: %s", dstTLSCACert)
	} else {
		// Fallback: copy from org-level MSP
		orgMSPDir := filepath.Join(cryptoDir, "peerOrganizations", domain, "msp")
		orgTLSCACertsDir := filepath.Join(orgMSPDir, "tlscacerts")
		tlsCACertFiles, err := os.ReadDir(orgTLSCACertsDir)
		if err == nil && len(tlsCACertFiles) > 0 {
			srcTLSCACert := filepath.Join(orgTLSCACertsDir, tlsCACertFiles[0].Name())
			dstTLSCACert := filepath.Join(userTLSCACertsDir, tlsCACertFiles[0].Name())
			if err := g.copyFile(srcTLSCACert, dstTLSCACert); err != nil {
				return fmt.Errorf("failed to copy TLS CA certificate: %w", err)
			}
			g.logDetails("  Copied TLS CA certificate: %s", dstTLSCACert)
		}
	}

	// WORKAROUND: Create priv_sk marker file for user
	keystoreDir := filepath.Join(userDir, "keystore")
	privSkPath := filepath.Join(keystoreDir, "priv_sk")
	markerContent := []byte("# This is a marker file for PKCS11 mode\n# Actual private key is stored in KMS\n# SKI will be derived from the certificate\n")
	if err := os.WriteFile(privSkPath, markerContent, 0600); err != nil {
		return fmt.Errorf("failed to create priv_sk marker file for user: %w", err)
	}

	g.logDetails("  Created priv_sk marker file for user: %s", user.Name)

	// Generate config.yaml for the user's MSP directory
	if err := g.generateMSPConfig(userDir, domain); err != nil {
		return fmt.Errorf("failed to generate MSP config for user: %w", err)
	}

	g.logDetails("  Successfully generated certificate for user: %s@%s", user.Name, domain)
	return nil
}

// logProgressSafe is a thread-safe version of logProgress
func (g *FabricCAGenerator) logProgressSafe(format string, args ...interface{}) {
	if !g.showProgress || g.totalSteps == 0 {
		return
	}

	g.progressMutex.Lock()
	defer g.progressMutex.Unlock()

	g.currentStep++
	message := fmt.Sprintf(format, args...)

	if g.isTTY {
		// TTY mode: Show progress bar on same line
		g.printProgressBar(message)
	} else {
		// Non-TTY mode: Print each step on new line (for CI/CD logs)
		fmt.Printf("[%d/%d] %s\n", g.currentStep, g.totalSteps, message)
	}
}

// generateNodesParallel generates node certificates (identity + TLS) in parallel
func (g *FabricCAGenerator) generateNodesParallel(orgName, domain, caURL, tokenLabel string,
	nodes []NodeInfo, cryptoDir string, orgType string) error {

	// Create semaphore for controlling concurrency
	semaphore := make(chan struct{}, g.maxConcurrency)

	// Error channel to collect errors from goroutines
	errChan := make(chan error, len(nodes)*2) // *2 because each node has identity + TLS

	// WaitGroup to wait for all goroutines to complete
	var wg sync.WaitGroup

	// Launch goroutines for each node
	for _, node := range nodes {
		wg.Add(1)
		go func(n NodeInfo) {
			defer wg.Done()

			// Acquire semaphore
			semaphore <- struct{}{}
			defer func() { <-semaphore }() // Release semaphore

			// 1. Generate node identity certificate (using KMS)
			if err := g.generateNodeCrypto(orgName, domain, caURL, tokenLabel, n, cryptoDir, orgType); err != nil {
				errChan <- fmt.Errorf("failed to generate crypto for node %s: %w", n.Name, err)
				return
			}

			// 2. Generate node TLS certificate (software mode, no KMS)
			if err := g.GenerateNodeTLS(domain, caURL, n, cryptoDir, orgType); err != nil {
				errChan <- fmt.Errorf("failed to generate TLS for node %s: %w", n.Name, err)
				return
			}
		}(node)
	}

	// Wait for all goroutines to complete
	wg.Wait()
	close(errChan)

	// Check for errors
	for err := range errChan {
		if err != nil {
			return err
		}
	}

	return nil
}

// runFabricCAClientEnroll runs fabric-ca-client enroll command (Docker or local)
func (g *FabricCAGenerator) runFabricCAClientEnroll(outputDir, caURL, tokenLabel, userPin string) error {
	if g.config.Docker.UseLocalTools {
		return g.runFabricCAClientEnrollLocal(outputDir, caURL, tokenLabel, userPin)
	}
	return g.runFabricCAClientEnrollDocker(outputDir, caURL, tokenLabel, userPin)
}

// runFabricCAClientEnrollLocal runs fabric-ca-client enroll using local binary
// This function now uses the same approach as Docker version: envsubst + config template
// Reference: runFabricCAClientEnrollDocker which uses fabric-ca-client-config.yaml.tpl
func (g *FabricCAGenerator) runFabricCAClientEnrollLocal(outputDir, caURL, tokenLabel, userPin string) error {
	// Check if fabric-ca-client is available
	if _, err := exec.LookPath("fabric-ca-client"); err != nil {
		return fmt.Errorf("fabric-ca-client not found in PATH. Please install it or use Docker mode (set use_local_tools: false)")
	}

	g.logDetails("Generating crypto with KMS for local fabric-ca-client")
	g.logDetails("  - Output Dir: %s", outputDir)
	g.logDetails("  - CA URL: %s", caURL)
	g.logDetails("  - Token Label: %s", tokenLabel)
	g.logDetails("  - KMS Endpoint: %s", g.config.KMS.Endpoint)

	// Create temporary directory for config file
	tempDir, err := os.MkdirTemp("", "fabric-ca-client-*")
	if err != nil {
		return fmt.Errorf("failed to create temp directory: %w", err)
	}
	defer os.RemoveAll(tempDir) // Clean up temp directory

	configPath := filepath.Join(tempDir, "fabric-ca-client-config.yaml")

	// Use envsubst to generate config from template (same as Docker version)
	// The template file is located at /app/fabric-ca-client-config.yaml.tpl in cbdc-tool container
	templatePath := "/app/fabric-ca-client-config.yaml.tpl"
	envsubstCmd := exec.Command("sh", "-c",
		fmt.Sprintf("envsubst < %s > %s", templatePath, configPath))

	// Set environment variables for envsubst to substitute
	envsubstCmd.Env = append(os.Environ(),
		fmt.Sprintf("SIGN_KMS_ENDPOINT=%s", g.config.KMS.Endpoint),
		fmt.Sprintf("KMS_TOKEN_LABEL=%s", tokenLabel),
		fmt.Sprintf("KMS_USER_PIN=%s", userPin),
		fmt.Sprintf("CA_URL=%s", caURL),
	)

	g.logDetails("  Generating config from template using envsubst")

	// Execute envsubst to generate config file
	if output, err := envsubstCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to generate config from template: %w\nOutput: %s", err, string(output))
	}

	g.logDetails("  Created config file: %s", configPath)

	// Build fabric-ca-client command with -c parameter (same as Docker version)
	cmd := exec.Command("fabric-ca-client", "enroll",
		"-c", configPath,
		"--url", caURL,
		"--mspdir", outputDir,
	)

	// Set working directory to /app so that relative path ./libkms_pkcs11.so works
	// This matches the Docker version behavior where the working directory is /app
	cmd.Dir = "/app"

	// Set environment variables for KMS (needed by libkms_pkcs11.so)
	cmd.Env = append(os.Environ(),
		fmt.Sprintf("SIGN_KMS_ENDPOINT=%s", g.config.KMS.Endpoint),
		fmt.Sprintf("KMS_TOKEN_LABEL=%s", tokenLabel),
		fmt.Sprintf("KMS_USER_PIN=%s", userPin),
	)

	// Enable debug logging if needed
	if g.logLevel == "debug" {
		cmd.Env = append(cmd.Env,
			"KMS_SO_DEBUG=1",
			"GRPC_HELPER_DEBUG=1",
		)
	}

	g.logDetails("Running fabric-ca-client with KMS config: %v", cmd.Args)

	// Execute command
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("fabric-ca-client enroll failed: %w\nOutput: %s", err, string(output))
	}

	if g.shouldShowDetails() {
		fmt.Println(g.filterDockerOutput(output))
	}

	return nil
}

// runFabricCAClientEnrollDocker runs fabric-ca-client enroll using Docker
func (g *FabricCAGenerator) runFabricCAClientEnrollDocker(outputDir, caURL, tokenLabel, userPin string) error {
	args := []string{
		"run",
		"--rm",
		"-v", fmt.Sprintf("%s:/app/msp", outputDir),
		"-e", fmt.Sprintf("SIGN_KMS_ENDPOINT=%s", g.config.KMS.Endpoint),
		"-e", fmt.Sprintf("KMS_TOKEN_LABEL=%s", tokenLabel),
		"-e", fmt.Sprintf("KMS_USER_PIN=%s", userPin),
		"-e", fmt.Sprintf("CA_URL=%s", caURL),
	}

	if g.logLevel == "debug" {
		args = append(args, "-e", "KMS_SO_DEBUG=1")
		args = append(args, "-e", "GRPC_HELPER_DEBUG=1")
	}

	args = append(args,
		g.toolsImage,
		"sh", "-c",
		`envsubst < fabric-ca-client-config.yaml.tpl > fabric-ca-client-config.yaml && \
./fabric-ca-client enroll \
-c "./fabric-ca-client-config.yaml" \
--url "$CA_URL" \
--mspdir "./msp"`,
	)

	g.logDetails("Running: docker %v", args)

	cmd := exec.Command("docker", args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("fabric-ca-client enroll failed: %w\nOutput: %s", err, string(output))
	}

	if g.shouldShowDetails() {
		fmt.Println(g.filterDockerOutput(output))
	}

	return nil
}

// runFabricCAClientEnrollTLS runs fabric-ca-client enroll for TLS (Docker or local)
func (g *FabricCAGenerator) runFabricCAClientEnrollTLS(outputDir, caURL, nodeFQDN, csrHosts string) error {
	if g.config.Docker.UseLocalTools {
		return g.runFabricCAClientEnrollTLSLocal(outputDir, caURL, nodeFQDN, csrHosts)
	}
	return g.runFabricCAClientEnrollTLSDocker(outputDir, caURL, nodeFQDN, csrHosts)
}

// runFabricCAClientEnrollTLSLocal runs fabric-ca-client enroll for TLS using local binary
func (g *FabricCAGenerator) runFabricCAClientEnrollTLSLocal(outputDir, caURL, nodeFQDN, csrHosts string) error {
	// Check if fabric-ca-client is available
	if _, err := exec.LookPath("fabric-ca-client"); err != nil {
		return fmt.Errorf("fabric-ca-client not found in PATH. Please install it or use Docker mode (set use_local_tools: false)")
	}

	// Build command
	cmd := exec.Command("fabric-ca-client", "enroll",
		"-u", caURL,
		"-m", nodeFQDN,
		"--enrollment.profile", "tls",
		"--csr.hosts", csrHosts,
		"-M", outputDir,
	)

	g.logDetails("Running fabric-ca-client TLS locally: %v", cmd.Args)

	// Execute command
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("fabric-ca-client TLS enroll failed: %w\nOutput: %s", err, string(output))
	}

	if g.shouldShowDetails() {
		fmt.Println(g.filterDockerOutput(output))
	}

	return nil
}

// runFabricCAClientEnrollTLSDocker runs fabric-ca-client enroll for TLS using Docker
func (g *FabricCAGenerator) runFabricCAClientEnrollTLSDocker(outputDir, caURL, nodeFQDN, csrHosts string) error {
	args := []string{
		"run",
		"--rm",
		"-v", fmt.Sprintf("%s:/app/tls", outputDir),
		g.toolsImage,
		"sh", "-c",
		fmt.Sprintf(`./fabric-ca-client enroll \
-u "%s" \
-m "%s" \
--enrollment.profile tls \
--csr.hosts "%s" \
-M "./tls"`, caURL, nodeFQDN, csrHosts),
	}

	g.logDetails("Running: docker %v", args)

	cmd := exec.Command("docker", args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("fabric-ca-client TLS enroll failed: %w\nOutput: %s", err, string(output))
	}

	if g.shouldShowDetails() {
		fmt.Println(g.filterDockerOutput(output))
	}

	return nil
}
