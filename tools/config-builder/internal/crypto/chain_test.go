package crypto

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// makeCert produces a PEM-encoded X.509 cert signed by signer (self-signed
// when signer == nil). Subject identifies the role for split assertions.
func makeCert(t *testing.T, subject string, signer *signerInfo) ([]byte, *signerInfo) {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("genkey: %v", err)
	}

	tpl := &x509.Certificate{
		SerialNumber:          big.NewInt(time.Now().UnixNano()),
		Subject:               pkix.Name{CommonName: subject},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}

	parent := tpl
	parentKey := key
	if signer != nil {
		parent = signer.cert
		parentKey = signer.key
	}

	der, err := x509.CreateCertificate(rand.Reader, tpl, parent, &key.PublicKey, parentKey)
	if err != nil {
		t.Fatalf("sign cert (%s): %v", subject, err)
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	parsed, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return pemBytes, &signerInfo{cert: parsed, key: key, pem: pemBytes}
}

type signerInfo struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
	pem  []byte
}

func TestSplitChainPEMs_OneTier(t *testing.T) {
	rootPEM, _ := makeCert(t, "root", nil)

	roots, intermediates, err := splitChainPEMs(rootPEM)
	if err != nil {
		t.Fatalf("split: %v", err)
	}
	if len(roots) != 1 {
		t.Fatalf("roots: want 1 got %d", len(roots))
	}
	if len(intermediates) != 0 {
		t.Fatalf("intermediates: want 0 got %d", len(intermediates))
	}
}

func TestSplitChainPEMs_TwoTier(t *testing.T) {
	rootPEM, rootSigner := makeCert(t, "root", nil)
	intermediatePEM, _ := makeCert(t, "intermediate", rootSigner)

	chain := append(append([]byte{}, intermediatePEM...), rootPEM...)
	roots, intermediates, err := splitChainPEMs(chain)
	if err != nil {
		t.Fatalf("split: %v", err)
	}
	if len(roots) != 1 {
		t.Fatalf("roots: want 1 got %d", len(roots))
	}
	if len(intermediates) != 1 {
		t.Fatalf("intermediates: want 1 got %d", len(intermediates))
	}
	if !strings.Contains(string(roots[0]), "BEGIN CERTIFICATE") {
		t.Fatalf("root PEM malformed: %s", roots[0])
	}
}

func TestWriteChainToMSPDirs_OneTier_NoIntermediateDir(t *testing.T) {
	rootPEM, _ := makeCert(t, "root", nil)

	dir := t.TempDir()
	rootDir := filepath.Join(dir, "cacerts")
	intermediateDir := filepath.Join(dir, "intermediatecerts")

	if err := writeChainToMSPDirs(rootPEM, rootDir, intermediateDir, "ca.example.com"); err != nil {
		t.Fatalf("write: %v", err)
	}

	if _, err := os.Stat(filepath.Join(rootDir, "ca.example.com-cert.pem")); err != nil {
		t.Fatalf("root cert missing: %v", err)
	}
	if _, err := os.Stat(intermediateDir); !os.IsNotExist(err) {
		t.Fatalf("intermediate dir should not exist for one-tier CA, got err=%v", err)
	}
}

func TestWriteChainToMSPDirs_TwoTier_SplitsCorrectly(t *testing.T) {
	rootPEM, rootSigner := makeCert(t, "root", nil)
	intermediatePEM, _ := makeCert(t, "intermediate", rootSigner)
	chain := append(append([]byte{}, intermediatePEM...), rootPEM...)

	dir := t.TempDir()
	rootDir := filepath.Join(dir, "cacerts")
	intermediateDir := filepath.Join(dir, "intermediatecerts")

	if err := writeChainToMSPDirs(chain, rootDir, intermediateDir, "ca.example.com"); err != nil {
		t.Fatalf("write: %v", err)
	}

	rootData, err := os.ReadFile(filepath.Join(rootDir, "ca.example.com-cert.pem"))
	if err != nil {
		t.Fatalf("read root: %v", err)
	}
	if string(rootData) != string(rootPEM) {
		t.Fatalf("root file content != self-signed root PEM")
	}

	imData, err := os.ReadFile(filepath.Join(intermediateDir, "ca.example.com-intermediate-0-cert.pem"))
	if err != nil {
		t.Fatalf("read intermediate: %v", err)
	}
	if string(imData) != string(intermediatePEM) {
		t.Fatalf("intermediate file content != intermediate PEM")
	}
}

