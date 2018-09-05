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
		{
			"should complain when invalid value is used",
			`port = "4222"`,
			errors.New(`invalid value for "port" directive`),
		},
		{
			"should complain if unsupported option is used",
			`monitor = "127.0.0.1:4442"`,
			errors.New(`invalid config directive "monitor"`),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			conf := createConfFile(t, []byte(test.config))
			defer os.Remove(conf)

			err := checkConfig(conf)
			t.Logf("========== %v", err)
			if err == nil && test.err != nil {
				t.Errorf("Expected error processing invalid config")
			}

			if err != nil && test.err != nil && err.Error() != test.err.Error() {
				t.Errorf("Expected %q, got %q", test.err.Error(), err.Error())
			}
		})
	}
}
