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
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	v2 "github.com/nats-io/nats-server/v2/conf/v2"
)

// computeTestDigest computes the behavioral SHA256 digest for a config file
// by parsing it, JSON-encoding the resulting map, and computing the SHA256.
// This matches the digest semantics used by lockIncludeNode and processInclude,
// where comments and formatting don't affect digests.
func computeTestDigest(t *testing.T, filePath string) string {
	t.Helper()
	_, digest, err := v2.ParseFileWithChecksDigest(filePath)
	if err != nil {
		t.Fatalf("computeTestDigest: ParseFileWithChecksDigest(%q) error: %v", filePath, err)
	}
	return digest
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
	expectedDigest := computeTestDigest(t, subFile)
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
	digest := computeTestDigest(t, subFile)
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
	correctDigest := computeTestDigest(t, subFile)
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
	digest1 := computeTestDigest(t, file1)
	digest2 := computeTestDigest(t, file2)
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

	leafDigest := computeTestDigest(t, leafFile)
	if !strings.Contains(midStr, leafDigest) {
		t.Fatalf("expected leaf digest %q in mid.conf, got:\n%s", leafDigest, midStr)
	}

	// The main file's digest for mid.conf should match mid.conf's content
	// AFTER it was rewritten (because lockConfigFile processes sub-files first
	// through recursive lockIncludeNode, then writes the parent).
	// Actually, let's verify: main.conf should contain a digest for mid.conf.
	// We need to re-read mid.conf to compute the digest after rewrite.
	midDigest := computeTestDigest(t, midFile)
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
	expectedDigest := computeTestDigest(t, subFile)
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
	expectedDigest := computeTestDigest(t, authFile)
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

// TestLockConfigCommentsBeforeInclude verifies that a comment line
// immediately before an include directive remains before it after locking,
// and the digest is correctly added.
func TestLockConfigCommentsBeforeInclude(t *testing.T) {
	dir := t.TempDir()

	subContent := []byte("port = 4222\n")
	subFile := filepath.Join(dir, "sub.conf")
	if err := os.WriteFile(subFile, subContent, 0644); err != nil {
		t.Fatal(err)
	}

	mainContent := fmt.Sprintf("# Load port configuration\ninclude '%s'\n", subFile)
	mainFile := filepath.Join(dir, "main.conf")
	if err := os.WriteFile(mainFile, []byte(mainContent), 0644); err != nil {
		t.Fatal(err)
	}

	if err := LockConfigIncludes(mainFile); err != nil {
		t.Fatalf("LockConfigIncludes error: %v", err)
	}

	result, err := os.ReadFile(mainFile)
	if err != nil {
		t.Fatal(err)
	}
	resultStr := string(result)

	// Verify digest is present.
	expectedDigest := computeTestDigest(t, subFile)
	if !strings.Contains(resultStr, expectedDigest) {
		t.Fatalf("expected digest %q in:\n%s", expectedDigest, resultStr)
	}

	// Verify comment stays before include.
	commentIdx := strings.Index(resultStr, "# Load port configuration")
	includeIdx := strings.Index(resultStr, "include")
	if commentIdx == -1 {
		t.Fatalf("comment was lost in:\n%s", resultStr)
	}
	if includeIdx == -1 {
		t.Fatalf("include was lost in:\n%s", resultStr)
	}
	if commentIdx >= includeIdx {
		t.Fatalf("comment should be before include, but comment at %d, include at %d:\n%s",
			commentIdx, includeIdx, resultStr)
	}
}

// TestLockConfigCommentsAfterInclude verifies that a comment line
// immediately after an include directive is preserved in correct position.
func TestLockConfigCommentsAfterInclude(t *testing.T) {
	dir := t.TempDir()

	subContent := []byte("port = 4222\n")
	subFile := filepath.Join(dir, "sub.conf")
	if err := os.WriteFile(subFile, subContent, 0644); err != nil {
		t.Fatal(err)
	}

	mainContent := fmt.Sprintf("include '%s'\n# End of includes\ndebug = true\n", subFile)
	mainFile := filepath.Join(dir, "main.conf")
	if err := os.WriteFile(mainFile, []byte(mainContent), 0644); err != nil {
		t.Fatal(err)
	}

	if err := LockConfigIncludes(mainFile); err != nil {
		t.Fatalf("LockConfigIncludes error: %v", err)
	}

	result, err := os.ReadFile(mainFile)
	if err != nil {
		t.Fatal(err)
	}
	resultStr := string(result)

	// Verify digest is present.
	expectedDigest := computeTestDigest(t, subFile)
	if !strings.Contains(resultStr, expectedDigest) {
		t.Fatalf("expected digest %q in:\n%s", expectedDigest, resultStr)
	}

	// Verify comment is after include and before debug.
	includeIdx := strings.Index(resultStr, "include")
	commentIdx := strings.Index(resultStr, "# End of includes")
	debugIdx := strings.Index(resultStr, "debug")
	if commentIdx == -1 {
		t.Fatalf("comment was lost in:\n%s", resultStr)
	}
	if includeIdx >= commentIdx {
		t.Fatalf("include should be before comment:\n%s", resultStr)
	}
	if commentIdx >= debugIdx {
		t.Fatalf("comment should be before debug:\n%s", resultStr)
	}
}

// TestLockConfigTrailingCommentOnKeyBeforeInclude verifies that a key-value
// line with a trailing comment, followed by an include, preserves both the
// trailing comment and the include's digest.
func TestLockConfigTrailingCommentOnKeyBeforeInclude(t *testing.T) {
	dir := t.TempDir()

	subContent := []byte("debug = true\n")
	subFile := filepath.Join(dir, "sub.conf")
	if err := os.WriteFile(subFile, subContent, 0644); err != nil {
		t.Fatal(err)
	}

	mainContent := fmt.Sprintf("port = 4222 # default port\ninclude '%s'\n", subFile)
	mainFile := filepath.Join(dir, "main.conf")
	if err := os.WriteFile(mainFile, []byte(mainContent), 0644); err != nil {
		t.Fatal(err)
	}

	if err := LockConfigIncludes(mainFile); err != nil {
		t.Fatalf("LockConfigIncludes error: %v", err)
	}

	result, err := os.ReadFile(mainFile)
	if err != nil {
		t.Fatal(err)
	}
	resultStr := string(result)

	// Verify digest is present.
	expectedDigest := computeTestDigest(t, subFile)
	if !strings.Contains(resultStr, expectedDigest) {
		t.Fatalf("expected digest %q in:\n%s", expectedDigest, resultStr)
	}

	// Verify trailing comment preserved.
	if !strings.Contains(resultStr, "# default port") {
		t.Fatalf("trailing comment lost in:\n%s", resultStr)
	}

	// Verify port key preserved.
	if !strings.Contains(resultStr, "port") {
		t.Fatalf("port key lost in:\n%s", resultStr)
	}

	// Verify ordering: port line with comment, then include.
	portIdx := strings.Index(resultStr, "port")
	includeIdx := strings.Index(resultStr, "include")
	if portIdx >= includeIdx {
		t.Fatalf("port should be before include:\n%s", resultStr)
	}
}

// TestLockConfigMultipleCommentsAndIncludes verifies that a config with
// multiple comment blocks interspersed with multiple includes preserves
// all comments in correct positions and adds digests to all includes.
func TestLockConfigMultipleCommentsAndIncludes(t *testing.T) {
	dir := t.TempDir()

	content1 := []byte("port = 4222\n")
	file1 := filepath.Join(dir, "ports.conf")
	if err := os.WriteFile(file1, content1, 0644); err != nil {
		t.Fatal(err)
	}

	content2 := []byte("debug = true\n")
	file2 := filepath.Join(dir, "debug.conf")
	if err := os.WriteFile(file2, content2, 0644); err != nil {
		t.Fatal(err)
	}

	content3 := []byte("host = \"0.0.0.0\"\n")
	file3 := filepath.Join(dir, "host.conf")
	if err := os.WriteFile(file3, content3, 0644); err != nil {
		t.Fatal(err)
	}

	mainContent := fmt.Sprintf(`# Port configuration
include '%s'

# Debug settings
include '%s'

# Host settings
include '%s'
`, file1, file2, file3)

	mainFile := filepath.Join(dir, "main.conf")
	if err := os.WriteFile(mainFile, []byte(mainContent), 0644); err != nil {
		t.Fatal(err)
	}

	if err := LockConfigIncludes(mainFile); err != nil {
		t.Fatalf("LockConfigIncludes error: %v", err)
	}

	result, err := os.ReadFile(mainFile)
	if err != nil {
		t.Fatal(err)
	}
	resultStr := string(result)

	// Verify all three digests are present.
	for i, f := range []string{file1, file2, file3} {
		digest := computeTestDigest(t, f)
		if !strings.Contains(resultStr, digest) {
			t.Fatalf("missing digest for file %d in:\n%s", i+1, resultStr)
		}
	}

	// Verify all three comments are preserved.
	comments := []string{"# Port configuration", "# Debug settings", "# Host settings"}
	for _, c := range comments {
		if !strings.Contains(resultStr, c) {
			t.Fatalf("comment %q lost in:\n%s", c, resultStr)
		}
	}

	// Verify comment ordering is preserved (each comment before its include).
	portCommentIdx := strings.Index(resultStr, "# Port configuration")
	debugCommentIdx := strings.Index(resultStr, "# Debug settings")
	hostCommentIdx := strings.Index(resultStr, "# Host settings")
	if portCommentIdx >= debugCommentIdx || debugCommentIdx >= hostCommentIdx {
		t.Fatalf("comments should be in order: port < debug < host, got %d, %d, %d:\n%s",
			portCommentIdx, debugCommentIdx, hostCommentIdx, resultStr)
	}

	// Verify blank lines are preserved (at least some separation).
	lines := strings.Split(resultStr, "\n")
	hasBlankLine := false
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			hasBlankLine = true
			break
		}
	}
	if !hasBlankLine {
		t.Fatalf("expected blank lines to be preserved in:\n%s", resultStr)
	}
}