func TestReadPEMBundle_SkipsMissingDirs(t *testing.T) {
	root, _ := makeCert(t, "root", nil)
	dir := t.TempDir()
	pemDir := filepath.Join(dir, "tlscacerts")
	if err := os.MkdirAll(pemDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(pemDir, "tlsca.example.com-cert.pem"), root, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	bundle, err := readPEMBundle(pemDir, filepath.Join(dir, "tlsintermediatecerts"))
	if err != nil {
		t.Fatalf("readPEMBundle: %v", err)
	}
	if string(bundle) != string(root) {
		t.Fatalf("bundle != root pem")
	}
}

func TestMergeIssuerCertsFromNodes_AddsIntermediateFromNodeCacerts(t *testing.T) {
	// Simulate sign-ca-identity intermediate mode: /cainfo returns only the
	// upstream root; fabric-ca-client enroll left the intermediate in each
	// node's msp/cacerts/.
	rootPEM, rootSigner := makeCert(t, "sign-ca-root", nil)
	intermediatePEM, _ := makeCert(t, "sign-ca-identity", rootSigner)

	cryptoDir := t.TempDir()
	orgDir := filepath.Join(cryptoDir, "ordererOrganizations", "ord.example.com", "orderers")
	for _, node := range []string{"orderer-router-1.ord.example.com", "orderer-batcher-1.ord.example.com"} {
		mspCacerts := filepath.Join(orgDir, node, "msp", "cacerts")
		if err := os.MkdirAll(mspCacerts, 0o755); err != nil {
			t.Fatalf("mkdir node msp cacerts: %v", err)
		}
		if err := os.WriteFile(filepath.Join(mspCacerts, "ca.ord.example.com-cert.pem"), intermediatePEM, 0o644); err != nil {
			t.Fatalf("write node cacert: %v", err)
		}
	}

	// /cainfo chain has only root
	chain := rootPEM
	merged := mergeIssuerCertsFromNodes(chain, cryptoDir, "ordererOrganizations", "ord.example.com", filepath.Join("msp", "cacerts"))

	roots, intermediates, err := splitChainPEMs(merged)
	if err != nil {
		t.Fatalf("split merged: %v", err)
	}
	if len(roots) != 1 || string(roots[0]) != string(rootPEM) {
		t.Fatalf("merged chain missing root or wrong: got %d roots", len(roots))
	}
	if len(intermediates) != 1 || string(intermediates[0]) != string(intermediatePEM) {
		t.Fatalf("merged chain missing intermediate or wrong: got %d intermediates", len(intermediates))
	}
}

func TestMergeIssuerCertsFromNodes_NoopWhenNothingToAdd(t *testing.T) {
	rootPEM, _ := makeCert(t, "sign-ca-root", nil)
	// Empty cryptoDir; helper should return chain unchanged.
	cryptoDir := t.TempDir()
	merged := mergeIssuerCertsFromNodes(rootPEM, cryptoDir, "ordererOrganizations", "ord.example.com", filepath.Join("msp", "cacerts"))
	if string(merged) != string(rootPEM) {
		t.Fatalf("expected chain unchanged when no nodes present")
	}
}

func TestMergeIssuerCertsFromNodes_SkipsDuplicates(t *testing.T) {
	// One-tier CA: enroll-time cacert == /cainfo root. Helper must not
	// duplicate.
	rootPEM, _ := makeCert(t, "self-signed", nil)

	cryptoDir := t.TempDir()
	nodeMSP := filepath.Join(cryptoDir, "peerOrganizations", "org1.example.com", "peers", "peer0.org1.example.com", "msp", "cacerts")
	if err := os.MkdirAll(nodeMSP, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(nodeMSP, "ca.org1.example.com-cert.pem"), rootPEM, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	merged := mergeIssuerCertsFromNodes(rootPEM, cryptoDir, "peerOrganizations", "org1.example.com", filepath.Join("msp", "cacerts"))
	if string(merged) != string(rootPEM) {
		t.Fatalf("expected duplicate to be skipped, but chain changed")
	}
}

func TestCopyDirPEMs_MissingSourceIsNoop(t *testing.T) {
	dir := t.TempDir()
	if err := copyDirPEMs(filepath.Join(dir, "no-such-dir"), filepath.Join(dir, "dst")); err != nil {
		t.Fatalf("copy missing src: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "dst")); !os.IsNotExist(err) {
		t.Fatalf("dst should not be created when src missing")
	}
}
