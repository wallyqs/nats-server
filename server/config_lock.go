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
	"fmt"
	"os"
	"path/filepath"

	v2 "github.com/nats-io/nats-server/v2/conf/v2"
)

// LockConfigIncludes rewrites the given configuration file to add or
// update SHA-256 digests on all include directives. It parses the file
// using the v2 AST raw mode (which preserves comments, formatting,
// variables, and include directives), computes digests for each
// included file, updates the AST, and writes the result back. Included
// files that themselves contain includes are processed recursively.
func LockConfigIncludes(configFile string) error {
	absPath, err := filepath.Abs(configFile)
	if err != nil {
		return fmt.Errorf("error resolving config path: %v", err)
	}
	_, err = lockConfigFile(absPath, nil)
	return err
}

// lockConfigFile processes a single config file: parses the AST in raw
// mode, walks include nodes to compute and set digests, emits the
// modified AST back to the file, and recurses into included files.
// The visited map tracks already-processed files to avoid cycles.
// Returns the total number of includes that were locked (added or updated).
func lockConfigFile(configFile string, visited map[string]bool) (int, error) {
	if visited == nil {
		visited = make(map[string]bool)
	}
	if visited[configFile] {
		return 0, nil
	}
	visited[configFile] = true

	// Read the original file to preserve permissions.
	info, err := os.Stat(configFile)
	if err != nil {
		return 0, fmt.Errorf("error reading config file: %v", err)
	}

	doc, err := v2.ParseASTRawFile(configFile)
	if err != nil {
		return 0, fmt.Errorf("error parsing config file %s: %v", configFile, err)
	}

	configDir := filepath.Dir(configFile)
	locked := 0

	// Walk top-level items and process includes.
	locked, err = lockIncludes(doc.Items, configDir, visited)
	if err != nil {
		return 0, err
	}

	if locked > 0 {
		// Emit the modified AST and write back.
		out, err := v2.Emit(doc)
		if err != nil {
			return 0, fmt.Errorf("error emitting config: %v", err)
		}
		if err := os.WriteFile(configFile, out, info.Mode().Perm()); err != nil {
			return 0, fmt.Errorf("error writing config file: %v", err)
		}
	}

	return locked, nil
}

// lockIncludes walks a list of AST nodes, finds IncludeNode entries
// (including those nested inside MapNode values), computes SHA-256
// digests for their referenced files, and updates the Digest field.
// It also recurses into included files to lock their includes.
// Returns the number of includes that were locked in this node list.
func lockIncludes(items []v2.Node, configDir string, visited map[string]bool) (int, error) {
	locked := 0
	for _, item := range items {
		switch n := item.(type) {
		case *v2.IncludeNode:
			count, err := lockIncludeNode(n, configDir, visited)
			if err != nil {
				return locked, err
			}
			locked += count
		case *v2.KeyValueNode:
			// Check if the value is a MapNode containing includes.
			if m, ok := n.Value.(*v2.MapNode); ok {
				count, err := lockIncludes(m.Items, configDir, visited)
				if err != nil {
					return locked, err
				}
				locked += count
			}
		}
	}
	return locked, nil
}

// lockIncludeNode processes a single IncludeNode: resolves the include
// path, recurses into the included file to lock its own includes first,
// then reads the (potentially rewritten) file, computes its SHA-256
// digest, and updates the node's Digest field if needed.
func lockIncludeNode(inc *v2.IncludeNode, configDir string, visited map[string]bool) (int, error) {
	locked := 0

	// Resolve include path relative to the config file directory.
	includePath := inc.Path
	if !filepath.IsAbs(includePath) {
		includePath = filepath.Join(configDir, includePath)
	}

	// Recurse into the included file first to lock its own includes.
	// This must happen before computing the digest, because the
	// recursive lock may rewrite the included file.
	absInclude, err := filepath.Abs(includePath)
	if err != nil {
		return locked, fmt.Errorf("error resolving include path: %v", err)
	}
	subLocked, err := lockConfigFile(absInclude, visited)
	if err != nil {
		return locked, err
	}
	locked += subLocked

	// Read the (potentially rewritten) included file and compute its digest.
	data, err := os.ReadFile(includePath)
	if err != nil {
		return 0, fmt.Errorf("error reading include file '%s': %v", inc.Path, err)
	}
	h := sha256.Sum256(data)
	digest := fmt.Sprintf("sha256:%x", h[:])

	// Update digest if missing or wrong.
	if inc.Digest != digest {
		inc.Digest = digest
		locked++
	}

	return locked, nil
}
