// SPDX-FileCopyrightText: The halogenOS Project
// SPDX-License-Identifier: Apache-2.0

package build

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"android/soong/shared"
	"android/soong/ui/logger"
)

func TestSigningKeysDigest(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
	digest := func() string {
		t.Helper()
		d, err := signingKeysDigest(dir)
		if err != nil {
			t.Fatal(err)
		}
		return d
	}

	write("releasekey.pk8", "first key")
	first := digest()

	if err := os.Mkdir(filepath.Join(dir, "unused"), 0755); err != nil {
		t.Fatal(err)
	}
	write("unused/releasekey.pk8", "file in a subdirectory")
	if got := digest(); got != first {
		t.Errorf("a file in a subdirectory changed the digest from %s to %s", first, got)
	}

	write("releasekey.pk8", "second key")
	replaced := digest()
	if replaced == first {
		t.Errorf("a replaced key gave the same digest, %s", first)
	}

	if err := os.Rename(filepath.Join(dir, "releasekey.pk8"), filepath.Join(dir, "platform.pk8")); err != nil {
		t.Fatal(err)
	}
	if got := digest(); got == replaced {
		t.Errorf("a renamed key gave the same digest, %s", replaced)
	}
}

func TestLinkSigningKeys(t *testing.T) {
	defer logger.Recover(func(err error) {
		t.Fatal(err)
	})

	outDir := t.TempDir()
	signedDir := filepath.Join(outDir, "signed")
	if err := os.Mkdir(signedDir, 0755); err != nil {
		t.Fatal(err)
	}
	keysDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(keysDir, "releasekey.pk8"), []byte("key"), 0644); err != nil {
		t.Fatal(err)
	}
	earlierLink := filepath.Join(signedDir, signingKeysLinkPrefix+"earlier")
	if err := os.Symlink(keysDir, earlierLink); err != nil {
		t.Fatal(err)
	}
	regularFile := filepath.Join(signedDir, signingKeysLinkPrefix+"file")
	if err := os.WriteFile(regularFile, nil, 0644); err != nil {
		t.Fatal(err)
	}
	config := Config{&configImpl{
		environ: &Environment{"OUT_DIR=" + outDir},
		signing: &signingRun{keysDir: &keysDir},
	}}

	link := linkSigningKeys(testContext(), config)
	digest, err := signingKeysDigest(keysDir)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(signedDir, signingKeysLinkPrefix+digest); link != want {
		t.Errorf("link: want %q, got %q", want, link)
	}
	if target, err := os.Readlink(link); err != nil || target != keysDir {
		t.Errorf("link target: want %q, got %q (%v)", keysDir, target, err)
	}
	if _, err := os.Lstat(earlierLink); !os.IsNotExist(err) {
		t.Errorf("the earlier link %s is still there (%v)", earlierLink, err)
	}
	if _, err := os.Stat(regularFile); err != nil {
		t.Errorf("the regular file %s is gone: %v", regularFile, err)
	}

	config.signing.keysDir = nil
	if got, want := linkSigningKeys(testContext(), config), filepath.Join(outDir, "soong", shared.TestKeysDir); got != want {
		t.Errorf("keys_dir of a signing run with the test keys: want %q, got %q", want, got)
	}
	if _, err := os.Lstat(link); !os.IsNotExist(err) {
		t.Errorf("a signing run with the test keys still had the link %s (%v)", link, err)
	}
}

func TestConfigParseArgsSigning(t *testing.T) {
	signingDate := time.Date(2026, time.October, 23, 14, 30, 0, 0, time.UTC)
	releaseKeysDir := "/keys"
	for _, test := range []struct {
		name    string
		args    []string
		signing *signingRun
		err     string
	}{
		{
			name: "regular build",
		},
		{
			name:    "signing build",
			args:    []string{"--signing-keys-dir=/keys", "--build-date=20261023-143000"},
			signing: &signingRun{keysDir: &releaseKeysDir, date: signingDate},
		},
		{
			name: "keys without a date",
			args: []string{"--signing-keys-dir=/keys"},
			err:  "--signing-keys-dir needs --build-date, which starts the signing run",
		},
		{
			name:    "date without keys",
			args:    []string{"--build-date=20261023-143000"},
			signing: &signingRun{date: signingDate},
		},
		{
			name: "relative key directory",
			args: []string{"--signing-keys-dir=keys", "--build-date=20261023-143000"},
			err:  `--signing-keys-dir needs an absolute path, got "keys"`,
		},
		{
			name: "date in another form",
			args: []string{"--signing-keys-dir=/keys", "--build-date=2026-10-23"},
			err:  `--build-date needs the form YYYYMMDD-hhmmss, got "2026-10-23"`,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			c := &configImpl{}
			func() {
				defer logger.Recover(func(err error) {
					if err.Error() != test.err {
						t.Errorf("error: want %q, got %q", test.err, err)
					}
				})
				c.parseArgs(testContext(), test.args)
				if test.err != "" {
					t.Errorf("no error, want %q", test.err)
				}
			}()
			if test.err != "" {
				return
			}
			if !reflect.DeepEqual(c.signing, test.signing) {
				t.Errorf("signing run: want %+v, got %+v", test.signing, c.signing)
			}
		})
	}
}

func TestSigningDate(t *testing.T) {
	c := &configImpl{}
	if got := c.signingDate(); got != shared.SigningDatePlaceholder {
		t.Errorf("build_date of a regular build: want %q, got %q", shared.SigningDatePlaceholder, got)
	}
	c.signing = &signingRun{date: time.Date(2026, time.October, 23, 14, 30, 0, 0, time.UTC)}
	if got, want := c.signingDate(), "20261023-143000"; got != want {
		t.Errorf("build_date of a signing build: want %q, got %q", want, got)
	}
}
