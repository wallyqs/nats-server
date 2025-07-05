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
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	bolt "go.etcd.io/bbolt"
)

var (
	// Bucket names for organizing data in BoltDB
	streamsBucket    = []byte("streams")
	consumersBucket  = []byte("consumers")
	metadataBucket   = []byte("metadata")
	snapshotsBucket  = []byte("snapshots")
	
	// Errors
	ErrMetadataStoreNotOpen = errors.New("metadata store not open")
	ErrInvalidKey          = errors.New("invalid key")
)

// MetadataStore provides a CoW B-tree based storage for JetStream metadata
type MetadataStore struct {
	mu       sync.RWMutex
	db       *bolt.DB
	path     string
	readonly bool
}

// OpenMetadataStore opens or creates a metadata store at the given path
func OpenMetadataStore(path string) (*MetadataStore, error) {
	dbPath := filepath.Join(path, "jetstream_metadata.db")
	
	// Ensure directory exists
	if err := os.MkdirAll(path, 0755); err != nil {
		return nil, err
	}
	
	db, err := bolt.Open(dbPath, 0600, &bolt.Options{
		Timeout:      1 * time.Second,
		NoGrowSync:   false,
		FreelistType: bolt.FreelistArrayType,
	})
	if err != nil {
		return nil, err
	}
	
	// Initialize buckets
	err = db.Update(func(tx *bolt.Tx) error {
		for _, bucket := range [][]byte{streamsBucket, consumersBucket, metadataBucket, snapshotsBucket} {
			if _, err := tx.CreateBucketIfNotExists(bucket); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		db.Close()
		return nil, err
	}
	
	return &MetadataStore{
		db:   db,
		path: path,
	}, nil
}

// Close closes the metadata store
func (ms *MetadataStore) Close() error {
	ms.mu.Lock()
	defer ms.mu.Unlock()
	
	if ms.db == nil {
		return nil
	}
	
	err := ms.db.Close()
	ms.db = nil
	return err
}

// streamKey generates a key for a stream assignment
func streamKey(account, stream string) []byte {
	return []byte(fmt.Sprintf("%s:%s", account, stream))
}

// consumerKey generates a key for a consumer assignment
func consumerKey(account, stream, consumer string) []byte {
	return []byte(fmt.Sprintf("%s:%s:%s", account, stream, consumer))
}

// PutStreamAssignment stores a stream assignment
func (ms *MetadataStore) PutStreamAssignment(account string, sa *streamAssignment) error {
	ms.mu.Lock()
	defer ms.mu.Unlock()
	
	if ms.db == nil {
		return ErrMetadataStoreNotOpen
	}
	
	if ms.readonly {
		return errors.New("metadata store is readonly")
	}
	
	// Convert to writeable format for storage
	wsa := writeableStreamAssignment{
		Client:    sa.Client.forAssignmentSnap(),
		Created:   sa.Created,
		Config:    sa.Config,
		Group:     sa.Group,
		Sync:      sa.Sync,
		Consumers: make([]*consumerAssignment, 0, len(sa.consumers)),
	}
	
	// Note: We store consumers separately, not nested
	
	data, err := json.Marshal(wsa)
	if err != nil {
		return err
	}
	
	return ms.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(streamsBucket)
		return b.Put(streamKey(account, sa.Config.Name), data)
	})
}

// GetStreamAssignment retrieves a stream assignment
func (ms *MetadataStore) GetStreamAssignment(account, stream string) (*streamAssignment, error) {
	ms.mu.RLock()
	defer ms.mu.RUnlock()
	
	if ms.db == nil {
		return nil, ErrMetadataStoreNotOpen
	}
	
	var wsa writeableStreamAssignment
	err := ms.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(streamsBucket)
		data := b.Get(streamKey(account, stream))
		if data == nil {
			return nil
		}
		return json.Unmarshal(data, &wsa)
	})
	
	if err != nil {
		return nil, err
	}
	
	if wsa.Config == nil {
		return nil, nil
	}
	
	sa := &streamAssignment{
		Client:    wsa.Client,
		Created:   wsa.Created,
		Config:    wsa.Config,
		Group:     wsa.Group,
		Sync:      wsa.Sync,
		consumers: make(map[string]*consumerAssignment),
	}
	
	return sa, nil
}

// DeleteStreamAssignment removes a stream assignment
func (ms *MetadataStore) DeleteStreamAssignment(account, stream string) error {
	ms.mu.Lock()
	defer ms.mu.Unlock()
	
	if ms.db == nil {
		return ErrMetadataStoreNotOpen
	}
	
	if ms.readonly {
		return errors.New("metadata store is readonly")
	}
	
	return ms.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(streamsBucket)
		return b.Delete(streamKey(account, stream))
	})
}

