package server

import (
	"fmt"
	"testing"
)

func TestJetStreamConfigQuotedSizeSuffixes(t *testing.T) {
	for _, test := range []struct {
		name              string
		conf              string
		wantBufferedSize  int64
		wantCompactSize   uint64
	}{
		{
			"JSON style with trailing commas",
			`{
  "listen": "127.0.0.1:-1",
  "jetstream": {
    "max_buffered_size": "128MiB",
    "meta_compact_size": "2GiB",
    "store_dir": %q,
  },
}`,
			128 * 1024 * 1024,
			2 * 1024 * 1024 * 1024,
		},
		{
			"JSON style without trailing commas",
			`{
  "listen": "127.0.0.1:-1",
  "jetstream": {
    "max_buffered_size": "128MiB",
    "meta_compact_size": "2GiB",
    "store_dir": %q
  }
}`,
			128 * 1024 * 1024,
			2 * 1024 * 1024 * 1024,
		},
		{
			"JSON style with SI suffixes",
			`{
  "listen": "127.0.0.1:-1",
  "jetstream": {
    "max_buffered_size": "128M",
    "meta_compact_size": "2G",
    "store_dir": %q
  }
}`,
			128 * 1000 * 1000,
			2 * 1000 * 1000 * 1000,
		},
		{
			"NATS style unquoted",
			`
listen: 127.0.0.1:-1
jetstream: {
  max_buffered_size: 128MiB
  meta_compact_size: 2GiB
  store_dir: %q
}`,
			128 * 1024 * 1024,
			2 * 1024 * 1024 * 1024,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			conf := createConfFile(t, []byte(fmt.Sprintf(test.conf, t.TempDir())))
			opts, err := ProcessConfigFile(conf)
			if err != nil {
				t.Fatalf("Error processing config: %v", err)
			}
			if opts.StreamMaxBufferedSize != test.wantBufferedSize {
				t.Fatalf("Expected StreamMaxBufferedSize to be %d, got %d", test.wantBufferedSize, opts.StreamMaxBufferedSize)
			}
			if opts.JetStreamMetaCompactSize != test.wantCompactSize {
				t.Fatalf("Expected JetStreamMetaCompactSize to be %d, got %d", test.wantCompactSize, opts.JetStreamMetaCompactSize)
			}
		})
	}
}
