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

package keystore

import "testing"

func TestParseStoreType(t *testing.T) {
	tests := []struct {
		input   string
		want    StoreType
		wantErr bool
	}{
		{"pkcs11", PKCS11, false},
		{"PKCS11", PKCS11, false},
		{"Pkcs11", PKCS11, false},
		{"unknown", 0, true},
		{"", 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got, err := ParseStoreType(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseStoreType(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("ParseStoreType(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestParseMatchBy(t *testing.T) {
	tests := []struct {
		input   string
		want    MatchByType
		wantErr bool
	}{
		{"label", MatchByLabel, false},
		{"Label", MatchByLabel, false},
		{"LABEL", MatchByLabel, false},
		{"id", MatchByID, false},
		{"ID", MatchByID, false},
		{"unknown", 0, true},
		{"", 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got, err := ParseMatchBy(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseMatchBy(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("ParseMatchBy(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}
