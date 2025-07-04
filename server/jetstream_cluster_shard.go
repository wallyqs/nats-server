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
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"hash"
	"sort"
	"sync"
	"time"

	"github.com/klauspost/compress/s2"
)

const (
	// Number of shards to split the snapshot into
	defaultMetaSnapshotShards = 16
	// Magic bytes for sharded snapshot format
	shardedSnapshotMagic = "NATS_SHARD_V1"
	// Maximum concurrent shard processing
	maxConcurrentShards = 8
)

// shardedSnapshot represents the header for a sharded snapshot
type shardedSnapshotHeader struct {
	Magic      string                 `json:"magic"`
	Version    int                    `json:"version"`
	NumShards  int                    `json:"num_shards"`
	Created    time.Time              `json:"created"`
	TotalSize  int64                  `json:"total_size"`
	ShardIndex map[string]shardInfo   `json:"shard_index"`
}

// shardInfo contains metadata about a single shard
type shardInfo struct {
	ID         int    `json:"id"`
	Size       int64  `json:"size"`
	Checksum   string `json:"checksum"`
	NumStreams int    `json:"num_streams"`
}

// shardedSnapshotWriter manages writing sharded snapshots
type shardedSnapshotWriter struct {
	numShards int
	shards    map[int]*shardData
	mu        sync.Mutex
}

// shardData holds the data for a single shard
type shardData struct {
	id      int
	streams []writeableStreamAssignment
	hash    hash.Hash
	size    int64
}

// metaSnapshotSharded creates a sharded snapshot of the metadata
func (js *jetStream) metaSnapshotSharded() ([]byte, error) {
	start := time.Now()
	js.mu.RLock()
	s := js.srv
	cc := js.cluster
	
	// Count total streams and consumers
	totalStreams := 0
	totalConsumers := 0
	for _, asa := range cc.streams {
		totalStreams += len(asa)
		for _, sa := range asa {
			totalConsumers += len(sa.consumers)
		}
	}
	
	// Determine number of shards based on size
	numShards := calculateOptimalShards(totalStreams)
	
	// Create shard writer
	sw := &shardedSnapshotWriter{
		numShards: numShards,
		shards:    make(map[int]*shardData, numShards),
	}
	
	// Initialize shards
	for i := 0; i < numShards; i++ {
		sw.shards[i] = &shardData{
			id:      i,
			streams: make([]writeableStreamAssignment, 0),
			hash:    sha256.New(),
		}
	}
	
	// Distribute streams across shards
	for accName, asa := range cc.streams {
		for streamName, sa := range asa {
			// Calculate shard based on account and stream name
			shardID := calculateShardID(accName, streamName, numShards)
			shard := sw.shards[shardID]
			
			// Create writeable stream assignment
			wsa := writeableStreamAssignment{
				Client:    sa.Client.forAssignmentSnap(),
				Created:   sa.Created,
				Config:    sa.Config,
				Group:     sa.Group,
				Sync:      sa.Sync,
				Consumers: make([]*consumerAssignment, 0, len(sa.consumers)),
			}
			
			// Add consumers
			for _, ca := range sa.consumers {
				if ca.pending {
					continue
				}
				cca := *ca
				cca.Stream = wsa.Config.Name
				cca.Client = cca.Client.forAssignmentSnap()
				cca.Subject, cca.Reply = _EMPTY_, _EMPTY_
				wsa.Consumers = append(wsa.Consumers, &cca)
			}
			
			shard.streams = append(shard.streams, wsa)
		}
	}
	
	js.mu.RUnlock()
	
	// Process shards in parallel
	var wg sync.WaitGroup
	errors := make(chan error, numShards)
	semaphore := make(chan struct{}, maxConcurrentShards)
	
	for _, shard := range sw.shards {
		wg.Add(1)
		go func(sd *shardData) {
			defer wg.Done()
			semaphore <- struct{}{}
			defer func() { <-semaphore }()
			
			if err := sw.processShard(sd); err != nil {
				errors <- fmt.Errorf("shard %d: %w", sd.id, err)
			}
		}(shard)
	}
	
	wg.Wait()
	close(errors)
	
	// Check for errors
	for err := range errors {
		return nil, err
	}
	
	// Create final snapshot
	snapshot, err := sw.createSnapshot()
	if err != nil {
		return nil, err
	}
	
	if took := time.Since(start); took > time.Second {
		s.rateLimitFormatWarnf("Sharded metalayer snapshot took %.3fs (streams: %d, consumers: %d, shards: %d)",
			took.Seconds(), totalStreams, totalConsumers, numShards)
	}
	
	return snapshot, nil
}