// TestLockConfigCommentInsideMap verifies that comments inside a map block
// containing includes are preserved correctly and digests are added.
func TestLockConfigCommentInsideMap(t *testing.T) {
	dir := t.TempDir()

	authContent := []byte("user = admin\npassword = secret\n")
	authFile := filepath.Join(dir, "auth.conf")
	if err := os.WriteFile(authFile, authContent, 0644); err != nil {
		t.Fatal(err)
	}

	mainContent := fmt.Sprintf(`authorization {
  # Load auth credentials
  include '%s'
}
`, authFile)

	mainFile := filepath.Join(dir, "main.conf")
	if err := os.WriteFile(mainFile, []byte(mainContent), 0644); err != nil {
		t.Fatal(err)
	}

	if err := LockConfigIncludes(mainFile); err != nil {
		t.Fatalf("LockConfigIncludes error: %v", err)
	}

	result, err := os.ReadFile(mainFile)
	if err != nil {
		t.Fatal(err)
	}
	resultStr := string(result)

	// Verify digest is present.
	expectedDigest := computeTestDigest(t, authFile)
	if !strings.Contains(resultStr, expectedDigest) {
		t.Fatalf("expected digest %q in:\n%s", expectedDigest, resultStr)
	}

	// Verify comment inside map block is preserved.
	if !strings.Contains(resultStr, "# Load auth credentials") {
		t.Fatalf("comment inside map lost in:\n%s", resultStr)
	}

	// Verify the map block structure is preserved.
	if !strings.Contains(resultStr, "authorization") {
		t.Fatalf("authorization key lost in:\n%s", resultStr)
	}

	// Verify comment is inside the block (between { and }).
	openBrace := strings.Index(resultStr, "{")
	closeBrace := strings.LastIndex(resultStr, "}")
	commentIdx := strings.Index(resultStr, "# Load auth credentials")
	if commentIdx <= openBrace || commentIdx >= closeBrace {
		t.Fatalf("comment should be inside the block, brace at %d-%d, comment at %d:\n%s",
			openBrace, closeBrace, commentIdx, resultStr)
	}

	// Verify indentation is preserved (comment and include should be indented).
	lines := strings.Split(resultStr, "\n")
	for _, line := range lines {
		if strings.Contains(line, "# Load auth credentials") {
			if !strings.HasPrefix(line, "  ") && !strings.HasPrefix(line, "\t") {
				t.Fatalf("comment inside map should be indented, got: %q", line)
			}
		}
		if strings.Contains(line, "include") {
			if !strings.HasPrefix(line, "  ") && !strings.HasPrefix(line, "\t") {
				t.Fatalf("include inside map should be indented, got: %q", line)
			}
		}
	}
}