// PutConsumerAssignment stores a consumer assignment
func (ms *MetadataStore) PutConsumerAssignment(account string, ca *consumerAssignment) error {
	ms.mu.Lock()
	defer ms.mu.Unlock()
	
	if ms.db == nil {
		return ErrMetadataStoreNotOpen
	}
	
	if ms.readonly {
		return errors.New("metadata store is readonly")
	}
	
	// Clear transient fields before storage
	cca := *ca
	cca.Client = cca.Client.forAssignmentSnap()
	cca.Subject, cca.Reply = _EMPTY_, _EMPTY_
	
	data, err := json.Marshal(&cca)
	if err != nil {
		return err
	}
	
	return ms.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(consumersBucket)
		return b.Put(consumerKey(account, ca.Stream, ca.Name), data)
	})
}

// GetConsumerAssignment retrieves a consumer assignment
func (ms *MetadataStore) GetConsumerAssignment(account, stream, consumer string) (*consumerAssignment, error) {
	ms.mu.RLock()
	defer ms.mu.RUnlock()
	
	if ms.db == nil {
		return nil, ErrMetadataStoreNotOpen
	}
	
	var ca consumerAssignment
	err := ms.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(consumersBucket)
		data := b.Get(consumerKey(account, stream, consumer))
		if data == nil {
			return nil
		}
		return json.Unmarshal(data, &ca)
	})
	
	if err != nil {
		return nil, err
	}
	
	if ca.Name == "" {
		return nil, nil
	}
	
	return &ca, nil
}

// DeleteConsumerAssignment removes a consumer assignment
func (ms *MetadataStore) DeleteConsumerAssignment(account, stream, consumer string) error {
	ms.mu.Lock()
	defer ms.mu.Unlock()
	
	if ms.db == nil {
		return ErrMetadataStoreNotOpen
	}
	
	if ms.readonly {
		return errors.New("metadata store is readonly")
	}
	
	return ms.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(consumersBucket)
		return b.Delete(consumerKey(account, stream, consumer))
	})
}

// AllStreamAssignments returns all stream assignments
func (ms *MetadataStore) AllStreamAssignments() (map[string]map[string]*streamAssignment, error) {
	ms.mu.RLock()
	defer ms.mu.RUnlock()
	
	if ms.db == nil {
		return nil, ErrMetadataStoreNotOpen
	}
	
	streams := make(map[string]map[string]*streamAssignment)
	
	err := ms.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(streamsBucket)
		return b.ForEach(func(k, v []byte) error {
			var wsa writeableStreamAssignment
			if err := json.Unmarshal(v, &wsa); err != nil {
				return err
			}
			
			account := wsa.Client.serviceAccount()
			if streams[account] == nil {
				streams[account] = make(map[string]*streamAssignment)
			}
			
			sa := &streamAssignment{
				Client:    wsa.Client,
				Created:   wsa.Created,
				Config:    wsa.Config,
				Group:     wsa.Group,
				Sync:      wsa.Sync,
				consumers: make(map[string]*consumerAssignment),
			}
			
			streams[account][wsa.Config.Name] = sa
			return nil
		})
	})
	
	if err != nil {
		return nil, err
	}
	
	// Load all consumers
	err = ms.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(consumersBucket)
		return b.ForEach(func(k, v []byte) error {
			var ca consumerAssignment
			if err := json.Unmarshal(v, &ca); err != nil {
				return err
			}
			
			account := ca.Client.serviceAccount()
			if sa, ok := streams[account][ca.Stream]; ok {
				sa.consumers[ca.Name] = &ca
			}
			return nil
		})
	})
	
	return streams, err
}

// CreateSnapshot creates a CoW snapshot and returns a reader for it
func (ms *MetadataStore) CreateSnapshot() (io.ReadCloser, error) {
	ms.mu.RLock()
	defer ms.mu.RUnlock()
	
	if ms.db == nil {
		return nil, ErrMetadataStoreNotOpen
	}
	
	// Create a read transaction to get a consistent view
	tx, err := ms.db.Begin(false)
	if err != nil {
		return nil, err
	}
	
	// Create a pipe to stream the data
	pr, pw := io.Pipe()
	
	go func() {
		defer tx.Rollback()
		defer pw.Close()
		
		// Stream all data as JSON
		encoder := json.NewEncoder(pw)
		
		// First write a header
		header := map[string]interface{}{
			"version":   1,
			"timestamp": time.Now().Unix(),
		}
		if err := encoder.Encode(header); err != nil {
			pw.CloseWithError(err)
			return
		}
		
		// Stream all streams
		streamsBkt := tx.Bucket(streamsBucket)
		if err := streamsBkt.ForEach(func(k, v []byte) error {
			record := map[string]interface{}{
				"type": "stream",
				"key":  string(k),
				"data": json.RawMessage(v),
			}
			return encoder.Encode(record)
		}); err != nil {
			pw.CloseWithError(err)
			return
		}
		
		// Stream all consumers
		consumersBkt := tx.Bucket(consumersBucket)
		if err := consumersBkt.ForEach(func(k, v []byte) error {
			record := map[string]interface{}{
				"type": "consumer",
				"key":  string(k),
				"data": json.RawMessage(v),
			}
			return encoder.Encode(record)
		}); err != nil {
			pw.CloseWithError(err)
			return
		}
	}()
	
	return pr, nil
}

