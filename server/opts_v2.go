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
	"fmt"
	"strings"
	"time"

	v2 "github.com/nats-io/nats-server/v2/conf/v2"
)

// configV2Wrapper is an intermediate struct used by ProcessConfigV2 to
// unmarshal a NATS config file into Options. It embeds *Options so that
// simple fields (those with conf struct tags on Options) are populated
// directly. Fields that require special/polymorphic handling are declared
// as overlay fields with custom Unmarshaler types (JetStreamValue,
// ListenValue, AuthorizationConfig, AccountsConfig, TLSConfigOpts).
// After unmarshaling, the overlay fields are post-processed to populate
// the corresponding Options fields.
type configV2Wrapper struct {
	*Options

	// Listen is the "listen" field that parses host:port or plain port.
	Listen ListenValue `conf:"listen"`
	// HTTP is the "http" monitoring endpoint field.
	HTTP ListenValue `conf:"http"`
	// HTTPS is the "https" monitoring endpoint field.
	HTTPS ListenValue `conf:"https"`
	// JetStreamConfig captures the polymorphic jetstream block.
	JetStreamConfig *JetStreamValue `conf:"jetstream"`
	// TLSBlock captures the tls configuration block.
	TLSBlock *TLSConfigOpts `conf:"tls"`
	// Auth captures the authorization block.
	Auth *AuthorizationConfig `conf:"authorization"`
	// AccountsBlock captures the accounts block.
	AccountsBlock *AccountsConfig `conf:"accounts"`
}

// ProcessConfigV2 parses a NATS configuration file using the conf/v2
// unmarshal engine and returns a populated *Options. It uses struct tags
// and custom Unmarshaler implementations from the server package to
// populate Options fields, then applies post-processing for computed
// and dependent fields.
func ProcessConfigV2(configFile string) (*Options, error) {
	opts := &Options{}
	if err := processConfigV2(configFile, opts); err != nil {
		return nil, err
	}
	return opts, nil
}

// processConfigV2 performs the actual config processing.
func processConfigV2(configFile string, opts *Options) error {
	opts.ConfigFile = configFile

	// Step 1: Compute the config digest using v2 parser.
	_, digest, err := v2.ParseFileWithChecksDigest(configFile)
	if err != nil {
		return err
	}
	opts.configDigest = digest

	// Step 2: Unmarshal the config file into the wrapper struct.
	// The wrapper embeds *Options so simple tagged fields are populated
	// directly. Overlay fields handle polymorphic/complex blocks.
	wrapper := &configV2Wrapper{Options: opts}
	if err := v2.UnmarshalFile(configFile, wrapper); err != nil {
		return fmt.Errorf("error processing config file: %w", err)
	}

	// Step 3: Post-process overlay fields into Options.
	if err := postProcessV2(wrapper); err != nil {
		return err
	}

	return nil
}

