package crypto

import (
	"github.com/ethsign/cbdc-chain/cbdc-network/config-builder/internal/config"
)

// CryptoGenerator is the interface for generating crypto materials
// It abstracts the underlying implementation (cryptogen vs fabric-ca-client)
type CryptoGenerator interface {
	// Generate generates all crypto materials for the network
	Generate() error

	// GenerateOrdererOrgCrypto generates crypto materials for an orderer organization
	GenerateOrdererOrgCrypto(org config.OrdererOrg) error

	// GeneratePeerOrgCrypto generates crypto materials for a peer organization
	GeneratePeerOrgCrypto(org config.PeerOrg) error
}

// NewCryptoGenerator creates a new crypto generator based on the network configuration
// If KMS is enabled, it returns a FabricCAGenerator that uses fabric-ca-client with KMS
// Otherwise, it returns the standard Generator that uses cryptogen
func NewCryptoGenerator(cfg *config.NetworkConfig, outputDir string, logLevel string, showProgress bool) CryptoGenerator {
	// Check if KMS is enabled
	if cfg.KMS != nil && cfg.KMS.Enabled {
		// Use Fabric CA with KMS for certificate generation
		return NewFabricCAGenerator(cfg, outputDir, logLevel, showProgress)
	}

	// Use standard cryptogen for certificate generation
	verbose := logLevel == "verbose" || logLevel == "debug"
	return NewGenerator(cfg, outputDir, verbose)
}

// Ensure Generator implements CryptoGenerator interface
var _ CryptoGenerator = (*Generator)(nil)

// GenerateOrdererOrgCrypto generates crypto materials for an orderer organization
// This is a new method to satisfy the CryptoGenerator interface
func (g *Generator) GenerateOrdererOrgCrypto(org config.OrdererOrg) error {
	// For cryptogen-based generation, we generate all orgs at once
	// So this method just calls the full Generate() method
	// In a more sophisticated implementation, we could generate per-org
	return g.Generate()
}

// GeneratePeerOrgCrypto generates crypto materials for a peer organization
// This is a new method to satisfy the CryptoGenerator interface
func (g *Generator) GeneratePeerOrgCrypto(org config.PeerOrg) error {
	// For cryptogen-based generation, we generate all orgs at once
	// So this method just calls the full Generate() method
	// In a more sophisticated implementation, we could generate per-org
	return g.Generate()
}

// Ensure FabricCAGenerator implements CryptoGenerator interface
var _ CryptoGenerator = (*FabricCAGenerator)(nil)
