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
	"fmt"
	"os"
	"testing"
)

func TestConfigCheck(t *testing.T) {
	tests := []struct {
		// name is the name of the test.
		name string

		// config is content of the configuration file.
		config string

		// defaultErr is the error we get pedantic checks are not enabled.
		defaultErr error

		// pedanticErr is the error we get when pedantic checks are enabled.
		pedanticErr error

		// errorLine is the location of the error.
		errorLine int
	}{
		{
			name: "when unknown field is used at top level",
			config: `
                monitor = "127.0.0.1:4442"
                `,
			defaultErr:  nil,
			pedanticErr: errors.New(`unknown field "monitor"`),
			errorLine:   2,
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
			pedanticErr: errors.New(`unknown field "default_permissions"`),

			// NOTE: line number is '5' because it is where the map definition ends.
			errorLine: 5,
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
			pedanticErr: errors.New(`unknown field "foo"`),
			errorLine:   3,
		},
		{
			name: "when authorization config has unknown fields",
			config: `
                port = 4222

		authorization = {
                  user = "hello"
                  foo = "bar"
		  password = "world"
		}

		`,
			defaultErr:  nil,
			pedanticErr: errors.New(`unknown field "foo"`),
			errorLine:   6,
		},
		{
			name: "when user authorization config has unknown fields",
			config: `
		authorization = {
		  users = [
		    { 
                      user = "foo"
                      pass = "bar"
                      token = "quux"
                    }
		  ]
		}
		`,
			defaultErr:  nil,
			pedanticErr: errors.New(`unknown field "token"`),
			errorLine:   7,
		},
		{
			name: "when user authorization permissions config has unknown fields",
			config: `
		authorization {
		  permissions {
		    subscribe = {}
		    inboxes = {}
		    publish = {}
		  }
		}
		`,
			defaultErr:  errors.New(`Unknown field inboxes parsing permissions`),
			pedanticErr: errors.New(`unknown field "inboxes"`),
			errorLine:   5,
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
			pedanticErr: errors.New(`unknown field "inboxes"`),
			errorLine:   6,
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
                # NATS Server Configuration
                port = 4222

		cluster = {

                  port = 6222

		  foo = "bar"

                  authorization {
                    user = "hello"
                    pass = "world"
                  }

		}
		`,

			defaultErr:  nil,
			pedanticErr: errors.New(`unknown field "foo"`),
			errorLine:   9,
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
			pedanticErr: errors.New(`unknown field "foo"`),
			errorLine:   4,
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
			defaultErr:  errors.New(`Unknown field hello parsing permissions`),
			pedanticErr: errors.New(`unknown field "hello"`),
			errorLine:   7,
		},
		// 		{
		// 			name: "when unknown option is in tls config",
		// 			config: `
		// tls = {
		//   hello = "world"
		// }
		// `,
		// 			// Backwards compatibility: also report error by default even if pedantic checks disabled.
		// 			defaultErr:  errors.New(`error parsing tls config, unknown field ["hello"]`),
		// 			pedanticErr: errors.New(`error parsing tls config, unknown field ["hello"]`),
		// 		},
		// 		{
		// 			name: "when unknown option is in cluster tls config",
		// 			config: `
		// cluster {
		//   tls = {
		//     foo = "bar"
		//   }
		// }
		// `,
		// 			// Backwards compatibility: also report error by default even if pedantic checks disabled.
		// 			defaultErr:  errors.New(`error parsing tls config, unknown field ["foo"]`),
		// 			pedanticErr: errors.New(`error parsing tls config, unknown field ["foo"]`),
		// 		},
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
		}
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			conf := createConfFile(t, []byte(test.config))
			defer os.Remove(conf)

			t.Run("with pedantic check enabled", func(t *testing.T) {
				err := checkConfig(conf, true)
				expectedErr := test.pedanticErr
				if err != nil && expectedErr != nil {
					msg := fmt.Sprintf("%s in %s:%d", expectedErr.Error(), conf, test.errorLine)
					if err.Error() != msg {
						t.Errorf("Expected %q, got %q", msg, err.Error())
					}
				}
				checkErr(t, err, test.pedanticErr)
			})

			t.Run("with pedantic check disabled", func(t *testing.T) {
				err := checkConfig(conf, false)
				expectedErr := test.defaultErr
				if err != nil && expectedErr != nil && err.Error() != expectedErr.Error() {
					t.Errorf("Expected %q, got %q", expectedErr.Error(), err.Error())
				}
				checkErr(t, err, test.defaultErr)
			})
		})
	}
}
