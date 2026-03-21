package mtp

import (
	"encoding/binary"
	"sync"
	"time"

	"github.com/klauspost/reedsolomon"
)

// FECMaxPayload is the maximum payload size for FEC shards
const FECMaxPayload = MaxPayloadSize

// FecEncoder collects outgoing DATA payloads and generates Parity packets
type FecEncoder struct {
	mu         sync.Mutex
	encoder    reedsolomon.Encoder
	shards     [][]byte
	dataCount  int
	startSeq   uint32
	flushTimer *time.Timer
	onParity   func(startSeq uint32, parityIndex uint8, payload []byte)
	config     *FECConfig
	configCh   chan *FECConfig
}

// NewFecEncoder creates a new FEC encoder with the given configuration
func NewFecEncoder(onParity func(uint32, uint8, []byte), config *FECConfig) (*FecEncoder, error) {
	if config == nil {
		config = NewFECConfig()
	}

	enc, err := reedsolomon.New(config.DataShards, config.ParityShards)
	if err != nil {
		return nil, err
	}

	encObj := &FecEncoder{
		encoder:  enc,
		onParity: onParity,
		config:   config,
		configCh: make(chan *FECConfig, 1),
	}

	// Goroutine для обработки изменений конфигурации
	go encObj.configLoop()

	return encObj, nil
}

// configLoop обрабатывает изменения конфигурации
func (f *FecEncoder) configLoop() {
	for newConfig := range f.configCh {
		f.mu.Lock()
		if newConfig.DataShards != f.config.DataShards || newConfig.ParityShards != f.config.ParityShards {
			newEnc, err := reedsolomon.New(newConfig.DataShards, newConfig.ParityShards)
			if err == nil {
				f.encoder = newEnc
				f.config = newConfig
				// Сбросить текущий блок, так как размер изменился
				f.dataCount = 0
				f.shards = nil
			}
		} else {
			f.config = newConfig
		}
		f.mu.Unlock()
	}
}

// Reconfigure обновляет конфигурацию FEC
func (f *FecEncoder) Reconfigure(config *FECConfig) {
	select {
	case f.configCh <- config:
	default:
		// Канал заполнен, отправляем снова (последняя конфигурация)
		<-f.configCh
		f.configCh <- config
	}
}

// AddDataPacket adds a DATA packet to the current FEC group
func (f *FecEncoder) AddDataPacket(seq uint32, payload []byte) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.dataCount == 0 {
		f.startSeq = seq
		f.shards = make([][]byte, f.config.DataShards+f.config.ParityShards)
		for i := range f.shards {
			f.shards[i] = make([]byte, FECMaxPayload)
		}
		// Start flush timer
		f.flushTimer = time.AfterFunc(50*time.Millisecond, f.flush)
	}

	// Shard format: [Length: 2 bytes] [Payload] [Zero Padding]
	length := uint16(len(payload))
	binary.BigEndian.PutUint16(f.shards[f.dataCount][:2], length)
	copy(f.shards[f.dataCount][2:], payload)

	f.dataCount++

	if f.dataCount >= f.config.DataShards {
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

	// Проверка: достаточно ли shards для текущей конфигурации
	requiredShards := f.config.DataShards + f.config.ParityShards
	if len(f.shards) < requiredShards {
		// Конфигурация изменилась, сбрасываем текущий блок
		f.dataCount = 0
		return
	}

	// Encode parity
	err := f.encoder.Encode(f.shards)
	if err == nil {
		for i := 0; i < f.config.ParityShards; i++ {
			parityShard := f.shards[f.config.DataShards+i]
			f.onParity(f.startSeq, uint8(i), parityShard)
		}
	}
	f.dataCount = 0
}

// FecDecoder collects incoming DATA and FEC packets, and recovers missing DATA packets
type FecDecoder struct {
	mu        sync.Mutex
	encoder   reedsolomon.Encoder
	groups    map[uint32]*FecGroup
	onRecover func(seq uint32, payload []byte)
	config    *FECConfig
	configCh  chan *FECConfig
}

// FecGroup represents a group of shards for Reed-Solomon decoding
type FecGroup struct {
	startSeq uint32
	shards   [][]byte
	received int
	timer    *time.Timer
}

// NewFecDecoder creates a new FEC decoder
func NewFecDecoder(onRecover func(uint32, []byte), config *FECConfig) (*FecDecoder, error) {
	if config == nil {
		config = NewFECConfig()
	}

	enc, err := reedsolomon.New(config.DataShards, config.ParityShards)
	if err != nil {
		return nil, err
	}

	dec := &FecDecoder{
		encoder:   enc,
		groups:    make(map[uint32]*FecGroup),
		onRecover: onRecover,
		config:    config,
		configCh:  make(chan *FECConfig, 1),
	}

	go dec.configLoop()

	return dec, nil
}

// configLoop обрабатывает изменения конфигурации
func (f *FecDecoder) configLoop() {
	for newConfig := range f.configCh {
		f.mu.Lock()
		if newConfig.DataShards != f.config.DataShards || newConfig.ParityShards != f.config.ParityShards {
			newEnc, err := reedsolomon.New(newConfig.DataShards, newConfig.ParityShards)
			if err == nil {
				f.encoder = newEnc
				f.config = newConfig
				f.groups = make(map[uint32]*FecGroup)
			}
		} else {
			f.config = newConfig
		}
		f.mu.Unlock()
	}
}

// Reconfigure обновляет конфигурацию FEC
func (f *FecDecoder) Reconfigure(config *FECConfig) {
	select {
	case f.configCh <- config:
	default:
		<-f.configCh
		f.configCh <- config
	}
}

// AddData records a received DATA packet
func (f *FecDecoder) AddData(seq uint32, payload []byte) {
	f.mu.Lock()
	defer f.mu.Unlock()

	startSeq := (seq / uint32(f.config.DataShards)) * uint32(f.config.DataShards)
	idx := int(seq % uint32(f.config.DataShards))

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
	idx := f.config.DataShards + int(parityIdx)
	if idx < len(g.shards) && g.shards[idx] == nil {
		g.shards[idx] = make([]byte, len(payload))
		copy(g.shards[idx], payload)
		g.received++
		f.checkRecover(g)
	}
}

func (f *FecDecoder) getOrCreateGroup(startSeq uint32) *FecGroup {
	g, ok := f.groups[startSeq]
	if !ok {
		g = &FecGroup{
			startSeq: startSeq,
			shards:   make([][]byte, f.config.DataShards+f.config.ParityShards),
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

func (f *FecDecoder) checkRecover(g *FecGroup) {
	if g.received >= f.config.DataShards {
		if g.timer != nil {
			g.timer.Stop()
		}

		missingData := false
		for i := 0; i < f.config.DataShards; i++ {
			if g.shards[i] == nil {
				missingData = true
				break
			}
		}

		if missingData {
			err := f.encoder.Reconstruct(g.shards)
			if err == nil {
				for i := 0; i < f.config.DataShards; i++ {
					if g.shards[i] != nil {
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

		delete(f.groups, g.startSeq)
	}
}
