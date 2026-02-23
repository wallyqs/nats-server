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

const (
	// FeatureFlagJsAckFormatV2 controls the use of the v2 JetStream ack format.
	FeatureFlagJsAckFormatV2 = "js_ack_fc_v2"
)

// featureFlags is the global registry of known feature flags with their default values.
// Flags start as false (disabled) and are expected to eventually become true (enabled)
// before being removed entirely once fully rolled out.
var featureFlags = map[string]bool{
	FeatureFlagJsAckFormatV2: false,
}

// getFeatureFlag returns the effective value of a feature flag, considering
// both the global default and any user-configured override.
// Must be called on a stable Options reference (e.g., from getOpts()).
func (o *Options) getFeatureFlag(k string) bool {
	if o.FeatureFlags != nil {
		if v, ok := o.FeatureFlags[k]; ok {
			return v
		}
	}
	return featureFlags[k]
}

// getMergedFeatureFlags returns a merged view of all known feature flags
// with user-configured overrides applied. Unknown user flags are filtered out.
func (o *Options) getMergedFeatureFlags() map[string]bool {
	merged := make(map[string]bool, len(featureFlags))
	for k, v := range featureFlags {
		merged[k] = v
	}
	if o.FeatureFlags != nil {
		for k, v := range o.FeatureFlags {
			if _, known := featureFlags[k]; known {
				merged[k] = v
			}
		}
	}
	return merged
}