// TestLockConfigRealWorldIncludeChain exercises the --lock feature on config
// files similar to the include_conf_check_a/b/c.conf pattern with variables,
// multiple includes, comments, and verifies round-trip fidelity.
func TestLockConfigRealWorldIncludeChain(t *testing.T) {
	dir := t.TempDir()

	// Create config C (leaf): authorization block.
	configC := `
authorization {
  user = "foo"
  pass = "bar"
}
`
	fileC := filepath.Join(dir, "check_c.conf")
	if err := os.WriteFile(fileC, []byte(configC), 0644); err != nil {
		t.Fatal(err)
	}

	// Create config B (middle): variable def + include of C.
	configB := fmt.Sprintf(`
# Monitoring port variable
monitoring_port = 8222

# Authorization config
include '%s'
`, fileC)
	fileB := filepath.Join(dir, "check_b.conf")
	if err := os.WriteFile(fileB, []byte(configB), 0644); err != nil {
		t.Fatal(err)
	}

	// Create config A (root): port, include B, variable reference.
	configA := fmt.Sprintf(`# Main NATS configuration
port = 4222

# Include sub-configs
include '%s'

# Use monitoring port variable
http_port = $monitoring_port
`, fileB)
	fileA := filepath.Join(dir, "check_a.conf")
	if err := os.WriteFile(fileA, []byte(configA), 0644); err != nil {
		t.Fatal(err)
	}

	// Lock starting from config A.
	if err := LockConfigIncludes(fileA); err != nil {
		t.Fatalf("LockConfigIncludes error: %v", err)
	}

	// Read all three files back.
	resultA, err := os.ReadFile(fileA)
	if err != nil {
		t.Fatal(err)
	}
	resultB, err := os.ReadFile(fileB)
	if err != nil {
		t.Fatal(err)
	}
	// Config C has no includes, so it should be unchanged.
	resultC, err := os.ReadFile(fileC)
	if err != nil {
		t.Fatal(err)
	}

	resultAStr := string(resultA)
	resultBStr := string(resultB)

	// --- Verify config A ---
	// Digest for B should be present in A.
	digestB := computeTestDigest(t, fileB) // B was rewritten, use its behavioral digest.
	if !strings.Contains(resultAStr, digestB) {
		t.Fatalf("expected digest for B in config A, got:\n%s", resultAStr)
	}
	// Comments in A should be preserved.
	if !strings.Contains(resultAStr, "# Main NATS configuration") {
		t.Fatalf("comment lost in config A:\n%s", resultAStr)
	}
	if !strings.Contains(resultAStr, "# Include sub-configs") {
		t.Fatalf("comment lost in config A:\n%s", resultAStr)
	}
	if !strings.Contains(resultAStr, "# Use monitoring port variable") {
		t.Fatalf("comment lost in config A:\n%s", resultAStr)
	}
	// Variable reference should be preserved.
	if !strings.Contains(resultAStr, "$monitoring_port") {
		t.Fatalf("variable reference lost in config A:\n%s", resultAStr)
	}
	// Port setting should be preserved.
	if !strings.Contains(resultAStr, "port = 4222") && !strings.Contains(resultAStr, "port: 4222") &&
		!strings.Contains(resultAStr, "port =4222") && !strings.Contains(resultAStr, "port=4222") {
		// Just check that port and 4222 are present.
		if !strings.Contains(resultAStr, "port") || !strings.Contains(resultAStr, "4222") {
			t.Fatalf("port setting lost in config A:\n%s", resultAStr)
		}
	}

	// --- Verify config B ---
	// Digest for C should be present in B.
	digestC := computeTestDigest(t, fileC)
	if !strings.Contains(resultBStr, digestC) {
		t.Fatalf("expected digest for C in config B, got:\n%s", resultBStr)
	}
	// Comments in B should be preserved.
	if !strings.Contains(resultBStr, "# Monitoring port variable") {
		t.Fatalf("comment lost in config B:\n%s", resultBStr)
	}
	if !strings.Contains(resultBStr, "# Authorization config") {
		t.Fatalf("comment lost in config B:\n%s", resultBStr)
	}
	// Variable definition should be preserved.
	if !strings.Contains(resultBStr, "monitoring_port") || !strings.Contains(resultBStr, "8222") {
		t.Fatalf("variable definition lost in config B:\n%s", resultBStr)
	}

	// --- Verify config C is unchanged ---
	if string(resultC) != configC {
		t.Fatalf("config C should be unchanged (no includes to lock).\nExpected:\n%s\nGot:\n%s",
			configC, string(resultC))
	}

	// --- Verify blank lines are preserved in A ---
	lines := strings.Split(resultAStr, "\n")
	blankCount := 0
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			blankCount++
		}
	}
	if blankCount < 2 {
		t.Fatalf("expected multiple blank lines preserved in config A (got %d):\n%s",
			blankCount, resultAStr)
	}
}
