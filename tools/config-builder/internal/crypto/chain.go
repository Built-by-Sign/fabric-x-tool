package crypto

import (
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"

	"config-builder/internal/perms"
)

// splitChainPEMs parses a PEM-encoded CA chain (one or more concatenated
// certificates) and partitions it into self-signed roots and intermediates.
// A one-tier CA returns exactly one root and zero intermediates; a two-tier
// CA returns the upstream root plus the intermediate that actually signs
// leaf certs. Non-CERTIFICATE PEM blocks are ignored.
func splitChainPEMs(chain []byte) (roots, intermediates [][]byte, err error) {
	rest := chain
	for {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			break
		}
		if block.Type != "CERTIFICATE" {
			continue
		}
		cert, perr := x509.ParseCertificate(block.Bytes)
		if perr != nil {
			return nil, nil, fmt.Errorf("parse cert in chain: %w", perr)
		}
		encoded := pem.EncodeToMemory(block)
		if cert.Subject.String() == cert.Issuer.String() {
			roots = append(roots, encoded)
		} else {
			intermediates = append(intermediates, encoded)
		}
	}
	if len(roots) == 0 && len(intermediates) == 0 {
		return nil, nil, fmt.Errorf("no certificates found in chain")
	}
	return roots, intermediates, nil
}

// writeChainToMSPDirs lays a CA chain into Fabric MSP-compatible directories:
// self-signed roots go to rootDir/{rootBaseName}-cert.pem, intermediates go to
// intermediateDir/{rootBaseName}-intermediate-N-cert.pem. The intermediate
// directory is only created when there is at least one intermediate, so a
// one-tier CA produces a byte-identical layout to the previous single-file
// behaviour.
//
// rootBaseName is typically "ca.<domain>" or "tlsca.<domain>".
//
// When the chain has no self-signed root (e.g. the configured fabric-ca is
// itself an intermediate and the upstream root lives elsewhere), the
// top-most intermediate is promoted to root so callers still get a usable
// trust anchor.
func writeChainToMSPDirs(chain []byte, rootDir, intermediateDir, rootBaseName string) error {
	roots, intermediates, err := splitChainPEMs(chain)
	if err != nil {
		return err
	}

	if len(roots) == 0 {
		// Promote the last intermediate (highest in the chain) so we at least
		// have a trust anchor; downstream callers depend on cacerts/ being
		// non-empty.
		roots = intermediates[len(intermediates)-1:]
		intermediates = intermediates[:len(intermediates)-1]
	}

	if err := os.MkdirAll(rootDir, perms.Dir); err != nil {
		return fmt.Errorf("mkdir %s: %w", rootDir, err)
	}
	var rootBundle []byte
	for _, r := range roots {
		rootBundle = append(rootBundle, r...)
	}
	rootPath := filepath.Join(rootDir, rootBaseName+"-cert.pem")
	if err := os.WriteFile(rootPath, rootBundle, perms.FileCert); err != nil {
		return fmt.Errorf("write %s: %w", rootPath, err)
	}

	if len(intermediates) == 0 {
		return nil
	}
	if err := os.MkdirAll(intermediateDir, perms.Dir); err != nil {
		return fmt.Errorf("mkdir %s: %w", intermediateDir, err)
	}
	for i, ic := range intermediates {
		path := filepath.Join(intermediateDir, fmt.Sprintf("%s-intermediate-%d-cert.pem", rootBaseName, i))
		if err := os.WriteFile(path, ic, perms.FileCert); err != nil {
			return fmt.Errorf("write %s: %w", path, err)
		}
	}
	return nil
}

// readPEMBundle returns the concatenation of every *.pem file under each
// directory in dirs, in directory-and-filename order. Missing dirs are
// silently skipped, so callers do not have to special-case the one-tier CA
// where intermediate dirs do not exist. Returns nil and no error if no
// directory yielded any PEM file.
func readPEMBundle(dirs ...string) ([]byte, error) {
	var bundle []byte
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, fmt.Errorf("read %s: %w", dir, err)
		}
		for _, e := range entries {
			if e.IsDir() || filepath.Ext(e.Name()) != ".pem" {
				continue
			}
			path := filepath.Join(dir, e.Name())
			data, err := os.ReadFile(path)
			if err != nil {
				return nil, fmt.Errorf("read %s: %w", path, err)
			}
			bundle = append(bundle, data...)
			if len(data) > 0 && data[len(data)-1] != '\n' {
				bundle = append(bundle, '\n')
			}
		}
	}
	return bundle, nil
}

// copyDirPEMs copies every *.pem file from srcDir to dstDir, preserving file
// names. A non-existent srcDir is silently treated as "nothing to copy", so
// one-tier CAs (no intermediate dirs) pass through cleanly.
func copyDirPEMs(srcDir, dstDir string) error {
	entries, err := os.ReadDir(srcDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read %s: %w", srcDir, err)
	}
	if len(entries) == 0 {
		return nil
	}
	if err := os.MkdirAll(dstDir, perms.Dir); err != nil {
		return fmt.Errorf("mkdir %s: %w", dstDir, err)
	}
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".pem" {
			continue
		}
		srcPath := filepath.Join(srcDir, e.Name())
		data, err := os.ReadFile(srcPath)
		if err != nil {
			return fmt.Errorf("read %s: %w", srcPath, err)
		}
		dstPath := filepath.Join(dstDir, e.Name())
		if err := os.WriteFile(dstPath, data, perms.FileCert); err != nil {
			return fmt.Errorf("write %s: %w", dstPath, err)
		}
	}
	return nil
}
