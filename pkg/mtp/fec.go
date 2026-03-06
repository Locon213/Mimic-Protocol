package mtp

import (
	"encoding/binary"
	"sync"
	"time"

	"github.com/klauspost/reedsolomon"
)

const (
	FECDataShards   = 8
	FECParityShards = 2
	FECMaxPayload   = MaxPayloadSize
)

// FecEncoder collects outgoing DATA payloads and generates Parity packets
type FecEncoder struct {
	mu         sync.Mutex
	encoder    reedsolomon.Encoder
	shards     [][]byte
	dataCount  int
	startSeq   uint32
	flushTimer *time.Timer
	onParity   func(startSeq uint32, parityIndex uint8, payload []byte)
}

func NewFecEncoder(onParity func(uint32, uint8, []byte)) (*FecEncoder, error) {
	enc, err := reedsolomon.New(FECDataShards, FECParityShards)
	if err != nil {
		return nil, err
	}
	return &FecEncoder{
		encoder:  enc,
		onParity: onParity,
	}, nil
}

// AddDataPacket adds a DATA packet to the current FEC group.
// It sends parity packets immediately when the group is full.
func (f *FecEncoder) AddDataPacket(seq uint32, payload []byte) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.dataCount == 0 {
		f.startSeq = seq
		f.shards = make([][]byte, FECDataShards+FECParityShards)
		for i := range f.shards {
			f.shards[i] = make([]byte, FECMaxPayload)
		}
		// Start flush timer to prevent hanging partial groups
		f.flushTimer = time.AfterFunc(50*time.Millisecond, f.flush)
	}

	// Wait, if packets are out of order (for encoder they are strictly in order)
	// We just pack the data into the shard, prepended with its actual length because we zero-pad
	// Shard format: [Length: 2 bytes] [Payload] [Zero Padding]
	length := uint16(len(payload))
	binary.BigEndian.PutUint16(f.shards[f.dataCount][:2], length)
	copy(f.shards[f.dataCount][2:], payload)

	f.dataCount++

	if f.dataCount == FECDataShards {
		f.flushLocked()
	}
}

func (f *FecEncoder) flush() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.flushLocked()
}

func (f *FecEncoder) flushLocked() {
	if f.dataCount == 0 {
		return
	}
	if f.flushTimer != nil {
		f.flushTimer.Stop()
		f.flushTimer = nil
	}

	// If partially filled, the remaining data shards are already padded with zero arrays.
	// Encode parity
	err := f.encoder.Encode(f.shards)
	if err == nil {
		for i := 0; i < FECParityShards; i++ {
			parityShard := f.shards[FECDataShards+i]
			// We must send the parity shard. We can compress it by stripping trailing zeroes? No, RS requires fixed sizes.
			// Let's just send the whole 1200 bytes.
			f.onParity(f.startSeq, uint8(i), parityShard)
		}
	}
	f.dataCount = 0
}

// FecDecoder collects incoming DATA and FEC packets, and recovers missing DATA packets.
type FecDecoder struct {
	mu        sync.Mutex
	encoder   reedsolomon.Encoder
	groups    map[uint32]*fecGroup
	onRecover func(seq uint32, payload []byte)
}

type fecGroup struct {
	startSeq uint32
	shards   [][]byte
	received int
	timer    *time.Timer
}

func NewFecDecoder(onRecover func(uint32, []byte)) (*FecDecoder, error) {
	enc, err := reedsolomon.New(FECDataShards, FECParityShards)
	if err != nil {
		return nil, err
	}
	return &FecDecoder{
		encoder:   enc,
		groups:    make(map[uint32]*fecGroup),
		onRecover: onRecover,
	}, nil
}

// AddData records a received DATA packet to aid in recovery of others
func (f *FecDecoder) AddData(seq uint32, payload []byte) {
	f.mu.Lock()
	defer f.mu.Unlock()

	// Find which group this belongs to based on StartSeq
	// Simplified: group ID is (seq / FECDataShards) * FECDataShards
	startSeq := (seq / FECDataShards) * FECDataShards
	idx := int(seq % FECDataShards)

	g := f.getOrCreateGroup(startSeq)
	if g.shards[idx] == nil {
		g.shards[idx] = make([]byte, FECMaxPayload)
		binary.BigEndian.PutUint16(g.shards[idx][:2], uint16(len(payload)))
		copy(g.shards[idx][2:], payload)
		g.received++
		f.checkRecover(g)
	}
}

// AddParity records a received FEC packet
func (f *FecDecoder) AddParity(startSeq uint32, parityIdx uint8, payload []byte) {
	f.mu.Lock()
	defer f.mu.Unlock()

	g := f.getOrCreateGroup(startSeq)
	idx := FECDataShards + int(parityIdx)
	if idx < len(g.shards) && g.shards[idx] == nil {
		g.shards[idx] = make([]byte, len(payload))
		copy(g.shards[idx], payload)
		g.received++
		f.checkRecover(g)
	}
}

func (f *FecDecoder) getOrCreateGroup(startSeq uint32) *fecGroup {
	g, ok := f.groups[startSeq]
	if !ok {
		g = &fecGroup{
			startSeq: startSeq,
			shards:   make([][]byte, FECDataShards+FECParityShards),
		}
		g.timer = time.AfterFunc(200*time.Millisecond, func() {
			f.mu.Lock()
			delete(f.groups, startSeq)
			f.mu.Unlock()
		})
		f.groups[startSeq] = g
	}
	return g
}

func (f *FecDecoder) checkRecover(g *fecGroup) {
	// If we have enough shards to recover (need FECDataShards)
	if g.received >= FECDataShards {
		// Stop timer
		if g.timer != nil {
			g.timer.Stop()
		}

		// Check if we actually lost any data shards
		missingData := false
		for i := 0; i < FECDataShards; i++ {
			if g.shards[i] == nil {
				missingData = true
				break
			}
		}

		if missingData {
			err := f.encoder.Reconstruct(g.shards)
			if err == nil {
				// Deliver missing data shards
				for i := 0; i < FECDataShards; i++ {
					if g.shards[i] != nil { // Wait, reconstruct fills in the nils! Wait, no, we must provide properly sized buffers or allocate them. Reconstruct will allocate if nil.
						length := binary.BigEndian.Uint16(g.shards[i][:2])
						if length > 0 && length <= FECMaxPayload-2 {
							payload := make([]byte, length)
							copy(payload, g.shards[i][2:2+length])
							f.onRecover(g.startSeq+uint32(i), payload)
						}
					}
				}
			}
		}

		// Cleanup
		delete(f.groups, g.startSeq)
	}
}