// processShard processes a single shard
func (sw *shardedSnapshotWriter) processShard(shard *shardData) error {
	if len(shard.streams) == 0 {
		return nil
	}
	
	// Marshal shard data
	data, err := json.Marshal(shard.streams)
	if err != nil {
		return err
	}
	
	// Compress data
	compressed := s2.Encode(nil, data)
	
	// Update shard metadata
	shard.size = int64(len(compressed))
	shard.hash.Write(compressed)
	
	return nil
}

// createSnapshot assembles the final sharded snapshot
func (sw *shardedSnapshotWriter) createSnapshot() ([]byte, error) {
	sw.mu.Lock()
	defer sw.mu.Unlock()
	
	// Create header
	header := shardedSnapshotHeader{
		Magic:      shardedSnapshotMagic,
		Version:    1,
		NumShards:  sw.numShards,
		Created:    time.Now(),
		ShardIndex: make(map[string]shardInfo),
	}
	
	// Build shard index
	var totalSize int64
	for id, shard := range sw.shards {
		if len(shard.streams) == 0 {
			continue
		}
		
		shardKey := fmt.Sprintf("shard_%d", id)
		header.ShardIndex[shardKey] = shardInfo{
			ID:         id,
			Size:       shard.size,
			Checksum:   fmt.Sprintf("%x", shard.hash.Sum(nil)),
			NumStreams: len(shard.streams),
		}
		totalSize += shard.size
	}
	header.TotalSize = totalSize
	
	// Create snapshot buffer
	var buf bytes.Buffer
	
	// Write header
	headerData, err := json.Marshal(header)
	if err != nil {
		return nil, err
	}
	
	// Write header length (4 bytes)
	if err := binary.Write(&buf, binary.LittleEndian, uint32(len(headerData))); err != nil {
		return nil, err
	}
	
	// Write header
	if _, err := buf.Write(headerData); err != nil {
		return nil, err
	}
	
	// Write each shard
	for i := 0; i < sw.numShards; i++ {
		shard := sw.shards[i]
		if len(shard.streams) == 0 {
			continue
		}
		
		// Marshal and compress shard data
		data, err := json.Marshal(shard.streams)
		if err != nil {
			return nil, err
		}
		compressed := s2.Encode(nil, data)
		
		// Write shard length
		if err := binary.Write(&buf, binary.LittleEndian, uint32(len(compressed))); err != nil {
			return nil, err
		}
		
		// Write shard data
		if _, err := buf.Write(compressed); err != nil {
			return nil, err
		}
	}
	
	return buf.Bytes(), nil
}

// calculateOptimalShards determines the optimal number of shards based on stream count
func calculateOptimalShards(streamCount int) int {
	switch {
	case streamCount < 1000:
		return 4
	case streamCount < 10000:
		return 8
	case streamCount < 100000:
		return 16
	case streamCount < 1000000:
		return 32
	default:
		return 64
	}
}

// calculateShardID determines which shard a stream belongs to
func calculateShardID(account, stream string, numShards int) int {
	h := sha256.Sum256([]byte(account + ":" + stream))
	return int(binary.LittleEndian.Uint32(h[:4])) % numShards
}

