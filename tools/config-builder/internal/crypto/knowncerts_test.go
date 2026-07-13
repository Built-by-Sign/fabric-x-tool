package crypto

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"config-builder/internal/config"
)

func TestPopulateKnownCertsUsesConfiguredSigningIdentities(t *testing.T) {
	outputDir := t.TempDir()
	cfg := &config.NetworkConfig{
		OrdererOrgs: []config.OrdererOrg{{
			Name:     "OrdererOrg1",
			Domain:   "orderer.example.com",
			Orderers: []config.Node{{Name: "orderer-router-1"}},
		}},
		PeerOrgs: []config.PeerOrg{{
			Name:   "Org1",
			Domain: "org1.example.com",
			Peers:  []config.Node{{Name: "peer0"}},
			Users: []config.User{
				{Name: "Admin"},
				{Name: "User1"},
				{Name: "channel_admin"},
				{Name: "endorser"},
			},
		}},
		Committer: &config.CommitterConfig{
			Components: []config.CommitterNode{{Name: "committer-sidecar", Type: "sidecar"}},
		},
	}

	cryptoDir := filepath.Join(outputDir, "build", "config", "cryptogen-artifacts", "crypto")
	writeTestSignCert(t, filepath.Join(cryptoDir, "ordererOrganizations", "orderer.example.com", "orderers", "orderer-router-1.orderer.example.com", "msp", "signcerts", "orderer-router-1.orderer.example.com-cert.pem"), "orderer-router-1")
	writeTestSignCert(t, filepath.Join(cryptoDir, "peerOrganizations", "org1.example.com", "peers", "peer0.org1.example.com", "msp", "signcerts", "peer0.org1.example.com-cert.pem"), "peer0")
	writeTestSignCert(t, filepath.Join(cryptoDir, "peerOrganizations", "org1.example.com", "peers", "committer-sidecar.org1.example.com", "msp", "signcerts", "committer-sidecar.org1.example.com-cert.pem"), "committer-sidecar")
	for _, user := range cfg.PeerOrgs[0].Users {
		fqdn := user.Name + "@org1.example.com"
		writeTestSignCert(t, filepath.Join(cryptoDir, "peerOrganizations", "org1.example.com", "users", fqdn, "msp", "signcerts", fqdn+"-cert.pem"), user.Name)
	}

	peerKnownCerts := filepath.Join(cryptoDir, "peerOrganizations", "org1.example.com", "msp", "knowncerts")
	if err := os.MkdirAll(peerKnownCerts, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(peerKnownCerts, "stale-cert.pem"), []byte("stale"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := PopulateKnownCerts(cfg, outputDir); err != nil {
		t.Fatalf("PopulateKnownCerts: %v", err)
	}

	assertDirectoryEntries(t, filepath.Join(cryptoDir, "ordererOrganizations", "orderer.example.com", "msp", "knowncerts"), []string{
		"orderer-router-1.orderer.example.com-cert.pem",
	})
	assertDirectoryEntries(t, peerKnownCerts, []string{
		"Admin@org1.example.com-cert.pem",
		"User1@org1.example.com-cert.pem",
		"channel_admin@org1.example.com-cert.pem",
		"committer-sidecar.org1.example.com-cert.pem",
		"endorser@org1.example.com-cert.pem",
		"peer0.org1.example.com-cert.pem",
	})

	source := filepath.Join(cryptoDir, "peerOrganizations", "org1.example.com", "users", "endorser@org1.example.com", "msp", "signcerts", "endorser@org1.example.com-cert.pem")
	destination := filepath.Join(peerKnownCerts, "endorser@org1.example.com-cert.pem")
	if certificateID(t, source) != certificateID(t, destination) {
		t.Fatal("known certificate does not preserve the FSC CertificateId hash")
	}
}

func TestPopulateKnownCertsRejectsMissingConfiguredIdentity(t *testing.T) {
	cfg := &config.NetworkConfig{PeerOrgs: []config.PeerOrg{{
		Name:   "Org1",
		Domain: "org1.example.com",
		Peers:  []config.Node{{Name: "peer0"}},
	}}}

	err := PopulateKnownCerts(cfg, t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "peer0.org1.example.com") {
		t.Fatalf("expected missing peer identity error, got %v", err)
	}
}

func TestPopulateKnownCertsRejectsInvalidCertificate(t *testing.T) {
	outputDir := t.TempDir()
	cfg := &config.NetworkConfig{PeerOrgs: []config.PeerOrg{{
		Name:   "Org1",
		Domain: "org1.example.com",
		Users:  []config.User{{Name: "endorser"}},
	}}}
	path := filepath.Join(outputDir, "build", "config", "cryptogen-artifacts", "crypto", "peerOrganizations", "org1.example.com", "users", "endorser@org1.example.com", "msp", "signcerts", "endorser@org1.example.com-cert.pem")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("not a certificate"), 0o644); err != nil {
		t.Fatal(err)
	}

	err := PopulateKnownCerts(cfg, outputDir)
	if err == nil || !strings.Contains(err.Error(), "invalid certificate") {
		t.Fatalf("expected invalid certificate error, got %v", err)
	}
}

func writeTestSignCert(t *testing.T, path, commonName string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: commonName},
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o644); err != nil {
		t.Fatal(err)
	}
}

func assertDirectoryEntries(t *testing.T, dir string, want []string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != len(want) {
		t.Fatalf("%s entries = %v, want %v", dir, entryNames(entries), want)
	}
	for i, entry := range entries {
		if entry.Name() != want[i] {
			t.Fatalf("%s entries = %v, want %v", dir, entryNames(entries), want)
		}
	}
}

func entryNames(entries []os.DirEntry) []string {
	names := make([]string, len(entries))
	for i, entry := range entries {
		names[i] = entry.Name()
	}
	return names
}

func certificateID(t *testing.T, path string) [sha256.Size]byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode(data)
	if block == nil {
		t.Fatalf("decode certificate %s", path)
	}
	return sha256.Sum256(block.Bytes)
}
