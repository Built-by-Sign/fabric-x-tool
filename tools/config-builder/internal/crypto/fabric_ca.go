package crypto

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"config-builder/internal/config"
	"config-builder/internal/perms"
	"config-builder/internal/template"
	templatefiles "config-builder/templates"

	"golang.org/x/term"
)

// defaultTLSHostsSANs is the fallback SAN list applied to every node's TLS
// leaf certificate when no org-level tls_hosts is configured. Kept for
// dev-mode compatibility (Docker port-forwarding, loopback access); harmless
// in production where the real FQDN/IP comes from node.Host or org tls_hosts.
var defaultTLSHostsSANs = []string{"host.docker.internal", "0.0.0.0", "localhost", "127.0.0.1", "::1"}

// FabricCAGenerator handles certificate generation using Fabric CA client with KMS
type FabricCAGenerator struct {
	config         *config.NetworkConfig
	outputDir      string
	logLevel       string
	showProgress   bool
	currentStep    int
	totalSteps     int
	toolsImage     string     // Docker image containing fabric-ca-client and fabric-x-tool
	isTTY          bool       // Whether stdout is a TTY (for progress bar)
	progressMutex  sync.Mutex // Protects progress bar updates
	maxConcurrency int        // Maximum concurrent certificate generations
}

// NodeInfo contains information about a node for certificate generation
type NodeInfo struct {
	Name     string
	UserPin  string   // PIN for KMS access during enroll
	TLSHosts []string // Pre-resolved SAN list for the TLS leaf cert (FQDN + node.Host + defaults + org tls_hosts, deduped)
}

type fabricCAClientConfigData struct {
	Library string
	Label   string
	Pin     string
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
		g.totalSteps += (len(org.Peers)+len(g.committerPeerNamesForOrg(&org)))*2 + 1 + len(nonAdminUsers(org.Users)) + 1
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

	// Write the orderer-org TLS CA bundle to build/fabric-ca-root.pem.
	// The template engine's copyCommitterTLS step picks this up and drops a
	// copy into each committer-*/config/tls/. Replaces the cbdc-network
	// Makefile's fetch-fabric-ca-root target.
	if err := g.writeOrdererTLSBundleFile(); err != nil {
		return fmt.Errorf("failed to write orderer TLS bundle: %w", err)
	}

	g.log("Crypto materials generated successfully using Fabric CA")
	return nil
}

// writeOrdererTLSBundleFile concatenates every orderer-org's TLS CA chain
// and writes the bundle to build/fabric-ca-root.pem. The template engine's
// copyCommitterTLS step then drops a copy into each committer-*/config/tls/.
// Committers don't have a single trust anchor — they may talk to orderers
// across multiple orgs, so the union of all orderer-org TLS CAs is required.
func (g *FabricCAGenerator) writeOrdererTLSBundleFile() error {
	if g.config.TLS == nil || !g.config.TLS.Enabled {
		g.logDetails("TLS disabled; skipping orderer TLS bundle file")
		return nil
	}

	absOutputDir, err := filepath.Abs(g.outputDir)
	if err != nil {
		return fmt.Errorf("failed to get absolute output path: %w", err)
	}

	cryptoDir := filepath.Join(absOutputDir, "build", "config", "cryptogen-artifacts", "crypto")

	var bundle bytes.Buffer
	for _, org := range g.config.OrdererOrgs {
		// Bundle root + any intermediate TLS CAs for this org. Multi-tier CAs
		// leave the intermediate in tlsintermediatecerts/; one-tier CAs only
		// have tlscacerts/ and readPEMBundle silently skips the missing
		// intermediate dir.
		orgMSPDir := filepath.Join(cryptoDir, "ordererOrganizations", org.Domain, "msp")
		data, err := readPEMBundle(
			filepath.Join(orgMSPDir, "tlscacerts"),
			filepath.Join(orgMSPDir, "tlsintermediatecerts"),
		)
		if err != nil {
			return fmt.Errorf("read orderer org %s TLS CA chain: %w", org.Name, err)
		}
		if len(data) == 0 {
			return fmt.Errorf("orderer org %s: no TLS CA PEM under %s", org.Name, orgMSPDir)
		}
		bundle.Write(data)
		if !bytes.HasSuffix(data, []byte("\n")) {
			bundle.WriteByte('\n')
		}
	}

	if bundle.Len() == 0 {
		g.logDetails("No orderer-org TLS CA chains to bundle")
		return nil
	}

	dst := filepath.Join(absOutputDir, "build", template.FabricCARootPEMFilename)
	if err := os.MkdirAll(filepath.Dir(dst), perms.Dir); err != nil {
		return fmt.Errorf("create build dir: %w", err)
	}
	if err := os.WriteFile(dst, bundle.Bytes(), perms.FileCert); err != nil {
		return fmt.Errorf("write %s: %w", dst, err)
	}
	g.logDetails("Wrote orderer TLS bundle: %s (%d bytes)", dst, bundle.Len())
	return nil
}

