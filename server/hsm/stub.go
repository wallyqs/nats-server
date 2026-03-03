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

//go:build !hsm

package hsm

import "crypto"

// GetSigner returns ErrHSMNotAvailable when built without the hsm build tag.
// Build with: go build -tags hsm
func GetSigner(_ *Config, _ crypto.PublicKey) (Signer, error) {
	return nil, ErrHSMNotAvailable
}