// applyMetaSnapshotSharded applies a sharded snapshot
func (js *jetStream) applyMetaSnapshotSharded(data []byte, ru *recoveryUpdates, isRecovering bool) error {
	if len(data) < 4 {
		return fmt.Errorf("snapshot too small")
	}
	
	// Read header length
	headerLen := binary.LittleEndian.Uint32(data[:4])
	if int(headerLen+4) > len(data) {
		return fmt.Errorf("invalid header length")
	}
	
	// Parse header
	var header shardedSnapshotHeader
	if err := json.Unmarshal(data[4:4+headerLen], &header); err != nil {
		return fmt.Errorf("failed to parse header: %w", err)
	}
	
	// Validate magic
	if header.Magic != shardedSnapshotMagic {
		return fmt.Errorf("invalid snapshot magic")
	}
	
	// First, collect all streams from all shards
	allStreams := make([]writeableStreamAssignment, 0)
	
	// Process shards
	offset := int(4 + headerLen)
	processedShards := 0
	
	// Sort shard keys for deterministic processing
	shardKeys := make([]string, 0, len(header.ShardIndex))
	for k := range header.ShardIndex {
		shardKeys = append(shardKeys, k)
	}
	sort.Strings(shardKeys)
	
	for _, shardKey := range shardKeys {
		shardInfo := header.ShardIndex[shardKey]
		
		if offset+4 > len(data) {
			return fmt.Errorf("unexpected end of snapshot data")
		}
		
		// Read shard length
		shardLen := binary.LittleEndian.Uint32(data[offset : offset+4])
		offset += 4
		
		if offset+int(shardLen) > len(data) {
			return fmt.Errorf("shard %d data exceeds snapshot size", shardInfo.ID)
		}
		
		// Extract shard data
		shardData := data[offset : offset+int(shardLen)]
		offset += int(shardLen)
		
		// Verify checksum
		h := sha256.Sum256(shardData)
		if fmt.Sprintf("%x", h) != shardInfo.Checksum {
			return fmt.Errorf("shard %d checksum mismatch", shardInfo.ID)
		}
		
		// Decompress and process shard
		decompressed, err := s2.Decode(nil, shardData)
		if err != nil {
			return fmt.Errorf("failed to decompress shard %d: %w", shardInfo.ID, err)
		}
		
		// Unmarshal shard data
		var wsas []writeableStreamAssignment
		if err := json.Unmarshal(decompressed, &wsas); err != nil {
			return fmt.Errorf("failed to unmarshal shard %d: %w", shardInfo.ID, err)
		}
		
		// Collect streams from this shard
		allStreams = append(allStreams, wsas...)
		processedShards++
	}
	
	if processedShards != len(header.ShardIndex) {
		return fmt.Errorf("processed %d shards, expected %d", processedShards, len(header.ShardIndex))
	}
	
	// Now apply all streams using the same logic as the original applyMetaSnapshot
	return js.applyCollectedStreams(allStreams, ru, isRecovering)
}

