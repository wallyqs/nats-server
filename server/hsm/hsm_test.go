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

package hsm

import "testing"

func TestConfigValidate(t *testing.T) {
	tests := []struct {
		name    string
		cfg     Config
		wantErr error
	}{
		{
			name: "valid with key_label",
			cfg: Config{
				Provider:   "/usr/lib/softhsm/libsofthsm2.so",
				TokenLabel: "my-token",
				KeyLabel:   "server-key",
			},
			wantErr: nil,
		},
		{
			name: "valid with key_id",
			cfg: Config{
				Provider:   "/usr/lib/softhsm/libsofthsm2.so",
				TokenLabel: "my-token",
				KeyID:      "0102030405",
			},
			wantErr: nil,
		},
		{
			name: "valid with both key_label and key_id",
			cfg: Config{
				Provider:   "/usr/lib/softhsm/libsofthsm2.so",
				TokenLabel: "my-token",
				KeyLabel:   "server-key",
				KeyID:      "0102030405",
			},
			wantErr: nil,
		},
		{
			name: "missing provider",
			cfg: Config{
				TokenLabel: "my-token",
				KeyLabel:   "server-key",
			},
			wantErr: ErrProviderRequired,
		},
		{
			name: "missing token_label",
			cfg: Config{
				Provider: "/usr/lib/softhsm/libsofthsm2.so",
				KeyLabel: "server-key",
			},
			wantErr: ErrTokenRequired,
		},
		{
			name: "missing key identifier",
			cfg: Config{
				Provider:   "/usr/lib/softhsm/libsofthsm2.so",
				TokenLabel: "my-token",
			},
			wantErr: ErrKeyRequired,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cfg.Validate()
			if err != tt.wantErr {
				t.Errorf("Validate() = %v, want %v", err, tt.wantErr)
			}
		})
	}
}