// GenerateOrdererOrgCrypto generates crypto materials for an orderer organization
func (g *FabricCAGenerator) GenerateOrdererOrgCrypto(org config.OrdererOrg) error {
	g.log("Generating crypto for orderer org: %s", org.Name)

	mspCAURL, err := org.ResolveMSPCAURL(g.config.KMS.CAURL)
	if err != nil {
		return err
	}
	tlsCAURL, err := org.ResolveTLSCAURL(g.config.KMS.CAURL)
	if err != nil {
		return err
	}
	setupPin, err := org.ResolveSetupPin()
	if err != nil {
		return err
	}

	nodes := make([]NodeInfo, len(org.Orderers))
	for i, orderer := range org.Orderers {
		nodes[i] = NodeInfo{
			Name:     orderer.Name,
			UserPin:  setupPin,
			TLSHosts: config.ResolveTLSHosts(defaultTLSHostsSANs, org.TLSHosts, &org.Orderers[i], org.Domain),
		}
	}

	tokenLabel, err := g.config.ResolveKMSTokenLabel(org.KMSTokenLabel)
	if err != nil {
		return err
	}

	return g.GenerateOrgCryptoSplit(org.Name, org.Domain, mspCAURL, tlsCAURL, tokenLabel, nodes, "orderer")
}

// GeneratePeerOrgCrypto generates crypto materials for a peer organization
func (g *FabricCAGenerator) GeneratePeerOrgCrypto(org config.PeerOrg) error {
	g.log("Generating crypto for peer org: %s", org.Name)

	mspCAURL, err := org.ResolveMSPCAURL(g.config.KMS.CAURL)
	if err != nil {
		return err
	}
	tlsCAURL, err := org.ResolveTLSCAURL(g.config.KMS.CAURL)
	if err != nil {
		return err
	}
	setupPin, err := org.ResolveSetupPin()
	if err != nil {
		return err
	}

	nodes := make([]NodeInfo, len(org.Peers))
	for i, peer := range org.Peers {
		nodes[i] = NodeInfo{
			Name:     peer.Name,
			UserPin:  setupPin,
			TLSHosts: config.ResolveTLSHosts(defaultTLSHostsSANs, org.TLSHosts, &org.Peers[i], org.Domain),
		}
	}
	seenPeers := make(map[string]struct{}, len(nodes))
	for _, node := range nodes {
		seenPeers[node.Name] = struct{}{}
	}
	for _, committerName := range g.committerPeerNamesForOrg(&org) {
		if _, exists := seenPeers[committerName]; exists {
			continue
		}
		committerNode := config.Node{Name: committerName}
		nodes = append(nodes, NodeInfo{
			Name:     committerName,
			UserPin:  setupPin,
			TLSHosts: config.ResolveTLSHosts(defaultTLSHostsSANs, org.TLSHosts, &committerNode, org.Domain),
		})
	}

	tokenLabel, err := g.config.ResolveKMSTokenLabel(org.KMSTokenLabel)
	if err != nil {
		return err
	}

	if err := g.GenerateOrgCryptoSplit(org.Name, org.Domain, mspCAURL, tlsCAURL, tokenLabel, nodes, "peer"); err != nil {
		return err
	}

	// Generate crypto materials for users (Admin, channel_admin, endorser, etc.)
	users := nonAdminUsers(org.Users)
	if len(users) > 0 {
		g.log("Generating crypto for %d users in peer org: %s (concurrent mode with max %d workers)",
			len(users), org.Name, g.maxConcurrency)

		absOutputDir, err := filepath.Abs(g.outputDir)
		if err != nil {
			return fmt.Errorf("failed to get absolute output path: %w", err)
		}

		cryptoDir := filepath.Join(absOutputDir, "build", "config", "cryptogen-artifacts", "crypto")

		orgMSPDir := filepath.Join(cryptoDir, "peerOrganizations", org.Domain, "msp")
		caCertData, tlsCACertData, err := g.readOrgCACerts(orgMSPDir)
		if err != nil {
			g.logDetails("Warning: Could not pre-read CA certificates: %v (will read per-user)", err)
		}

		if err := g.generateUsersParallel(org.Domain, mspCAURL, tokenLabel,
			setupPin, users, cryptoDir, caCertData, tlsCACertData); err != nil {
			return err
		}
	}

	return nil
}

func nonAdminUsers(users []config.User) []config.User {
	filtered := make([]config.User, 0, len(users))
	for _, user := range users {
		if user.Name == "Admin" {
			continue
		}
		filtered = append(filtered, user)
	}
	return filtered
}

func (g *FabricCAGenerator) committerPeerNamesForOrg(org *config.PeerOrg) []string {
	if g.config.TLS != nil && g.config.TLS.Enabled {
		byOrg, err := g.config.ResolveCommitterCryptoIdentitiesByOrg()
		if err != nil {
			return nil
		}
		return byOrg[org.Name]
	}
	byOrg, err := g.config.ResolveSidecarIdentitiesByOrg()
	if err != nil {
		return nil
	}
	return byOrg[org.Name]
}

