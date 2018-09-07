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

func checkConfig(config string) error {
	opts := &Options{
		CheckConfig: true,
	}
	return opts.ProcessConfigFile(config)
}

func TestConfigPedanticCheck(t *testing.T) {
	tests := []struct {
		name   string
		config string
		err    error
	}{
		////////////////////////////////////////////////////////////////
		//                    Server Options    	              //
		////////////////////////////////////////////////////////////////
		{
			"should complain if unsupported option is used at top level",
			`
monitor = "127.0.0.1:4442"
`,
			errors.New(`Invalid config directive "monitor"`),
		},
		{
			"should complain when invalid value is used for 'port'",
			`
port = "4222"
`,
			errors.New(`Invalid value for "port" directive`),
		},
		{
			"should complain when invalid value is used for 'client_advertise'",
			`
client_advertise = 4222
`,
			errors.New(`Invalid value for "client_advertise" directive`),
		},
		{
			"should complain when invalid value is used for 'host'",
			`
host = true
`,
			errors.New(`Invalid value for "host" directive`),
		},
		{
			"should complain when invalid value is used for 'host'",
			`
host = 4222
`,
			errors.New(`Invalid value for "host" directive`),
		},
		{
			"should complain when invalid value is used for 'debug'",
			`
debug = "true"
`,
			errors.New(`Invalid value for "debug" directive`),
		},
		{
			"should complain when invalid value is used for 'trace'",
			`
trace = 4222
`,
			errors.New(`Invalid value for "trace" directive`),
		},
		{
			"should complain when string value is used for 'logtime'",
			`
logtime = "true"
`,
			errors.New(`Invalid value for "logtime" directive`),
		},
		////////////////////////////////////////////////////////////////
		//                    Authorization checks    	              //
		////////////////////////////////////////////////////////////////
		{
			"should not complain when 'authorization' block is empty",
			`
port = 4222
authorization {}
`,
			nil,
		},
		{
			"should complain when invalid type is used for authorization",
			`
port = 4222
authorization = "hello.world"
`,
			errors.New(`Invalid value for "authorization" directive`),
		},
		{
			"should complain when invalid type is used for authorization user",
			`
authorization = { 
  user = 12345
}`,
			errors.New(`Invalid value for "user" directive within authorization config`),
		},
		{
			"should complain when invalid type is used for authorization pass",
			`
authorization = {
  pass = 67890
}
`,
			errors.New(`Invalid value for "pass" directive within authorization config`),
		},
		{
			"should complain when invalid type is used for authorization token",
			`
authorization = {
  token = 12345
}
`,
			errors.New(`Invalid value for "token" directive within authorization config`),
		},
		{
			"should complain when invalid type is used for authorization timeout",
			`
authorization = {
  timeout = "12345"
}
`,
			errors.New(`Invalid value for "timeout" directive within authorization config`),
		},
		{
			"should not complain when int type is used for authorization timeout",
			`
authorization = {
  timeout = 120
}
`,
			nil,
		},
		{
			"should not complain when float type is used for authorization timeout",
			`
authorization = {
  timeout = 5.0
}
`,
			nil,
		},
		{
			"should complain when authorization timeout is undefined",
			`
authorization = {
  timeout = ;
  user = "hello"
}
`,
			errors.New(`Invalid value for "timeout" directive within authorization config`),
		},
		{
			"should complain when authorization users have invalid options",
			`
authorization = {
  foo = "bar"
}
`,
			errors.New(`Invalid config directive "foo" within authorization config`),
		},
		{
			"should complain when authorization users are of invalid types",
			`
authorization = {
  users = ["hello"]
}
`,
			errors.New(`Expected user entry to be a map/struct, got hello`),
		},
		{
			"should complain when authorization config uses invalid options for a user",
			`
authorization = {
  users = [
    { user: "foo1", pass: "bar1", token: "notimplemented" },
    { user: "foo2", pass: "bar2", token: "notimplemented"},
    { user: "foo3", pass: "bar3", token: "notimplemented" },
  ]
}
`,
			errors.New(`Invalid config directive "token" within user authorization config`),
		},
		{
			"should complain when authorization config uses invalid values for a user password",
			`
authorization = {
  users = [
    { user: "foo1", pass: 123 },
  ]
}
`,
			errors.New(`Invalid value for "pass" within user authorization config`),
		},
		{
			"should complain when authorization config uses invalid values for a username",
			`
authorization = {
  users = [
    { user: 123, pass: "bar1" },
  ]
}
`,
			errors.New(`Invalid value for "user" within user authorization config`),
		},
		{
			"should complain when authorization config uses invalid values for user permissions",
			`
authorization = {
  users = [
    { user: "foo1", pass: "bar1", permissions: "foo" },
  ]
}
`,
			errors.New(`Expected user permissions to be a map/struct, got foo`),
		},
		{
			"should not complain when authorization config includes empty permissions",
			`
authorization = {
  users = [
    { 
      user: "foo1", pass: "bar1", permissions: {
        publish: [],
        subscribe: []
      }
    }
  ]
}
`,
			nil,
		},
		{
			"should not complain when authorization config includes empty permissions",
			`
authorization = {
  users = [
    { 
      user: "foo1", pass: "bar1", permissions: {
      }
    }
  ]
}
`,
			nil,
		},
		{
			"should complain when authorization config includes invalid options",
			`
authorization = {
  users = [
    { 
      user: "foo1", pass: "bar1", permissions: {
        inboxes: true
      }
    }
  ]
}
`,
			errors.New(`Unknown field "inboxes" parsing permissions`),
		},
		{
			"should complain when authorization config includes empty default permissions",
			`
authorization = {
  default_permissions = {}
}
`,
			nil,
		},
		{
			"should complain when authorization config invalid values in default permissions",
			`
authorization = {
  default_permissions = { inboxes: true }
}
`,
			errors.New(`Unknown field "inboxes" parsing permissions`),
		},
		{
			"should complain when default permissions are defined at the top level",
			`
default_permissions = { 
  publish = ["_SANDBOX.>"]
  subscribe = ["_SANDBOX.>"]
}
`,
			errors.New(`Invalid config directive "default_permissions"`),
		},
		////////////////////////////////////////////////////////////////
		//                    Clustering          	              //
		////////////////////////////////////////////////////////////////
		{
			"should complain when clustering config includes unsupported options",
			`
cluster {
  foo = "bar"
}
`,
			errors.New(`Invalid config directive "foo" within clustering config`),
		},
		{
			"should complain when clustering config has a wrong value type",
			`
cluster = []
`,
			errors.New(`Invalid value for "cluster" directive`),
		},
		// 		{
		// 			"should complain when clustering listen is of invalid type",
		// 			`
		// cluster {
		//   listen = true
		// }
		// `,
		// 			errors.New(`Invalid value for "listen" directive`),
		// 		},
		{
			"should complain when clustering authorization config includes unsupported options",
			`
cluster {
  authorization {
    foo = "bar"
  }
}
`,
			errors.New(`Invalid config directive "foo" within authorization config`),
		},
		{
			"should complain when clustering authorization users config includes unsupported options",
			`
cluster {
  authorization {
    users = [ 
      { user: "hello", pass: "world", token: "secret" }
    ]
  }
}
`,
			errors.New(`Invalid config directive "token" within user authorization config`),
		},
		{
			"should not complain when clustering authorization users config includes unsupported options",
			`
cluster {
  authorization {
    user: "hello"
    pass: "world"
    foo: "secret"
  }
}
`,
			errors.New(`Invalid config directive "foo" within authorization config`),
		},
		{
			"should complain when clustering authorization users config is defined",
			`
cluster {
  authorization {
    users = [ 
      { user: "hello", pass: "world" }
    ]
  }
}
`,
			errors.New(`Cluster authorization does not allow multiple users`),
		},
		{
			"should complain when clustering port is of an invalid type",
			`
cluster {
  port = true
}
`,
			errors.New(`Invalid value for "port" directive in clustering config`),
		},
		{
			"should complain when clustering host is of an invalid type",
			`
cluster {
  host = false
}
`,
			errors.New(`Invalid value for "host" directive in clustering config`),
		},
		{
			"should complain when clustering routes are of an invalid type",
			`
cluster {
  routes = {}
}
`,
			errors.New(`Invalid value for "routes" directive in clustering config`),
		},
		{
			"should complain when clustering tls are of an invalid type",
			`
cluster {
  tls = []
}
`,
			errors.New(`Invalid value for "tls" directive in clustering config`),
		},
		{
			"should complain when clustering advertise is of an invalid type",
			`
cluster {
  cluster_advertise = []
}
`,
			errors.New(`Invalid value for "cluster_advertise" directive in clustering config`),
		},
		{
			"should complain when clustering no advertise is of an invalid type",
			`
cluster {
  no_advertise = []
}
`,
			errors.New(`Invalid value for "no_advertise" directive in clustering config`),
		},
		{
			"should complain when clustering connect_retries are of an invalid type",
			`
cluster {
  connect_retries = false
}
`,
			errors.New(`Invalid value for "connect_retries" directive in clustering config`),
		},
		// 		{
		// 			"should not complain when tls config is empty",
		// 			`
		// tls {
		// }
		// `,
		// 			nil,
		// 		},
		{
			"should complain when tls is of an invalid type",
			`
tls = false
`,
			errors.New(`Invalid value for "tls" directive in config`),
		},
		{
			"should complain when timeout is of an invalid type in tls config",
			`
tls {
  timeout = false
}
`,
			errors.New(`Invalid value for "timeout" directive in TLS config`),
		},
		{
			"should complain when cert_file is of an invalid type in tls config",
			`
tls {
  cert_file = []
}
`,
			errors.New(`error parsing tls config, expected 'cert_file' to be filename`),
		},
		{
			"should complain when cipher_suites are of an invalid type in tls config",
			`
tls {
  cipher_suites = [false]
}
`,
			errors.New(`Invalid value for cipher in TLS 'cipher_suites' config`),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			conf := createConfFile(t, []byte(test.config))
			defer os.Remove(conf)

			err := checkConfig(conf)
			switch {
			case err == nil && test.err == nil:
				// OK
			case err != nil && test.err == nil:
				t.Errorf("Unexpected error after processing config: %s", err)
			case err == nil && test.err != nil:
				t.Errorf("Expected %q error after processing invalid config but got nothing", test.err)
			case err != nil && test.err != nil && err.Error() != test.err.Error():
				t.Errorf("Expected %q, got %q", test.err.Error(), err.Error())
			}
		})
	}
}