// postProcessV2 applies computed/dependent field processing after
// the initial unmarshal. This handles fields that v1's processConfigFile
// sets via special parsing functions rather than direct assignment.
func postProcessV2(w *configV2Wrapper) error {
	o := w.Options

	// Process "listen" -> Host, Port
	if w.Listen.Port != 0 || w.Listen.Host != "" {
		o.Host = w.Listen.Host
		o.Port = w.Listen.Port
	}

	// Process "http" -> HTTPHost, HTTPPort
	if w.HTTP.Port != 0 || w.HTTP.Host != "" {
		o.HTTPHost = w.HTTP.Host
		o.HTTPPort = w.HTTP.Port
	}

	// Process "https" -> HTTPHost, HTTPSPort
	if w.HTTPS.Port != 0 || w.HTTPS.Host != "" {
		o.HTTPHost = w.HTTPS.Host
		o.HTTPSPort = w.HTTPS.Port
	}

	// Process JetStream configuration.
	if w.JetStreamConfig != nil {
		js := w.JetStreamConfig
		o.JetStream = js.Enabled
		if js.StoreDir != _EMPTY_ {
			// Check for duplicate store_dir with top-level setting.
			if o.StoreDir != _EMPTY_ && o.StoreDir != js.StoreDir {
				return fmt.Errorf("duplicate 'store_dir' configuration")
			}
			o.StoreDir = js.StoreDir
		}
		if js.MaxMemory != 0 {
			o.JetStreamMaxMemory = js.MaxMemory
			o.maxMemSet = js.MaxMemSet
		}
		if js.MaxStore != 0 {
			o.JetStreamMaxStore = js.MaxStore
			o.maxStoreSet = js.MaxStoreSet
		}
		if js.Domain != _EMPTY_ {
			o.JetStreamDomain = js.Domain
		}
		if js.Key != _EMPTY_ {
			o.JetStreamKey = js.Key
		}
		if js.OldKey != _EMPTY_ {
			o.JetStreamOldKey = js.OldKey
		}
		if js.Cipher != _EMPTY_ {
			cipher, err := parseJetStreamCipherV2(js.Cipher)
			if err != nil {
				return err
			}
			o.JetStreamCipher = cipher
		}
		if js.UniqueTag != _EMPTY_ {
			o.JetStreamUniqueTag = js.UniqueTag
		}
		if js.ExtHint != _EMPTY_ {
			o.JetStreamExtHint = js.ExtHint
		}
		if js.SyncInterval != _EMPTY_ {
			o.SyncInterval = parseSyncIntervalDuration(js.SyncInterval)
		}
		if js.SyncAlways {
			o.SyncAlways = true
			o.SyncInterval = defaultSyncInterval
		}
		o.syncSet = js.SyncSet
		o.NoJetStreamStrict = js.NoStrict
		if js.MaxCatchup != 0 {
			o.JetStreamMaxCatchup = js.MaxCatchup
		}
		if js.MaxBufferedSize != 0 {
			o.StreamMaxBufferedSize = js.MaxBufferedSize
		}
		if js.MaxBufferedMsgs != 0 {
			o.StreamMaxBufferedMsgs = int(js.MaxBufferedMsgs)
		}
		if js.RequestQueueLimit != 0 {
			o.JetStreamRequestQueueLimit = js.RequestQueueLimit
		}
		if js.InfoQueueLimit != 0 {
			o.JetStreamInfoQueueLimit = js.InfoQueueLimit
		}
		if js.MetaCompact != 0 {
			o.JetStreamMetaCompact = uint64(js.MetaCompact)
		}
		if js.MetaCompactSize != 0 {
			o.JetStreamMetaCompactSize = uint64(js.MetaCompactSize)
		}
		o.JetStreamMetaCompactSync = js.MetaCompactSync
	}

	// Process TLS configuration.
	if w.TLSBlock != nil {
		tc := w.TLSBlock
		tlsConfig, err := GenTLSConfig(tc)
		if err != nil {
			return fmt.Errorf("error generating TLS config: %w", err)
		}
		o.TLSConfig = tlsConfig
		o.TLSTimeout = tc.Timeout
		o.TLSMap = tc.Map
		o.TLSPinnedCerts = tc.PinnedCerts
		o.TLSRateLimit = tc.RateLimit
		o.TLSHandshakeFirst = tc.HandshakeFirst
		o.TLSHandshakeFirstFallback = tc.FallbackDelay
		o.tlsConfigOpts = tc
	}

	// Process Authorization.
	if w.Auth != nil {
		auth := w.Auth
		o.Username = auth.User
		o.Password = auth.Pass
		o.Authorization = auth.Token
		o.AuthTimeout = auth.Timeout
		o.AuthCallout = auth.Callout
		o.authBlockDefined = true

		if auth.Users != nil {
			o.Users = append(o.Users, auth.Users...)
		}
		if auth.Nkeys != nil {
			o.Nkeys = append(o.Nkeys, auth.Nkeys...)
		}
		if auth.DefaultPermissions != nil {
			// Apply default permissions to users without explicit permissions.
			for _, u := range o.Users {
				if u.Permissions == nil {
					u.Permissions = auth.DefaultPermissions
				}
			}
			for _, nk := range o.Nkeys {
				if nk.Permissions == nil {
					nk.Permissions = auth.DefaultPermissions
				}
			}
		}
	}

	// Process Accounts.
	if w.AccountsBlock != nil {
		acc := w.AccountsBlock
		o.Accounts = append(o.Accounts, acc.Accounts...)
		o.Users = append(o.Users, acc.Users...)
		o.Nkeys = append(o.Nkeys, acc.Nkeys...)
	}

	return nil
}

// parseJetStreamCipherV2 converts a cipher name string to StoreCipher.
func parseJetStreamCipherV2(name string) (StoreCipher, error) {
	switch strings.ToLower(name) {
	case "chacha", "chachapoly":
		return ChaCha, nil
	case "aes":
		return AES, nil
	default:
		return 0, fmt.Errorf("unknown cipher type: %q", name)
	}
}

// parseSyncIntervalDuration parses a sync interval string as a duration.
func parseSyncIntervalDuration(s string) time.Duration {
	// Try to parse as a Go duration string.
	if d, err := time.ParseDuration(s); err == nil {
		return d
	}
	return 0
}