// GenerateOrgCryptoSplit generates crypto materials for an organization, routing
// MSP enroll and TLS leaf enroll to potentially different fabric-ca-servers.
// orgType should be "orderer" or "peer".
func (g *FabricCAGenerator) GenerateOrgCryptoSplit(orgName, domain, mspCAURL, tlsCAURL, tokenLabel string, nodes []NodeInfo, orgType string) error {
	if len(nodes) == 0 {
		return fmt.Errorf("%s org %s.%s requires at least one node identity", orgType, orgName, domain)
	}

	absOutputDir, err := filepath.Abs(g.outputDir)
	if err != nil {
		return fmt.Errorf("failed to get absolute output path: %w", err)
	}

	cryptoDir := filepath.Join(absOutputDir, "build", "config", "cryptogen-artifacts", "crypto")

	var orgDirType string
	if orgType == "peer" {
		orgDirType = "peerOrganizations"
	} else {
		orgDirType = "ordererOrganizations"
	}

	orgDir := filepath.Join(cryptoDir, orgDirType, domain)
	if err := os.MkdirAll(orgDir, perms.Dir); err != nil {
		return fmt.Errorf("failed to create org directory: %w", err)
	}

	// Node identity (MSP) + TLS leaf enrolls run in parallel; MSP goes through
	// mspCAURL, TLS goes through tlsCAURL. The two URLs may resolve to the
	// same fabric-ca-server (fallback mode) or to two independent ones.
	if err := g.generateNodesParallel(orgName, domain, mspCAURL, tlsCAURL, tokenLabel, nodes, cryptoDir, orgType); err != nil {
		return err
	}

	adminNode := NodeInfo{
		Name:    "Admin",
		UserPin: nodes[0].UserPin,
	}
	if err := g.GenerateAdminUser(domain, mspCAURL, tokenLabel, adminNode, cryptoDir, orgType); err != nil {
		return fmt.Errorf("failed to generate admin user: %w", err)
	}

	if err := g.createOrgMSP(domain, mspCAURL, tlsCAURL, cryptoDir, orgType); err != nil {
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
	if err := os.MkdirAll(nodeDir, perms.Dir); err != nil {
		return fmt.Errorf("failed to create node directory: %w", err)
	}

	if node.UserPin == "" {
		return fmt.Errorf("node %s.%s: kms user pin is empty", node.Name, domain)
	}
	userPin := node.UserPin

	// Run fabric-ca-client enroll (Docker or local)
	if err := g.runFabricCAClientEnroll(nodeDir, caURL, tokenLabel, userPin); err != nil {
		return fmt.Errorf("fabric-ca-client enroll failed for node %s: %w", node.Name, err)
	}

	g.logProgress("Generated identity certificate for %s.%s", node.Name, domain)

	// Rename signcerts/cert.pem to signcerts/{node}.{domain}-cert.pem
	// This is required by armageddon tool which expects the specific naming format
	nodeFQDN := fmt.Sprintf("%s.%s", node.Name, domain)
	if err := g.normalizeEnrolledMSP(nodeDir, domain, nodeFQDN); err != nil {
		return fmt.Errorf("failed to normalize node MSP: %w", err)
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
	if err := os.WriteFile(privSkPath, markerContent, perms.FilePrivateKey); err != nil {
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
// This uses software mode (no KMS) with --enrollment.profile tls.
// The TLS cert subject gets OU=<node-name>-tls (cosmetic / audit aid; not
// enforced by Fabric-X) and SAN comes from the pre-resolved node.TLSHosts.
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
	if err := os.MkdirAll(keysDir, perms.Dir); err != nil {
		return fmt.Errorf("failed to create keys directory: %w", err)
	}

	nodeFQDN := fmt.Sprintf("%s.%s", node.Name, domain)

	csrHostsList := node.TLSHosts
	if len(csrHostsList) == 0 {
		// Defensive fallback: caller should have populated TLSHosts already.
		csrHostsList = []string{nodeFQDN}
	}
	csrHosts := strings.Join(csrHostsList, ",")
	csrNames := fmt.Sprintf("OU=%s-tls", node.Name)

	// Run fabric-ca-client TLS enroll (Docker or local)
	// The enrollment creates: keys/node/{keystore,signcerts,cacerts}
	tlsTempDir := filepath.Join(keysDir, "node")
	if err := g.runFabricCAClientEnrollTLS(tlsTempDir, caURL, nodeFQDN, csrHosts, csrNames); err != nil {
		return fmt.Errorf("fabric-ca-client TLS enroll failed for node %s: %w", node.Name, err)
	}

	g.logProgress("Generated TLS certificate for %s.%s", node.Name, domain)

	// Rename and reorganize TLS files to standard format
	tlsDir := filepath.Join(nodeDir, "tls")
	if err := os.MkdirAll(tlsDir, perms.Dir); err != nil {
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
	if err := os.Chmod(dstKey, perms.FilePrivateKey); err != nil {
		return fmt.Errorf("failed to chmod TLS private key: %w", err)
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

	// Write the leaf TLS certificate only. The CA cert is written separately to
	// ca.crt and clients reference it through ca-cert-paths for chain verification.
	// Embedding the CA inside server.crt breaks Fabric-X's envelope TLS cert hash
	// check: that hash is computed over the full server.crt file, but the peer's
	// TLS handshake only presents the leaf cert, so claimed and actual hashes diverge.
	srcCert := filepath.Join(tlsTempDir, "signcerts", "cert.pem")
	dstCert := filepath.Join(tlsDir, "server.crt")
	if err := g.copyFile(srcCert, dstCert); err != nil {
		return fmt.Errorf("failed to copy TLS server certificate: %w", err)
	}

	g.logDetails("  Copied leaf TLS certificate to server.crt")

	// Also copy to keys directory for compatibility with gen_crypto.sh
	// gen_crypto.sh copies: ${dir}/node/keystore/* -> ${dir}/node.key
	//                       ${dir}/node/signcerts/cert.pem -> ${dir}/node.crt
	dstNodeKey := filepath.Join(keysDir, "node.key")
	if err := g.copyFile(srcKey, dstNodeKey); err != nil {
		return fmt.Errorf("failed to copy node.key: %w", err)
	}
	if err := os.Chmod(dstNodeKey, perms.FilePrivateKey); err != nil {
		return fmt.Errorf("failed to chmod node.key: %w", err)
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
	if err := os.MkdirAll(adminDir, perms.Dir); err != nil {
		return fmt.Errorf("failed to create admin directory: %w", err)
	}

	if admin.UserPin == "" {
		return fmt.Errorf("admin user @%s: kms user pin is empty", domain)
	}
	userPin := admin.UserPin

	// Run fabric-ca-client enroll for Admin (Docker or local)
	if err := g.runFabricCAClientEnroll(adminDir, caURL, tokenLabel, userPin); err != nil {
		return fmt.Errorf("fabric-ca-client enroll failed for Admin user: %w", err)
	}

	g.logProgress("Generated Admin user certificate for %s", domain)

	adminFQDN := fmt.Sprintf("Admin@%s", domain)
	if err := g.normalizeEnrolledMSP(adminDir, domain, adminFQDN); err != nil {
		return fmt.Errorf("failed to normalize Admin MSP: %w", err)
	}

	// WORKAROUND: Create priv_sk marker file for Admin user as well
	keystoreDir := filepath.Join(adminDir, "keystore")
	privSkPath := filepath.Join(keystoreDir, "priv_sk")
	markerContent := []byte("# This is a marker file for PKCS11 mode\n# Actual private key is stored in KMS\n# SKI will be derived from the certificate\n")
	if err := os.WriteFile(privSkPath, markerContent, perms.FilePrivateKey); err != nil {
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

// generateMSPConfig generates config.yaml for MSP with NodeOUs configuration.
//
// Fabric MSP NodeOUs require each OUIdentifier.Certificate to point at the
// CA that **directly** issues the role-bearing leaf certs. In a one-tier
// CA deployment that is the root in cacerts/. In a multi-tier deployment
// the leaf certs are signed by the intermediate CA, so the OUIdentifier
// must reference intermediatecerts/<intermediate>.pem — pointing at the
// root would make NodeOUs fail to recognise any role and downstream
// `.member` policies would evaluate as 0 satisfied sub-policies.
//
// We detect multi-tier by scanning the org-level intermediatecerts/
// directory (sibling of mspDir's enclosing org dir). The detection is
// best-effort and falls back to cacerts/ if no intermediate is found.
func (g *FabricCAGenerator) generateMSPConfig(mspDir, domain string) error {
	configPath := filepath.Join(mspDir, "config.yaml")

	ouCertPath := resolveNodeOUCertPath(mspDir, domain)

	// Generate NodeOUs configuration
	// Reference: Fabric MSP config with NodeOUs enabled
	// IMPORTANT: Use spaces for indentation, not tabs (YAML requirement)
	configContent := fmt.Sprintf(`NodeOUs:
  Enable: true
  ClientOUIdentifier:
    Certificate: %s
    OrganizationalUnitIdentifier: client
  PeerOUIdentifier:
    Certificate: %s
    OrganizationalUnitIdentifier: peer
  AdminOUIdentifier:
    Certificate: %s
    OrganizationalUnitIdentifier: admin
  OrdererOUIdentifier:
    Certificate: %s
    OrganizationalUnitIdentifier: orderer
`, ouCertPath, ouCertPath, ouCertPath, ouCertPath)

	if err := os.WriteFile(configPath, []byte(configContent), perms.FileConfig); err != nil {
		return fmt.Errorf("failed to write config.yaml: %w", err)
	}

	g.logDetails("  Generated MSP config.yaml: %s", configPath)
	return nil
}

// normalizeEnrolledMSP reshapes fabric-ca-client output to match cryptogen's
// filenames. Later config/copy logic expects this deterministic layout.
func (g *FabricCAGenerator) normalizeEnrolledMSP(mspDir, domain, certBaseName string) error {
	signcertsDir := filepath.Join(mspDir, "signcerts")
	srcCertPath := filepath.Join(signcertsDir, "cert.pem")
	dstCertPath := filepath.Join(signcertsDir, fmt.Sprintf("%s-cert.pem", certBaseName))

	if _, err := os.Stat(srcCertPath); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("certificate file not found: %s", srcCertPath)
		}
		return fmt.Errorf("failed to check certificate file: %w", err)
	}
	if err := os.Rename(srcCertPath, dstCertPath); err != nil {
		return fmt.Errorf("failed to rename certificate from %s to %s: %w", srcCertPath, dstCertPath, err)
	}
	g.logDetails("  Renamed certificate: %s -> %s", srcCertPath, dstCertPath)

	cacertsDir := filepath.Join(mspDir, "cacerts")
	entries, err := os.ReadDir(cacertsDir)
	if err != nil {
		return fmt.Errorf("failed to read cacerts directory: %w", err)
	}
	if len(entries) == 0 {
		return fmt.Errorf("no CA certificate found in %s", cacertsDir)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".pem") {
			continue
		}
		srcCACertPath := filepath.Join(cacertsDir, entry.Name())
		dstCACertPath := filepath.Join(cacertsDir, fmt.Sprintf("ca.%s-cert.pem", domain))
		if entry.Name() != fmt.Sprintf("ca.%s-cert.pem", domain) {
			if err := os.Rename(srcCACertPath, dstCACertPath); err != nil {
				return fmt.Errorf("failed to rename CA certificate from %s to %s: %w", srcCACertPath, dstCACertPath, err)
			}
			g.logDetails("  Renamed CA certificate: %s -> %s", entry.Name(), fmt.Sprintf("ca.%s-cert.pem", domain))
		}
		return nil
	}
	return fmt.Errorf("no CA certificate PEM found in %s", cacertsDir)
}

// createOrgMSP creates organization-level MSP directory structure by fetching
// the MSP CA chain from mspCAURL/cainfo and the TLS CA chain from tlsCAURL/cainfo.
//
// The returned chain may contain multiple certificates (root + intermediates)
// when the fabric-ca-server is itself an intermediate. We split self-signed
// roots into cacerts/ / tlscacerts/ and intermediates into intermediatecerts/
// / tlsintermediatecerts/ so that configtxgen / Fabric MSP loader / armageddon
// see a well-formed Fabric MSP layout and validate chains correctly.
//
// One-tier CAs (chain length 1, self-signed) produce no intermediate dirs,
// keeping the layout byte-identical to the previous single-file behaviour.
//
// After laying down the org-level MSP, the per-node and admin MSP directories
// produced by earlier enroll calls are synced from this single source of
// truth — fabric-ca-client only writes its direct issuer into those nodes'
// cacerts/, so any upstream root / intermediate would be missing without
// this sync.
func (g *FabricCAGenerator) createOrgMSP(domain, mspCAURL, tlsCAURL, cryptoDir string, orgType string) error {
	g.logDetails("Creating organization-level MSP for domain: %s", domain)

	var orgDirType string
	if orgType == "peer" {
		orgDirType = "peerOrganizations"
	} else {
		orgDirType = "ordererOrganizations"
	}

	orgMSPDir := filepath.Join(cryptoDir, orgDirType, domain, "msp")
	orgCACertsDir := filepath.Join(orgMSPDir, "cacerts")
	orgIntermediateCertsDir := filepath.Join(orgMSPDir, "intermediatecerts")
	orgTLSCACertsDir := filepath.Join(orgMSPDir, "tlscacerts")
	orgTLSIntermediateCertsDir := filepath.Join(orgMSPDir, "tlsintermediatecerts")

	mspChain, err := fetchFabricCAChain(mspCAURL)
	if err != nil {
		return fmt.Errorf("failed to fetch MSP CA chain from %s: %w", redactURL(mspCAURL), err)
	}
	// /cainfo on an intermediate fabric-ca-server returns only its parent
	// trust anchor — not its own intermediate cert. Harvest that cert from
	// any node MSP that fabric-ca-client just populated, so the chain
	// becomes complete before we split it.
	mspChain = mergeIssuerCertsFromNodes(mspChain, cryptoDir, orgDirType, domain, filepath.Join("msp", "cacerts"))
	if err := writeChainToMSPDirs(mspChain, orgCACertsDir, orgIntermediateCertsDir, fmt.Sprintf("ca.%s", domain)); err != nil {
		return fmt.Errorf("failed to write MSP CA chain: %w", err)
	}
	g.logDetails("  Wrote MSP CA chain into: %s (+ intermediatecerts if multi-tier)", orgCACertsDir)

	tlsChain, err := fetchFabricCAChain(tlsCAURL)
	if err != nil {
		return fmt.Errorf("failed to fetch TLS CA chain from %s: %w", redactURL(tlsCAURL), err)
	}
	// TLS counterpart: fabric-ca-client TLS enroll wrote the TLS issuer
	// cert into each node's tls/ca.crt.
	tlsChain = mergeIssuerCertsFromNodes(tlsChain, cryptoDir, orgDirType, domain, filepath.Join("tls", "ca.crt"))
	if err := writeChainToMSPDirs(tlsChain, orgTLSCACertsDir, orgTLSIntermediateCertsDir, fmt.Sprintf("tlsca.%s", domain)); err != nil {
		return fmt.Errorf("failed to write TLS CA chain: %w", err)
	}
	g.logDetails("  Wrote TLS CA chain into: %s (+ tlsintermediatecerts if multi-tier)", orgTLSCACertsDir)

	if err := g.generateMSPConfig(orgMSPDir, domain); err != nil {
		return fmt.Errorf("failed to generate MSP config: %w", err)
	}

	if err := g.syncNodeMSPsFromOrg(orgMSPDir, cryptoDir, domain, orgType); err != nil {
		return fmt.Errorf("failed to sync node MSPs from org-level: %w", err)
	}

	g.logDetails("  Created org MSP: %s", orgMSPDir)
	g.logProgress("Created organization MSP for %s", domain)

	return nil
}

// syncNodeMSPsFromOrg propagates the org-level CA trust material (cacerts /
// intermediatecerts / tlscacerts / tlsintermediatecerts) to every per-node
// and per-admin MSP under the same org, and refreshes each node's
// tls/ca.crt with the full TLS chain bundle.
//
// fabric-ca-client enroll only writes its direct issuer into a per-enroll
// cacerts/, which leaves multi-tier deployments missing the upstream root.
// Centralising the trust material here gives every consumer (Fabric MSP
// loader inside the node, configtxgen reading these dirs to build channel
// config MSPConfig.root_certs / intermediate_certs / tls_*_certs, and the
// embedded TLS verifier inside fabric-x components) a complete chain.
//
// Missing source subdirectories (e.g. intermediatecerts/ when the chain is
// one-tier) are silently skipped, so the one-tier CA layout passes through
// unchanged.
func (g *FabricCAGenerator) syncNodeMSPsFromOrg(orgMSPDir, cryptoDir, domain, orgType string) error {
	var orgDirType, nodeType string
	if orgType == "peer" {
		orgDirType = "peerOrganizations"
		nodeType = "peers"
	} else {
		orgDirType = "ordererOrganizations"
		nodeType = "orderers"
	}

	nodesParent := filepath.Join(cryptoDir, orgDirType, domain, nodeType)
	nodeMSPDirs := make([]string, 0)
	if entries, err := os.ReadDir(nodesParent); err == nil {
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			nodeMSPDirs = append(nodeMSPDirs, filepath.Join(nodesParent, e.Name(), "msp"))
		}
	}

	usersParent := filepath.Join(cryptoDir, orgDirType, domain, "users")
	if entries, err := os.ReadDir(usersParent); err == nil {
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			nodeMSPDirs = append(nodeMSPDirs, filepath.Join(usersParent, e.Name(), "msp"))
		}
	}

	srcCacerts := filepath.Join(orgMSPDir, "cacerts")
	srcIntermediates := filepath.Join(orgMSPDir, "intermediatecerts")
	srcTLSCacerts := filepath.Join(orgMSPDir, "tlscacerts")
	srcTLSIntermediates := filepath.Join(orgMSPDir, "tlsintermediatecerts")

	for _, mspDir := range nodeMSPDirs {
		if _, err := os.Stat(mspDir); err != nil {
			continue
		}
		// Overwrite cacerts/ — fabric-ca-client wrote only its direct issuer
		// here; we replace it with the canonical root from /cainfo.
		if err := g.replaceDirPEMs(srcCacerts, filepath.Join(mspDir, "cacerts")); err != nil {
			return fmt.Errorf("sync cacerts to %s: %w", mspDir, err)
		}
		if err := copyDirPEMs(srcIntermediates, filepath.Join(mspDir, "intermediatecerts")); err != nil {
			return fmt.Errorf("sync intermediatecerts to %s: %w", mspDir, err)
		}
		if err := copyDirPEMs(srcTLSCacerts, filepath.Join(mspDir, "tlscacerts")); err != nil {
			return fmt.Errorf("sync tlscacerts to %s: %w", mspDir, err)
		}
		if err := copyDirPEMs(srcTLSIntermediates, filepath.Join(mspDir, "tlsintermediatecerts")); err != nil {
			return fmt.Errorf("sync tlsintermediatecerts to %s: %w", mspDir, err)
		}
		// Re-generate config.yaml now that intermediatecerts/ exists in this
		// node MSP — the initial generateMSPConfig() call ran before sync and
		// could only point NodeOUs at cacerts/. With the intermediate now
		// present, resolveNodeOUCertPath picks it up and we get correct
		// multi-tier NodeOUs classification.
		if err := g.generateMSPConfig(mspDir, domain); err != nil {
			return fmt.Errorf("regenerate MSP config at %s: %w", mspDir, err)
		}
	}

	// Refresh each node's tls/ca.crt with the full TLS chain bundle so the
	// embedded TLS verifier accepts peer certs signed by the intermediate.
	for _, e := range mustReadDirOrEmpty(nodesParent) {
		if !e.IsDir() {
			continue
		}
		tlsCAPath := filepath.Join(nodesParent, e.Name(), "tls", "ca.crt")
		if _, err := os.Stat(tlsCAPath); err != nil {
			continue
		}
		bundle, err := readPEMBundle(srcTLSCacerts, srcTLSIntermediates)
		if err != nil {
			return fmt.Errorf("read TLS CA bundle for %s: %w", e.Name(), err)
		}
		if len(bundle) == 0 {
			continue
		}
		if err := os.WriteFile(tlsCAPath, bundle, perms.FileCert); err != nil {
			return fmt.Errorf("write %s: %w", tlsCAPath, err)
		}
	}

	return nil
}

// mustReadDirOrEmpty returns the dir entries or an empty slice if dir does
// not exist or cannot be read. Used as a defensive helper in code that
// iterates per-node directories that may legitimately be absent.
func mustReadDirOrEmpty(dir string) []os.DirEntry {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	return entries
}

// replaceDirPEMs deletes existing *.pem files in dstDir then copies *.pem
// files from srcDir. Used when the destination already contains stale
// material (e.g. node MSP cacerts/ written by fabric-ca-client that
// contains only the direct issuer).
func (g *FabricCAGenerator) replaceDirPEMs(srcDir, dstDir string) error {
	if entries, err := os.ReadDir(dstDir); err == nil {
		for _, e := range entries {
			if e.IsDir() || filepath.Ext(e.Name()) != ".pem" {
				continue
			}
			_ = os.Remove(filepath.Join(dstDir, e.Name()))
		}
	}
	return copyDirPEMs(srcDir, dstDir)
}

// fetchFabricCAChain hits a fabric-ca-server's /cainfo endpoint and returns
// the CAChain PEM (the server's TLS-signing CA root + intermediates).
// The caURL may include userinfo (admin:adminpw@...) — only the scheme/host
// portion is used for the HTTP request; userinfo is dropped from the request.
func fetchFabricCAChain(caURL string) ([]byte, error) {
	endpoint, err := buildCAInfoEndpoint(caURL)
	if err != nil {
		return nil, err
	}

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Get(endpoint)
	if err != nil {
		return nil, fmt.Errorf("GET %s: %w", endpoint, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: status %d", endpoint, resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read cainfo body: %w", err)
	}

	var payload struct {
		Result struct {
			CAChain string `json:"CAChain"`
		} `json:"result"`
		Errors []struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("parse cainfo JSON: %w (body=%q)", err, truncate(string(body), 200))
	}
	if len(payload.Errors) > 0 {
		return nil, fmt.Errorf("cainfo returned errors: %+v", payload.Errors)
	}
	if payload.Result.CAChain == "" {
		return nil, fmt.Errorf("cainfo missing CAChain field (body=%q)", truncate(string(body), 200))
	}

	chain, err := base64.StdEncoding.DecodeString(payload.Result.CAChain)
	if err != nil {
		return nil, fmt.Errorf("decode CAChain base64: %w", err)
	}
	if len(chain) == 0 {
		return nil, fmt.Errorf("decoded CAChain is empty")
	}
	// fabric-ca returns concatenated PEMs; ensure trailing newline for downstream consumers.
	if !bytes.HasSuffix(chain, []byte("\n")) {
		chain = append(chain, '\n')
	}
	return chain, nil
}

// buildCAInfoEndpoint normalizes a fabric-ca URL (possibly with userinfo)
// into a clean http(s)://host:port/cainfo URL.
func buildCAInfoEndpoint(caURL string) (string, error) {
	u, err := url.Parse(caURL)
	if err != nil {
		return "", fmt.Errorf("parse CA URL %q: %w", caURL, err)
	}
	if u.Scheme == "" || u.Host == "" {
		return "", fmt.Errorf("CA URL %q missing scheme or host", caURL)
	}
	clean := &url.URL{
		Scheme: u.Scheme,
		Host:   u.Host,
		Path:   "/cainfo",
	}
	return clean.String(), nil
}

func redactURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	if u.User != nil {
		u.User = url.User("***")
	}
	return u.String()
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// copyFile copies a file from src to dst
func (g *FabricCAGenerator) copyFile(src, dst string) error {
	sourceFile, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("failed to open source file: %w", err)
	}
	defer sourceFile.Close()

	if err := os.MkdirAll(filepath.Dir(dst), perms.Dir); err != nil {
		return fmt.Errorf("failed to create destination directory: %w", err)
	}

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

// generatePeerUserOptimized enrolls a peer user via fabric-ca using cached CA certificates
func (g *FabricCAGenerator) generatePeerUserOptimized(domain, caURL, tokenLabel string, user NodeInfo,
	cryptoDir string, caCertData, tlsCACertData []byte) error {

	g.logDetails("  Generating user certificate for: %s@%s", user.Name, domain)

	// Create user directory: users/{username}@{domain}/msp
	userDir := filepath.Join(cryptoDir, "peerOrganizations", domain, "users", fmt.Sprintf("%s@%s", user.Name, domain), "msp")
	if err := os.MkdirAll(userDir, perms.Dir); err != nil {
		return fmt.Errorf("failed to create user directory: %w", err)
	}

	if user.UserPin == "" {
		return fmt.Errorf("user %s@%s: kms user pin is empty", user.Name, domain)
	}
	userPin := user.UserPin

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
	if err := os.MkdirAll(admincertsDir, perms.Dir); err != nil {
		return fmt.Errorf("failed to create admincerts directory: %w", err)
	}

	adminCertPath := filepath.Join(admincertsDir, fmt.Sprintf("%s-cert.pem", userFQDN))
	if err := g.copyFile(dstCertPath, adminCertPath); err != nil {
		return fmt.Errorf("failed to copy certificate to admincerts: %w", err)
	}
	g.logDetails("  Created admincerts: %s", adminCertPath)

	// Mirror the org-level MSP trust material into the user MSP. We copy the
	// whole directories (cacerts / intermediatecerts / tlscacerts /
	// tlsintermediatecerts) rather than a single cached cert so multi-tier
	// CA deployments end up with the full chain; one-tier CAs degrade
	// naturally since the intermediate dirs simply do not exist.
	//
	// The legacy caCertData / tlsCACertData arguments are kept for signature
	// compatibility with the worker pool but no longer drive layout: the
	// org-level MSP (written by createOrgMSP /cainfo before users run) is
	// the single source of truth.
	_ = caCertData
	_ = tlsCACertData

	orgMSPDir := filepath.Join(cryptoDir, "peerOrganizations", domain, "msp")
	for _, sub := range []string{"cacerts", "intermediatecerts", "tlscacerts", "tlsintermediatecerts"} {
		if err := copyDirPEMs(filepath.Join(orgMSPDir, sub), filepath.Join(userDir, sub)); err != nil {
			return fmt.Errorf("copy %s to user MSP: %w", sub, err)
		}
	}

	// WORKAROUND: Create priv_sk marker file for user
	keystoreDir := filepath.Join(userDir, "keystore")
	privSkPath := filepath.Join(keystoreDir, "priv_sk")
	markerContent := []byte("# This is a marker file for PKCS11 mode\n# Actual private key is stored in KMS\n# SKI will be derived from the certificate\n")
	if err := os.WriteFile(privSkPath, markerContent, perms.FilePrivateKey); err != nil {
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

// generateNodesParallel generates node certificates (identity + TLS) in parallel.
// MSP enroll uses mspCAURL with the KMS-backed PIN; TLS leaf enroll uses
// tlsCAURL with a software-generated keypair (no KMS).
func (g *FabricCAGenerator) generateNodesParallel(orgName, domain, mspCAURL, tlsCAURL, tokenLabel string,
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

			// 1. Generate node identity certificate (using KMS via MSP CA)
			if err := g.generateNodeCrypto(orgName, domain, mspCAURL, tokenLabel, n, cryptoDir, orgType); err != nil {
				errChan <- fmt.Errorf("failed to generate crypto for node %s: %w", n.Name, err)
				return
			}

			// 2. Generate node TLS certificate (software mode, via TLS CA)
			if err := g.GenerateNodeTLS(domain, tlsCAURL, n, cryptoDir, orgType); err != nil {
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

// runFabricCAClientEnrollLocal runs fabric-ca-client enroll using local binary.
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
	if err := g.writeFabricCAClientConfig(configPath, tokenLabel, userPin); err != nil {
		return err
	}

	g.logDetails("  Created config file: %s", configPath)

	// Build fabric-ca-client command with -c parameter (same as Docker version)
	cmd := exec.Command("fabric-ca-client", "enroll",
		"-c", configPath,
		"--url", caURL,
		"--mspdir", outputDir,
	)

	// Set working directory to /app when running inside the fabric-x-tool image
	// so the default relative PKCS11 library path resolves like Docker mode.
	if _, err := os.Stat("/app"); err == nil {
		cmd.Dir = "/app"
	}

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
	tempDir, err := os.MkdirTemp("", "fabric-ca-client-config-*")
	if err != nil {
		return fmt.Errorf("failed to create fabric-ca-client config directory: %w", err)
	}
	defer os.RemoveAll(tempDir)

	configPath := filepath.Join(tempDir, "fabric-ca-client-config.yaml")
	if err := g.writeFabricCAClientConfig(configPath, tokenLabel, userPin); err != nil {
		return err
	}

	args := []string{
		"run",
		"--rm",
		"-v", fmt.Sprintf("%s:/app/msp", outputDir),
		"-v", fmt.Sprintf("%s:/app/config:ro", tempDir),
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
		`./fabric-ca-client enroll \
-c "/app/config/fabric-ca-client-config.yaml" \
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

func (g *FabricCAGenerator) writeFabricCAClientConfig(configPath, tokenLabel, userPin string) error {
	tmpl, err := templatefiles.Parse("fabricca/fabric-ca-client-config.yaml.tmpl", nil)
	if err != nil {
		return fmt.Errorf("failed to parse fabric-ca-client config template: %w", err)
	}

	data := fabricCAClientConfigData{
		Library: g.fabricCAPKCS11Library(),
		Label:   tokenLabel,
		Pin:     userPin,
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return fmt.Errorf("failed to render fabric-ca-client config template: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(configPath), perms.Dir); err != nil {
		return fmt.Errorf("failed to create fabric-ca-client config directory: %w", err)
	}
	if err := os.WriteFile(configPath, buf.Bytes(), perms.FileSecret); err != nil {
		return fmt.Errorf("failed to write fabric-ca-client config: %w", err)
	}
	return nil
}

func (g *FabricCAGenerator) fabricCAPKCS11Library() string {
	if library := os.Getenv("FABRIC_CA_CLIENT_PKCS11_LIBRARY"); library != "" {
		return library
	}
	return "./libkms_pkcs11.so"
}

// runFabricCAClientEnrollTLS runs fabric-ca-client enroll for TLS (Docker or local)
func (g *FabricCAGenerator) runFabricCAClientEnrollTLS(outputDir, caURL, nodeFQDN, csrHosts, csrNames string) error {
	if g.config.Docker.UseLocalTools {
		return g.runFabricCAClientEnrollTLSLocal(outputDir, caURL, nodeFQDN, csrHosts, csrNames)
	}
	return g.runFabricCAClientEnrollTLSDocker(outputDir, caURL, nodeFQDN, csrHosts, csrNames)
}

// runFabricCAClientEnrollTLSLocal runs fabric-ca-client enroll for TLS using local binary
func (g *FabricCAGenerator) runFabricCAClientEnrollTLSLocal(outputDir, caURL, nodeFQDN, csrHosts, csrNames string) error {
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
		"--csr.names", csrNames,
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
func (g *FabricCAGenerator) runFabricCAClientEnrollTLSDocker(outputDir, caURL, nodeFQDN, csrHosts, csrNames string) error {
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
--csr.names "%s" \
-M "./tls"`, caURL, nodeFQDN, csrHosts, csrNames),
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
