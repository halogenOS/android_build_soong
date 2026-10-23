// SPDX-FileCopyrightText: The halogenOS Project
// SPDX-License-Identifier: Apache-2.0

package build

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"android/soong/shared"
)

const (
	// The start of the name of the link to the directory given with --signing-keys-dir.
	signingKeysLinkPrefix = "signing-keys-"
	// The number of hex digits of the digest in the name of the link.
	signingKeysDigestLength = 16
)

// linkSigningKeys returns the value of the keys_dir Ninja variable. With
// --signing-keys-dir, the signing run links signing-keys-<digest> in the
// signed directory to the directory given with that option, and the value is
// that link. Otherwise, the value is the directory of links to the test keys
// that Soong writes in the regular build. It removes every earlier link to
// signing keys in the directory that the build writes into, so a build without
// --signing-keys-dir leaves no link to release keys there. The digest covers
// the names and contents of the files in the directory, so a key replaced under
// the same name changes the command of every step that reads a key.
func linkSigningKeys(ctx Context, config Config) string {
	entries, err := os.ReadDir(config.WriteDir())
	if err != nil {
		ctx.Fatalf("Could not list the output directory: %v", err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), signingKeysLinkPrefix) && entry.Type()&os.ModeSymlink != 0 {
			if err := os.Remove(filepath.Join(config.WriteDir(), entry.Name())); err != nil {
				ctx.Fatalf("Could not remove an earlier link to signing keys: %v", err)
			}
		}
	}

	if !config.SigningRun() || config.signing.keysDir == nil {
		return filepath.Join(config.SoongOutDir(), shared.TestKeysDir)
	}

	keysDir := *config.signing.keysDir
	digest, err := signingKeysDigest(keysDir)
	if err != nil {
		ctx.Fatalf("Could not read the signing keys in %s: %v", keysDir, err)
	}
	link := filepath.Join(config.WriteDir(), signingKeysLinkPrefix+digest)
	if err := os.Symlink(keysDir, link); err != nil {
		ctx.Fatalf("Could not link the signing keys: %v", err)
	}
	return link
}

// signingKeysDigest returns the start of a sha256 over the names and contents
// of the files in dir. It skips subdirectories, since the build steps read only
// files directly in keys_dir.
func signingKeysDigest(dir string) (string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", fmt.Errorf("could not list the directory: %w", err)
	}

	digest := sha256.New()
	for _, entry := range entries {
		path := filepath.Join(dir, entry.Name())
		info, err := os.Stat(path)
		if err != nil {
			return "", fmt.Errorf("could not read a key file: %w", err)
		}
		if info.IsDir() {
			continue
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return "", fmt.Errorf("could not read a key file: %w", err)
		}
		// A NUL ends each name, and each content digest has a fixed length.
		contentDigest := sha256.Sum256(content)
		fmt.Fprintf(digest, "%s\x00", entry.Name())
		digest.Write(contentDigest[:])
	}
	return hex.EncodeToString(digest.Sum(nil))[:signingKeysDigestLength], nil
}
