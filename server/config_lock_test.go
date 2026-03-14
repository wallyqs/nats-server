// Copyright 2025 The NATS Authors
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package server

import (
	"crypto/sha256"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// computeTestDigest computes the SHA256 digest for data in "sha256:<hex>" format.
func computeTestDigest(data []byte) string {
	h := sha256.Sum256(data)
	return fmt.Sprintf("sha256:%x", h[:])
}

// TestLockConfigIncludes verifies that LockConfigIncludes rewrites a config
// file to add SHA256 digests to include directives without digests.
func TestLockConfigIncludes(t *testing.T) {
	dir := t.TempDir()

	// Create the included file.
	subContent := []byte("port = 4222\n")
	subFile := filepath.Join(dir, "sub.conf")
	if err := os.WriteFile(subFile, subContent, 0644); err != nil {
		t.Fatal(err)
	}

	// Create the main config file with an include directive (no digest).
	mainContent := fmt.Sprintf("include '%s'\n", subFile)
	mainFile := filepath.Join(dir, "main.conf")
	if err := os.WriteFile(mainFile, []byte(mainContent), 0644); err != nil {
		t.Fatal(err)
	}

	// Lock the config.
	if err := LockConfigIncludes(mainFile); err != nil {
		t.Fatalf("LockConfigIncludes error: %v", err)
	}

	// Read the rewritten file.
	result, err := os.ReadFile(mainFile)
	if err != nil {
		t.Fatal(err)
	}

	// Verify the digest was added.
	expectedDigest := computeTestDigest(subContent)
	resultStr := string(result)
	if !strings.Contains(resultStr, expectedDigest) {
		t.Fatalf("expected digest %q in rewritten config, got:\n%s", expectedDigest, resultStr)
	}
	if !strings.Contains(resultStr, "include") {
		t.Fatalf("expected 'include' directive in rewritten config, got:\n%s", resultStr)
	}
}

// TestLockConfigIncludesAlreadyLocked verifies that a config with correct
// digests is left unchanged by LockConfigIncludes.
func TestLockConfigIncludesAlreadyLocked(t *testing.T) {
	dir := t.TempDir()

	// Create the included file.
	subContent := []byte("port = 4222\n")
	subFile := filepath.Join(dir, "sub.conf")
	if err := os.WriteFile(subFile, subContent, 0644); err != nil {
		t.Fatal(err)
	}

	// Create the main config file with the correct digest already present.
	digest := computeTestDigest(subContent)
	mainContent := fmt.Sprintf("include '%s' '%s'\n", subFile, digest)
	mainFile := filepath.Join(dir, "main.conf")
	if err := os.WriteFile(mainFile, []byte(mainContent), 0644); err != nil {
		t.Fatal(err)
	}

	// Record original file content and mod time.
	origContent, _ := os.ReadFile(mainFile)
	origInfo, _ := os.Stat(mainFile)

	// Lock the config.
	if err := LockConfigIncludes(mainFile); err != nil {
		t.Fatalf("LockConfigIncludes error: %v", err)
	}

	// Verify file was NOT rewritten (content should be identical).
	newContent, err := os.ReadFile(mainFile)
	if err != nil {
		t.Fatal(err)
	}
	newInfo, _ := os.Stat(mainFile)

	if string(newContent) != string(origContent) {
		t.Fatalf("file should be unchanged when already locked.\nOriginal:\n%s\nNew:\n%s",
			string(origContent), string(newContent))
	}
	// Verify mod time hasn't changed (file should not have been rewritten).
	if newInfo.ModTime() != origInfo.ModTime() {
		t.Fatal("file mod time changed despite already being locked")
	}
}

// TestLockConfigIncludesWrongDigest verifies that a config with an incorrect
// digest is updated to the correct value.
func TestLockConfigIncludesWrongDigest(t *testing.T) {
	dir := t.TempDir()

	// Create the included file.
	subContent := []byte("port = 4222\n")
	subFile := filepath.Join(dir, "sub.conf")
	if err := os.WriteFile(subFile, subContent, 0644); err != nil {
		t.Fatal(err)
	}

	// Create the main config file with a wrong digest.
	wrongDigest := "sha256:0000000000000000000000000000000000000000000000000000000000000000"
	mainContent := fmt.Sprintf("include '%s' '%s'\n", subFile, wrongDigest)
	mainFile := filepath.Join(dir, "main.conf")
	if err := os.WriteFile(mainFile, []byte(mainContent), 0644); err != nil {
		t.Fatal(err)
	}

	// Lock the config.
	if err := LockConfigIncludes(mainFile); err != nil {
		t.Fatalf("LockConfigIncludes error: %v", err)
	}

	// Read the rewritten file.
	result, err := os.ReadFile(mainFile)
	if err != nil {
		t.Fatal(err)
	}

	// Verify the wrong digest was replaced with the correct one.
	correctDigest := computeTestDigest(subContent)
	resultStr := string(result)
	if strings.Contains(resultStr, wrongDigest) {
		t.Fatalf("wrong digest should have been replaced, got:\n%s", resultStr)
	}
	if !strings.Contains(resultStr, correctDigest) {
		t.Fatalf("expected correct digest %q, got:\n%s", correctDigest, resultStr)
	}
}

// TestLockConfigIncludesMultiple verifies that a config with multiple includes
// all get digests added.
func TestLockConfigIncludesMultiple(t *testing.T) {
	dir := t.TempDir()

	// Create two included files.
	content1 := []byte("port = 4222\n")
	file1 := filepath.Join(dir, "one.conf")
	if err := os.WriteFile(file1, content1, 0644); err != nil {
		t.Fatal(err)
	}

	content2 := []byte("host = \"0.0.0.0\"\n")
	file2 := filepath.Join(dir, "two.conf")
	if err := os.WriteFile(file2, content2, 0644); err != nil {
		t.Fatal(err)
	}

	// Create the main config file with two includes (no digests).
	mainContent := fmt.Sprintf("include '%s'\ninclude '%s'\n", file1, file2)
	mainFile := filepath.Join(dir, "main.conf")
	if err := os.WriteFile(mainFile, []byte(mainContent), 0644); err != nil {
		t.Fatal(err)
	}

	// Lock the config.
	if err := LockConfigIncludes(mainFile); err != nil {
		t.Fatalf("LockConfigIncludes error: %v", err)
	}

	// Read the rewritten file.
	result, err := os.ReadFile(mainFile)
	if err != nil {
		t.Fatal(err)
	}
	resultStr := string(result)

	// Verify both digests are present.
	digest1 := computeTestDigest(content1)
	digest2 := computeTestDigest(content2)
	if !strings.Contains(resultStr, digest1) {
		t.Fatalf("expected digest for file1 %q in:\n%s", digest1, resultStr)
	}
	if !strings.Contains(resultStr, digest2) {
		t.Fatalf("expected digest for file2 %q in:\n%s", digest2, resultStr)
	}
}

// TestLockConfigIncludesRecursive verifies that includes in sub-files also
// get their digests locked.
func TestLockConfigIncludesRecursive(t *testing.T) {
	dir := t.TempDir()

	// Create a leaf config file (no includes).
	leafContent := []byte("debug = true\n")
	leafFile := filepath.Join(dir, "leaf.conf")
	if err := os.WriteFile(leafFile, leafContent, 0644); err != nil {
		t.Fatal(err)
	}

	// Create a middle config file that includes the leaf.
	midContent := fmt.Sprintf("port = 4222\ninclude '%s'\n", leafFile)
	midFile := filepath.Join(dir, "mid.conf")
	if err := os.WriteFile(midFile, []byte(midContent), 0644); err != nil {
		t.Fatal(err)
	}

	// Create the main config file that includes the middle.
	mainContent := fmt.Sprintf("include '%s'\n", midFile)
	mainFile := filepath.Join(dir, "main.conf")
	if err := os.WriteFile(mainFile, []byte(mainContent), 0644); err != nil {
		t.Fatal(err)
	}

	// Lock the config.
	if err := LockConfigIncludes(mainFile); err != nil {
		t.Fatalf("LockConfigIncludes error: %v", err)
	}

	// Read the rewritten main file — it should have the digest for mid.conf.
	mainResult, err := os.ReadFile(mainFile)
	if err != nil {
		t.Fatal(err)
	}
	mainStr := string(mainResult)

	// Read the rewritten mid file — it should have the digest for leaf.conf.
	midResult, err := os.ReadFile(midFile)
	if err != nil {
		t.Fatal(err)
	}
	midStr := string(midResult)

	leafDigest := computeTestDigest(leafContent)
	if !strings.Contains(midStr, leafDigest) {
		t.Fatalf("expected leaf digest %q in mid.conf, got:\n%s", leafDigest, midStr)
	}

	// The main file's digest for mid.conf should match mid.conf's content
	// AFTER it was rewritten (because lockConfigFile processes sub-files first
	// through recursive lockIncludeNode, then writes the parent).
	// Actually, let's verify: main.conf should contain a digest for mid.conf.
	// We need to re-read mid.conf to compute the digest after rewrite.
	midRewritten, _ := os.ReadFile(midFile)
	midDigest := computeTestDigest(midRewritten)
	if !strings.Contains(mainStr, midDigest) {
		t.Fatalf("expected mid.conf digest %q in main.conf, got:\n%s", midDigest, mainStr)
	}
}

// TestLockConfigIncludesPreservesFormatting verifies that comments, blank lines,
// and other config content are preserved after LockConfigIncludes rewrites the file.
func TestLockConfigIncludesPreservesFormatting(t *testing.T) {
	dir := t.TempDir()

	// Create the included file.
	subContent := []byte("port = 4222\n")
	subFile := filepath.Join(dir, "sub.conf")
	if err := os.WriteFile(subFile, subContent, 0644); err != nil {
		t.Fatal(err)
	}

	// Create a config with comments, blank lines, and other settings.
	mainContent := fmt.Sprintf(`# Main configuration file
server_name = my_server

# Include sub-config
include '%s'

# More settings
debug = true
`, subFile)
	mainFile := filepath.Join(dir, "main.conf")
	if err := os.WriteFile(mainFile, []byte(mainContent), 0644); err != nil {
		t.Fatal(err)
	}

	// Lock the config.
	if err := LockConfigIncludes(mainFile); err != nil {
		t.Fatalf("LockConfigIncludes error: %v", err)
	}

	// Read the rewritten file.
	result, err := os.ReadFile(mainFile)
	if err != nil {
		t.Fatal(err)
	}
	resultStr := string(result)

	// Verify formatting is preserved.
	if !strings.Contains(resultStr, "# Main configuration file") {
		t.Fatalf("comment was not preserved in:\n%s", resultStr)
	}
	if !strings.Contains(resultStr, "server_name") {
		t.Fatalf("server_name setting was not preserved in:\n%s", resultStr)
	}
	if !strings.Contains(resultStr, "# Include sub-config") {
		t.Fatalf("include comment was not preserved in:\n%s", resultStr)
	}
	if !strings.Contains(resultStr, "# More settings") {
		t.Fatalf("more settings comment was not preserved in:\n%s", resultStr)
	}
	if !strings.Contains(resultStr, "debug") {
		t.Fatalf("debug setting was not preserved in:\n%s", resultStr)
	}

	// Verify the digest was added.
	expectedDigest := computeTestDigest(subContent)
	if !strings.Contains(resultStr, expectedDigest) {
		t.Fatalf("expected digest %q in:\n%s", expectedDigest, resultStr)
	}
}

// TestLockConfigIncludesInMap verifies that include directives inside map blocks
// are also locked.
func TestLockConfigIncludesInMap(t *testing.T) {
	dir := t.TempDir()

	// Create the included file.
	authContent := []byte("user = admin\npassword = secret\n")
	authFile := filepath.Join(dir, "auth.conf")
	if err := os.WriteFile(authFile, authContent, 0644); err != nil {
		t.Fatal(err)
	}

	// Create main config with include inside a map block.
	mainContent := fmt.Sprintf("authorization {\n  include '%s'\n}\n", authFile)
	mainFile := filepath.Join(dir, "main.conf")
	if err := os.WriteFile(mainFile, []byte(mainContent), 0644); err != nil {
		t.Fatal(err)
	}

	// Lock the config.
	if err := LockConfigIncludes(mainFile); err != nil {
		t.Fatalf("LockConfigIncludes error: %v", err)
	}

	// Read the rewritten file.
	result, err := os.ReadFile(mainFile)
	if err != nil {
		t.Fatal(err)
	}
	resultStr := string(result)

	// Verify the digest was added.
	expectedDigest := computeTestDigest(authContent)
	if !strings.Contains(resultStr, expectedDigest) {
		t.Fatalf("expected digest %q in:\n%s", expectedDigest, resultStr)
	}
	if !strings.Contains(resultStr, "authorization") {
		t.Fatalf("authorization block not preserved in:\n%s", resultStr)
	}
}

// TestLockConfigViaMainFlags tests the full flow via ConfigureOptions
// with -t --lock flags and NATS_CONFIG_V2 env var.
func TestLockConfigViaMainFlags(t *testing.T) {
	dir := t.TempDir()

	// Create the included file.
	subContent := []byte("port = 4222\n")
	subFile := filepath.Join(dir, "sub.conf")
	if err := os.WriteFile(subFile, subContent, 0644); err != nil {
		t.Fatal(err)
	}

	// Create the main config file.
	mainContent := fmt.Sprintf("include '%s'\n", subFile)
	mainFile := filepath.Join(dir, "main.conf")
	if err := os.WriteFile(mainFile, []byte(mainContent), 0644); err != nil {
		t.Fatal(err)
	}

	// Enable NATS_CONFIG_V2.
	t.Setenv(envConfigV2, "true")

	// Test with -t --lock flags.
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	args := []string{"-c", mainFile, "-t", "--lock"}
	opts, err := ConfigureOptions(fs, args, nil, nil, nil)
	if err != nil {
		t.Fatalf("ConfigureOptions error: %v", err)
	}

	// Verify the options are correctly set.
	if !opts.CheckConfig {
		t.Fatal("expected CheckConfig to be true")
	}
	if !opts.LockConfig {
		t.Fatal("expected LockConfig to be true")
	}
	if opts.ConfigFile != mainFile {
		t.Fatalf("expected ConfigFile=%q, got %q", mainFile, opts.ConfigFile)
	}
}

// TestLockConfigRequiresCheckConfig verifies that --lock without -t
// returns an error.
func TestLockConfigRequiresCheckConfig(t *testing.T) {
	dir := t.TempDir()
	mainFile := filepath.Join(dir, "main.conf")
	if err := os.WriteFile(mainFile, []byte("port = 4222\n"), 0644); err != nil {
		t.Fatal(err)
	}

	t.Setenv(envConfigV2, "true")

	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	args := []string{"-c", mainFile, "--lock"}
	_, err := ConfigureOptions(fs, args, nil, nil, nil)
	if err == nil {
		t.Fatal("expected error when --lock used without -t")
	}
	if !strings.Contains(err.Error(), "--lock requires -t") {
		t.Fatalf("expected error about --lock requiring -t, got: %v", err)
	}
}

// TestLockConfigRequiresConfigV2 verifies that --lock without NATS_CONFIG_V2
// returns an error.
func TestLockConfigRequiresConfigV2(t *testing.T) {
	dir := t.TempDir()
	mainFile := filepath.Join(dir, "main.conf")
	if err := os.WriteFile(mainFile, []byte("port = 4222\n"), 0644); err != nil {
		t.Fatal(err)
	}

	// Ensure v2 is NOT set.
	t.Setenv(envConfigV2, "")

	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	args := []string{"-c", mainFile, "-t", "--lock"}
	_, err := ConfigureOptions(fs, args, nil, nil, nil)
	if err == nil {
		t.Fatal("expected error when --lock used without NATS_CONFIG_V2")
	}
	if !strings.Contains(err.Error(), "NATS_CONFIG_V2") {
		t.Fatalf("expected error about NATS_CONFIG_V2, got: %v", err)
	}
}
