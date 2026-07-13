package crypto

import (
	"bytes"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"config-builder/internal/config"
	"config-builder/internal/perms"
)

type knownCertSource struct {
	identity string
	path     string
}

type knownCert struct {
	name string
	data []byte
	der  []byte
}

// PopulateKnownCerts collects generated signing certificates into each
// organization MSP before configtxgen builds the genesis block.
func PopulateKnownCerts(cfg *config.NetworkConfig, outputDir string) error {
	absOutputDir, err := filepath.Abs(outputDir)
	if err != nil {
		return fmt.Errorf("resolve output directory: %w", err)
	}
	cryptoDir := filepath.Join(absOutputDir, "build", "config", "cryptogen-artifacts", "crypto")

	for _, org := range cfg.OrdererOrgs {
		sources := make([]knownCertSource, 0, len(org.Orderers))
		for _, node := range org.Orderers {
			fqdn := fmt.Sprintf("%s.%s", node.Name, org.Domain)
			sources = append(sources, knownCertSource{
				identity: fqdn,
				path: filepath.Join(cryptoDir, "ordererOrganizations", org.Domain,
					"orderers", fqdn, "msp", "signcerts", fqdn+"-cert.pem"),
			})
		}
		orgMSPDir := filepath.Join(cryptoDir, "ordererOrganizations", org.Domain, "msp")
		if err := populateOrganizationKnownCerts("orderer", org.Name, orgMSPDir, sources); err != nil {
			return err
		}
	}

	sidecarsByOrg, err := cfg.ResolveSidecarIdentitiesByOrg()
	if err != nil {
		return fmt.Errorf("resolve committer sidecar identities: %w", err)
	}
	for _, org := range cfg.PeerOrgs {
		sources := make([]knownCertSource, 0, len(org.Peers)+len(sidecarsByOrg[org.Name])+len(org.Users))
		for _, node := range org.Peers {
			sources = append(sources, peerKnownCertSource(cryptoDir, org.Domain, node.Name))
		}
		for _, identity := range sidecarsByOrg[org.Name] {
			sources = append(sources, peerKnownCertSource(cryptoDir, org.Domain, identity))
		}
		for _, user := range org.Users {
			fqdn := fmt.Sprintf("%s@%s", user.Name, org.Domain)
			sources = append(sources, knownCertSource{
				identity: fqdn,
				path: filepath.Join(cryptoDir, "peerOrganizations", org.Domain,
					"users", fqdn, "msp", "signcerts", fqdn+"-cert.pem"),
			})
		}
		orgMSPDir := filepath.Join(cryptoDir, "peerOrganizations", org.Domain, "msp")
		if err := populateOrganizationKnownCerts("peer", org.Name, orgMSPDir, sources); err != nil {
			return err
		}
	}

	return nil
}

func peerKnownCertSource(cryptoDir, domain, identity string) knownCertSource {
	fqdn := fmt.Sprintf("%s.%s", identity, domain)
	return knownCertSource{
		identity: fqdn,
		path: filepath.Join(cryptoDir, "peerOrganizations", domain,
			"peers", fqdn, "msp", "signcerts", fqdn+"-cert.pem"),
	}
}

func populateOrganizationKnownCerts(orgType, orgName, orgMSPDir string, sources []knownCertSource) error {
	certsByName := make(map[string]knownCert, len(sources))
	for _, source := range sources {
		cert, err := readKnownCert(source)
		if err != nil {
			return fmt.Errorf("%s org %s identity %s: %w", orgType, orgName, source.identity, err)
		}
		if existing, ok := certsByName[cert.name]; ok {
			if !bytes.Equal(existing.der, cert.der) {
				return fmt.Errorf("%s org %s: conflicting known certificate filename %s", orgType, orgName, cert.name)
			}
			continue
		}
		certsByName[cert.name] = cert
	}

	certs := make([]knownCert, 0, len(certsByName))
	for _, cert := range certsByName {
		certs = append(certs, cert)
	}
	sort.Slice(certs, func(i, j int) bool { return certs[i].name < certs[j].name })

	knownCertsDir := filepath.Join(orgMSPDir, "knowncerts")
	if err := os.RemoveAll(knownCertsDir); err != nil {
		return fmt.Errorf("remove stale known certificates from %s: %w", knownCertsDir, err)
	}
	if err := os.MkdirAll(knownCertsDir, perms.Dir); err != nil {
		return fmt.Errorf("create known certificates directory %s: %w", knownCertsDir, err)
	}
	for _, cert := range certs {
		destination := filepath.Join(knownCertsDir, cert.name)
		if err := os.WriteFile(destination, cert.data, perms.FileCert); err != nil {
			return fmt.Errorf("write known certificate %s: %w", destination, err)
		}
	}
	return nil
}

func readKnownCert(source knownCertSource) (knownCert, error) {
	data, err := os.ReadFile(source.path)
	if err != nil {
		return knownCert{}, fmt.Errorf("read signing certificate %s: %w", source.path, err)
	}
	block, rest := pem.Decode(data)
	if block == nil || block.Type != "CERTIFICATE" || len(bytes.TrimSpace(rest)) != 0 {
		return knownCert{}, fmt.Errorf("invalid certificate PEM in %s", source.path)
	}
	if _, err := x509.ParseCertificate(block.Bytes); err != nil {
		return knownCert{}, fmt.Errorf("invalid certificate in %s: %w", source.path, err)
	}
	return knownCert{
		name: filepath.Base(source.path),
		data: data,
		der:  block.Bytes,
	}, nil
}