// ApplySnapshot restores from a snapshot
func (ms *MetadataStore) ApplySnapshot(r io.Reader) error {
	ms.mu.Lock()
	defer ms.mu.Unlock()
	
	if ms.db == nil {
		return ErrMetadataStoreNotOpen
	}
	
	if ms.readonly {
		return errors.New("metadata store is readonly")
	}
	
	// Clear existing data and apply snapshot
	return ms.db.Update(func(tx *bolt.Tx) error {
		// Clear existing buckets
		for _, bucketName := range [][]byte{streamsBucket, consumersBucket} {
			if err := tx.DeleteBucket(bucketName); err != nil && err != bolt.ErrBucketNotFound {
				return err
			}
			if _, err := tx.CreateBucket(bucketName); err != nil {
				return err
			}
		}
		
		// Apply snapshot data
		decoder := json.NewDecoder(r)
		
		// Read header
		var header map[string]interface{}
		if err := decoder.Decode(&header); err != nil {
			return err
		}
		
		// Read records
		for {
			var record map[string]interface{}
			if err := decoder.Decode(&record); err != nil {
				if err == io.EOF {
					break
				}
				return err
			}
			
			recordType, _ := record["type"].(string)
			key, _ := record["key"].(string)
			data, _ := record["data"].(json.RawMessage)
			
			switch recordType {
			case "stream":
				b := tx.Bucket(streamsBucket)
				if err := b.Put([]byte(key), data); err != nil {
					return err
				}
			case "consumer":
				b := tx.Bucket(consumersBucket)
				if err := b.Put([]byte(key), data); err != nil {
					return err
				}
			}
		}
		
		return nil
	})
}

// Compact runs a manual compaction on the database
func (ms *MetadataStore) Compact() error {
	ms.mu.Lock()
	defer ms.mu.Unlock()
	
	if ms.db == nil {
		return ErrMetadataStoreNotOpen
	}
	
	// BoltDB doesn't expose a direct compact method, but we can trigger
	// a rewrite by copying to a new file
	tempPath := ms.path + ".compact"
	
	// Open new database
	newDB, err := bolt.Open(tempPath, 0600, &bolt.Options{
		Timeout:      1 * time.Second,
		NoGrowSync:   false,
		FreelistType: bolt.FreelistArrayType,
	})
	if err != nil {
		return err
	}
	
	// Copy all data
	err = ms.db.View(func(oldTx *bolt.Tx) error {
		return newDB.Update(func(newTx *bolt.Tx) error {
			return oldTx.ForEach(func(name []byte, b *bolt.Bucket) error {
				newBucket, err := newTx.CreateBucketIfNotExists(name)
				if err != nil {
					return err
				}
				return b.ForEach(func(k, v []byte) error {
					return newBucket.Put(k, v)
				})
			})
		})
	})
	
	if err != nil {
		newDB.Close()
		os.Remove(tempPath)
		return err
	}
	
	// Close both databases
	ms.db.Close()
	newDB.Close()
	
	// Replace old with new
	if err := os.Rename(tempPath, ms.path); err != nil {
		return err
	}
	
	// Reopen
	ms.db, err = bolt.Open(ms.path, 0600, &bolt.Options{
		Timeout:      1 * time.Second,
		NoGrowSync:   false,
		FreelistType: bolt.FreelistArrayType,
	})
	
	return err
}

// MetadataSnapshot represents a point-in-time snapshot of metadata
type MetadataSnapshot struct {
	data   []byte
	reader *bytes.Reader
}

// Read implements io.Reader
func (ms *MetadataSnapshot) Read(p []byte) (n int, err error) {
	if ms.reader == nil {
		ms.reader = bytes.NewReader(ms.data)
	}
	return ms.reader.Read(p)
}

// Close implements io.Closer
func (ms *MetadataSnapshot) Close() error {
	ms.data = nil
	ms.reader = nil
	return nil
}

// Size returns the size of the snapshot
func (ms *MetadataSnapshot) Size() int {
	return len(ms.data)
}