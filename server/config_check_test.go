// Copyright 2018 The NATS Authors
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
	"errors"
	"os"
	"testing"
)

func TestConfigurationPedanticCheck(t *testing.T) {
	tests := []struct {
		name   string
		config string

		// defaultErr is the error we get pedantic checks are not enabled.
		defaultErr error

		// pedanticErr is the error we get when pedantic checks are enabled.
		pedanticErr error
	}{
		{
			name: "when unknown field is used at top level",
			config: `
monitor = "127.0.0.1:4442"
`,
			defaultErr:  nil,
			pedanticErr: errors.New(`Unknown field "monitor"`),
		},
		{
			name: "when default permissions are used at top level",
			config: `
"default_permissions" {
  publish = ["_SANDBOX.>"]
  subscribe = ["_SANDBOX.>"]
}
`,
			defaultErr:  nil,
			pedanticErr: errors.New(`Unknown field "default_permissions"`),
		},
		{
			name: "when authorization config is empty",
			config: `
authorization = {
}
`,
			defaultErr:  nil,
			pedanticErr: nil,
		},
		{
			name: "when authorization config has unknown fields",
			config: `
authorization = {
  foo = "bar"
}
`,
			defaultErr:  nil,
			pedanticErr: errors.New(`Unknown field "foo" within authorization config`),
		},
		{
			name: "when user authorization config has unknown fields",
			config: `
authorization = {
  users = [
    { user = "foo", pass = "bar", token = "quux" }
  ]
}
`,
			defaultErr:  nil,
			pedanticErr: errors.New(`Unknown field "token" within user authorization config`),
		},
		{
			name: "when user authorization permissions config is empty",
			config: `
authorization = {
  users = [
    { 
      user = "foo", pass = "bar", permissions = {
      }
    }
  ]
}
`,
			defaultErr:  nil,
			pedanticErr: nil,
		},
		{
			name: "when unknown permissions are included in config",
			config: `
authorization = {
  users = [
    { 
      user = "foo", pass = "bar", permissions {
        inboxes = true
      }
    }
  ]
}
`,
			defaultErr:  errors.New(`Unknown field inboxes parsing permissions`),
			pedanticErr: errors.New(`Unknown field inboxes parsing permissions`),
		},
		{
			name: "when clustering config is empty",
			config: `
cluster = {
}
`,

			defaultErr:  nil,
			pedanticErr: nil,
		},
		{
			name: "when unknown option is in clustering config",
			config: `
cluster = {
  foo = "bar"
}
`,

			defaultErr:  nil,
			pedanticErr: errors.New(`Unknown field "foo" within cluster config`),
		},
		{
			name: "when unknown option is in clustering authorization config",
			config: `
cluster = {
  authorization {
    foo = "bar"
  }
}
`,

			defaultErr:  nil,
			pedanticErr: errors.New(`Unknown field "foo" within authorization config`),
		},
		{
			name: "when unknown option is in clustering authorization permissions config",
			config: `
cluster = {
  authorization {
    user = "foo"
    pass = "bar"
    permissions = {
      hello = "world"
    }
  }
}
`,
			// Backwards compatibility: also report error by default
			defaultErr:  errors.New(`Unknown field hello parsing permissions`),
			pedanticErr: errors.New(`Unknown field hello parsing permissions`),
		},
	}

	checkConfig := func(config string, pedantic bool) error {
		opts := &Options{
			CheckConfig: pedantic,
		}
		return opts.ProcessConfigFile(config)
	}

	checkErr := func(t *testing.T, err, expectedErr error) {
		t.Helper()
		switch {
		case err == nil && expectedErr == nil:
			// OK
		case err != nil && expectedErr == nil:
			t.Errorf("Unexpected error after processing config: %s", err)
		case err == nil && expectedErr != nil:
			t.Errorf("Expected %q error after processing invalid config but got nothing", expectedErr)
		case err != nil && expectedErr != nil && err.Error() != expectedErr.Error():
			t.Errorf("Expected %q, got %q", expectedErr.Error(), err.Error())
		}
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			conf := createConfFile(t, []byte(test.config))
			defer os.Remove(conf)

			t.Run("with pedantic check enabled", func(t *testing.T) {
				err := checkConfig(conf, true)
				checkErr(t, err, test.pedanticErr)
			})

			t.Run("with pedantic check disabled", func(t *testing.T) {
				err := checkConfig(conf, false)
				checkErr(t, err, test.defaultErr)
			})
		})
	}
}
