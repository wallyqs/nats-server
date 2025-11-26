//go:generate cborgen -i events.go -o jetstream_cbor_events_gen.go -s ClientInfo
//go:generate cborgen -i store.go -o jetstream_cbor_store_gen.go -s SequencePair -s Pending -s ConsumerState -s StorageType
//go:generate cborgen -i jetstream_cluster.go -o jetstream_cluster_cbor_gen.go -s raftGroup -s writeableConsumerAssignment -s writeableStreamAssignment

package server