func TestConfigPedanticCheckDisabled(t *testing.T) {
	tests := []struct {
		name   string
		config string
		err    error
	}{
		{
			"should not complain if unsupported option is used at top level",
			`monitor = "127.0.0.1:4442"`,
			nil,
		},
		{
			"should not complain when 'authorization' block is empty",
			`
authorization {}
`,
			nil,
		},
		{
			"should complain when authorization timeout is undefined",
			`
authorization = {
  timeout = ;
  user = "hello"
}
`,
			errors.New(`Invalid value for "timeout" directive within authorization config`),
		},
		{
			"should complain when authorization users are of invalid types",
			`
authorization = {
  users = ["hello"]
}
`,
			errors.New(`Expected user entry to be a map/struct, got hello`),
		},
		{
			"should not complain when authorization config uses invalid options",
			`
authorization = {
  foo = "bar"
}
`,
			nil,
		},
		{
			"should not complain when authorization config uses invalid options for a user",
			`
authorization = {
  users = [
    { user: "foo1", pass: "bar1", token: "notimplemented" },
    { user: "foo2", pass: "bar2", token: "notimplemented"},
    { user: "foo3", pass: "bar3", token: "notimplemented" },
  ]
}
`,
			nil,
		},
		{
			"should complain when authorization config includes invalid options",
			`
authorization = {
  users = [
    { 
      user: "foo1", pass: "bar1", permissions: {
        inboxes: true
      }
    }
  ]
}
`,
			errors.New(`Unknown field "inboxes" parsing permissions`),
		},
		{
			"should not complain when default permissions are defined on the top level",
			`
default_permissions = { 
  publish = ["_SANDBOX.>"]
  subscribe = ["_SANDBOX.>"]
}
clustering {
}
`,
			nil,
		},
		{
			"should not complain when clustering config includes unsupported options",
			`
cluster {
  foo = "bar"
}
`,
			nil,
		},
		{
			"should complain when clustering config has a wrong value type",
			`
cluster = []
`,
			errors.New(`Invalid value for "cluster" directive`),
		},
		{
			"should not complain when clustering authorization config includes unsupported options",
			`
cluster {
  authorization {
    foo = "bar"
  }
}
`,
			nil,
		},
		{
			"should not complain when clustering authorization config is empty",
			`
cluster {
}
`,
			nil,
		},
		{
			"should not complain when clustering authorization users config includes unsupported options",
			`
cluster {
  authorization {
    user: "hello"
    pass: "world"
    token: "secret"
  }
}
`,
			nil,
		},
		{
			"should complain when clustering authorization users config is defined",
			`
cluster {
  authorization {
    users = [ 
      { user: "hello", pass: "world" }
    ]
  }
}
`,
			errors.New(`Cluster authorization does not allow multiple users`),
		},
		{
			"should complain when clustering port is of an invalid type",
			`
cluster {
  port = true
}
`,
			errors.New(`Invalid value for "port" directive in clustering config`),
		},
		{
			"should complain when clustering host is of an invalid type",
			`
cluster {
  host = false
}
`,
			errors.New(`Invalid value for "host" directive in clustering config`),
		},
		{
			"should complain when clustering routes are of an invalid type",
			`
cluster {
  routes = {}
}
`,
			errors.New(`Invalid value for "routes" directive in clustering config`),
		},
		{
			"should complain when clustering tls are of an invalid type",
			`
cluster {
  tls = []
}
`,
			errors.New(`Invalid value for "tls" directive in clustering config`),
		},
		{
			"should complain when clustering advertise is of an invalid type",
			`
cluster {
  cluster_advertise = []
}
`,
			errors.New(`Invalid value for "cluster_advertise" directive in clustering config`),
		},
		{
			"should complain when clustering no advertise is of an invalid type",
			`
cluster {
  no_advertise = []
}
`,
			errors.New(`Invalid value for "no_advertise" directive in clustering config`),
		},
		{
			"should complain when clustering connect_retries are of an invalid type",
			`
cluster {
  connect_retries = false
}
`,
			errors.New(`Invalid value for "connect_retries" directive in clustering config`),
		},
		{
			"should complain when tls is of an invalid type",
			`
tls = false
`,
			errors.New(`Invalid value for "tls" directive in config`),
		},
		{
			"should not complain when timeout is of an invalid type in tls config",
			`
tls {
  timeout = false
}
`,
			errors.New(`error parsing X509 certificate/key pair: open : no such file or directory`),
		},
		{
			"should complain when cert_file is of an invalid type in tls config",
			`
tls {
  cert_file = []
}
`,
			errors.New(`error parsing tls config, expected 'cert_file' to be filename`),
		},
		{
			"should complain when cipher_suites are of an invalid type in tls config",
			`
tls {
  cipher_suites = [false]
}
`,
			errors.New(`Invalid value for cipher in TLS 'cipher_suites' config`),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			conf := createConfFile(t, []byte(test.config))
			defer os.Remove(conf)

			opts := &Options{}
			err := opts.ProcessConfigFile(conf)

			switch {
			case err == nil && test.err == nil:
				// OK
			case err != nil && test.err == nil:
				t.Errorf("Unexpected error after processing config: %s", err)
			case err == nil && test.err != nil:
				t.Errorf("Expected %q error after processing invalid config but got nothing", test.err)
			case err != nil && test.err != nil && err.Error() != test.err.Error():
				t.Errorf("Expected %q, got %q", test.err.Error(), err.Error())
			}
		})
	}
}