// applyCollectedStreams applies all collected streams using the same logic as the original applyMetaSnapshot
func (js *jetStream) applyCollectedStreams(wsas []writeableStreamAssignment, ru *recoveryUpdates, isRecovering bool) error {
	// Build our new version here outside of js.
	streams := make(map[string]map[string]*streamAssignment)
	for _, wsa := range wsas {
		fixCfgMirrorWithDedupWindow(wsa.Config)
		as := streams[wsa.Client.serviceAccount()]
		if as == nil {
			as = make(map[string]*streamAssignment)
			streams[wsa.Client.serviceAccount()] = as
		}
		sa := &streamAssignment{Client: wsa.Client, Created: wsa.Created, Config: wsa.Config, Group: wsa.Group, Sync: wsa.Sync}
		if len(wsa.Consumers) > 0 {
			sa.consumers = make(map[string]*consumerAssignment)
			for _, ca := range wsa.Consumers {
				if ca.Stream == _EMPTY_ {
					ca.Stream = sa.Config.Name // Rehydrate from the stream name.
				}
				sa.consumers[ca.Name] = ca
			}
		}
		as[wsa.Config.Name] = sa
	}

	js.mu.Lock()
	cc := js.cluster

	var saAdd, saDel, saChk []*streamAssignment
	// Walk through the old list to generate the delete list.
	for account, asa := range cc.streams {
		nasa := streams[account]
		for sn, sa := range asa {
			if nsa := nasa[sn]; nsa == nil {
				saDel = append(saDel, sa)
			} else {
				saChk = append(saChk, nsa)
			}
		}
	}
	// Walk through the new list to generate the add list.
	for account, nasa := range streams {
		asa := cc.streams[account]
		for sn, sa := range nasa {
			if asa[sn] == nil {
				saAdd = append(saAdd, sa)
			}
		}
	}

	// Now walk the ones to check and process consumers.
	var caAdd, caDel []*consumerAssignment
	for _, sa := range saChk {
		// Make sure to add in all the new ones from sa.
		for _, ca := range sa.consumers {
			caAdd = append(caAdd, ca)
		}
		if osa := js.streamAssignment(sa.Client.serviceAccount(), sa.Config.Name); osa != nil {
			for _, ca := range osa.consumers {
				// Consumer was either removed, or recreated with a different raft group.
				if nca := sa.consumers[ca.Name]; nca == nil {
					caDel = append(caDel, ca)
				} else if nca.Group != nil && ca.Group != nil && nca.Group.Name != ca.Group.Name {
					caDel = append(caDel, ca)
				}
			}
		}
	}
	js.mu.Unlock()

	// Do removals first.
	for _, sa := range saDel {
		js.setStreamAssignmentRecovering(sa)
		if isRecovering {
			key := sa.recoveryKey()
			ru.removeStreams[key] = sa
			delete(ru.addStreams, key)
			delete(ru.updateStreams, key)
			delete(ru.updateConsumers, key)
			delete(ru.removeConsumers, key)
		} else {
			js.processStreamRemoval(sa)
		}
	}
	// Now do add for the streams. Also add in all consumers.
	for _, sa := range saAdd {
		js.setStreamAssignmentRecovering(sa)
		js.processStreamAssignment(sa)

		// We can simply process the consumers.
		for _, ca := range sa.consumers {
			js.setConsumerAssignmentRecovering(ca)
			js.processConsumerAssignment(ca)
		}
	}

	// Perform updates on those in saChk. These were existing so make
	// sure to process any changes.
	for _, sa := range saChk {
		js.setStreamAssignmentRecovering(sa)
		if isRecovering {
			key := sa.recoveryKey()
			ru.updateStreams[key] = sa
			delete(ru.addStreams, key)
			delete(ru.removeStreams, key)
		} else {
			js.processUpdateStreamAssignment(sa)
		}
	}

	// Now do the same for consumers.
	for _, ca := range caDel {
		js.setConsumerAssignmentRecovering(ca)
		if isRecovering {
			sa := js.streamAssignment(ca.Client.serviceAccount(), ca.Stream)
			key := sa.recoveryKey()
			if ru.removeConsumers[key] == nil {
				ru.removeConsumers[key] = make(map[string]*consumerAssignment)
			}
			ru.removeConsumers[key][ca.Name] = ca
		} else {
			js.processConsumerRemoval(ca)
		}
	}

	for _, ca := range caAdd {
		js.setConsumerAssignmentRecovering(ca)
		if isRecovering {
			sa := js.streamAssignment(ca.Client.serviceAccount(), ca.Stream)
			key := sa.recoveryKey()
			if ru.updateConsumers[key] == nil {
				ru.updateConsumers[key] = make(map[string]*consumerAssignment)
			}
			ru.updateConsumers[key][ca.Name] = ca
		} else {
			js.processConsumerAssignment(ca)
		}
	}

	// Update our state.
	js.mu.Lock()
	js.cluster.streams = streams
	js.mu.Unlock()

	return nil
}