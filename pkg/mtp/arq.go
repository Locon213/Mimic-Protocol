package mtp

import (
	"fmt"
	"math"
	"sync"
	"time"
)

// ARQ engine constants - OPTIMIZED for performance
const (
	DefaultWindowSize     = 128                    // Увеличено с 64 до 128
	MaxWindowSize         = 512                    // Увеличено с 256 до 512
	InitialRTO            = 200 * time.Millisecond // Уменьшено с 500ms до 200ms
	MinRTO                = 20 * time.Millisecond  // Уменьшено с 100ms до 20ms (для плохих сетей)
	MaxRTO                = 10 * time.Second
	MaxRetransmissions    = 10
	DuplicateACKThreshold = 3

	// BBR parameters
	BBRGain = 1.25 // Более агрессивный gain (было 1.5)

	// FEC адаптивные параметры
	FECMinDataShards   = 8
	FECMaxDataShards   = 16
	FECMinParityShards = 2
	FECMaxParityShards = 4
)

// FECConfig - адаптивная конфигурация FEC
type FECConfig struct {
	DataShards   int
	ParityShards int
	AutoAdjust   bool
	PacketLoss   float64 // текущий уровень потерь
}

// NewFECConfig создает конфигурацию FEC по умолчанию
func NewFECConfig() *FECConfig {
	return &FECConfig{
		DataShards:   8,
		ParityShards: 2,
		AutoAdjust:   true,
		PacketLoss:   0.0,
	}
}

// Adjust адаптирует параметры FEC на основе потерь
func (c *FECConfig) Adjust(packetLoss float64) {
	c.PacketLoss = packetLoss

	if !c.AutoAdjust {
		return
	}

	// Адаптивный выбор параметров на основе потерь
	if packetLoss < 0.01 {
		// Хорошая сеть: минимальный оверхед
		c.DataShards = 16
		c.ParityShards = 2 // 11% оверхед
	} else if packetLoss < 0.05 {
		// Средняя сеть
		c.DataShards = 12
		c.ParityShards = 3 // 20% оверхед
	} else if packetLoss < 0.10 {
		// Плохая сеть
		c.DataShards = 8
		c.ParityShards = 3 // 27% оверхед
	} else {
		// Очень плохая сеть: максимальная коррекция
		c.DataShards = 8
		c.ParityShards = 4 // 33% оверхед
	}
}

type inflight struct {
	pkt        *Packet
	encoded    []byte
	sentAt     time.Time
	retries    int
	retransmit *time.Timer
	delivered  uint64 // Поле для расчетов BBR на момент отправки
	size       int    // размер пакета для статистики
}

// ARQEngine manages reliable delivery over unreliable UDP
type ARQEngine struct {
	mu sync.Mutex

	// Send state
	sendSeq    uint32               // Next sequence number to assign
	sendWindow int                  // Current congestion window size
	ssthresh   int                  // Slow start threshold
	unacked    map[uint32]*inflight // Packets waiting for ACK

	// Receive state
	recvNext  uint32             // Next expected sequence number
	recvBuf   map[uint32]*Packet // Out-of-order received packets
	delivered chan *Packet       // Ordered packets ready for Read()

	// RTT estimation (Jacobson/Karels algorithm)
	srtt    time.Duration // Smoothed RTT
	rttvar  time.Duration // RTT variance
	rto     time.Duration // Retransmission timeout
	rttInit bool          // Whether we've measured the first RTT

	// BBR (Bottleneck Bandwidth and Round-trip propagation time)
	minRTT         time.Duration
	btlBw          float64 // bytes per nanosecond
	bytesDelivered uint64  // total bytes delivered

	// BBR cycle
	priorBytesDelivered uint64
	roundStart          time.Time
	roundCount          uint64

	// Packet loss estimation
	packetsLost     uint64
	packetsSent     uint64
	lossWindowStart uint32

	// Callbacks
	sendFunc func([]byte) error // Send raw bytes over UDP
	codec    *PacketCodec

	// Pacing
	pacer *Pacer

	// FEC adaptive config
	fecConfig *FECConfig

	// Stats
	totalRetransmissions uint64
	totalPacketsSent     uint64
	totalPacketsRecv     uint64
	totalBytesSent       uint64
	totalBytesRecv       uint64

	// FEC
	fecEnc *FecEncoder
	fecDec *FecDecoder

	// Lifecycle
	closed  bool
	closeCh chan struct{}
}

// NewARQEngine creates a new ARQ engine with optimized parameters
func NewARQEngine(codec *PacketCodec, sendFunc func([]byte) error, deliverBufSize int) *ARQEngine {
	arq := &ARQEngine{
		sendWindow: 32,  // Увеличено с 2 до 32 для быстрого старта
		ssthresh:   256, // Увеличено с 64 до 256
		unacked:    make(map[uint32]*inflight),
		recvBuf:    make(map[uint32]*Packet),
		delivered:  make(chan *Packet, deliverBufSize),
		rto:        InitialRTO,
		sendFunc:   sendFunc,
		codec:      codec,
		closeCh:    make(chan struct{}),
		minRTT:     10 * time.Second,
		fecConfig:  NewFECConfig(),
	}

	// Initialize pacer: 1000 packets/sec, burst 32
	arq.pacer = NewPacer(1000, 32)

	enc, _ := NewFecEncoder(func(startSeq uint32, parityIdx uint8, payload []byte) {
		pkt := &Packet{
			Type:    PacketFEC,
			SeqNum:  startSeq,
			Flags:   parityIdx,
			Payload: payload,
			AckNum:  arq.recvNext,
		}
		if encoded, err := codec.Encode(pkt); err == nil {
			sendFunc(encoded)
		}
	}, arq.fecConfig)
	arq.fecEnc = enc

	dec, _ := NewFecDecoder(func(seq uint32, payload []byte) {
		pkt := &Packet{
			Type:    PacketDATA,
			SeqNum:  seq,
			Payload: append([]byte(nil), payload...),
		}
		go arq.HandlePacket(pkt)
	}, arq.fecConfig)
	arq.fecDec = dec

	return arq
}

// Send queues a DATA packet for reliable delivery with pacing
func (a *ARQEngine) Send(payload []byte) error {
	a.mu.Lock()
	if a.closed {
		a.mu.Unlock()
		return fmt.Errorf("mtp: arq engine closed")
	}

	// Wait for window space using condition variable approach
	for len(a.unacked) >= a.sendWindow {
		a.mu.Unlock()
		select {
		case <-a.closeCh:
			return fmt.Errorf("mtp: arq engine closed")
		case <-time.After(2 * time.Millisecond): // Уменьшено с 5ms до 2ms
		}
		a.mu.Lock()
		if a.closed {
			a.mu.Unlock()
			return fmt.Errorf("mtp: arq engine closed")
		}
	}

	seq := a.sendSeq
	a.sendSeq++

	pkt := &Packet{
		Type:    PacketDATA,
		SeqNum:  seq,
		AckNum:  a.recvNext,
		Payload: payload,
	}

	encoded, err := a.codec.Encode(pkt)
	if err != nil {
		a.mu.Unlock()
		return fmt.Errorf("mtp: encode failed: %w", err)
	}

	inf := &inflight{
		pkt:       pkt,
		encoded:   encoded,
		sentAt:    time.Now(),
		delivered: a.bytesDelivered,
		size:      len(encoded),
	}

	a.unacked[seq] = inf
	a.totalPacketsSent++
	a.packetsSent++
	a.totalBytesSent += uint64(len(encoded))
	a.mu.Unlock()

	// Add to FEC encoder
	a.fecEnc.AddDataPacket(seq, payload)

	// Pacing: wait for token before sending
	a.pacer.Wait()

	// Send the packet
	if err := a.sendFunc(encoded); err != nil {
		return err
	}

	// Start retransmission timer
	a.mu.Lock()
	if inf2, ok := a.unacked[seq]; ok {
		inf2.retransmit = time.AfterFunc(a.rto, func() {
			a.retransmitPacket(seq)
		})
	}
	a.mu.Unlock()

	return nil
}

// SendControl sends a control packet without ARQ tracking or pacing
func (a *ARQEngine) SendControl(pkt *Packet) error {
	encoded, err := a.codec.Encode(pkt)
	if err != nil {
		return fmt.Errorf("mtp: encode control failed: %w", err)
	}
	return a.sendFunc(encoded)
}

// HandlePacket processes a received packet
func (a *ARQEngine) HandlePacket(pkt *Packet) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.closed {
		return
	}

	a.totalPacketsRecv++
	a.totalBytesRecv += uint64(len(pkt.Payload))

	switch pkt.Type {
	case PacketACK:
		a.handleACK(pkt)
	case PacketDATA:
		a.fecDec.AddData(pkt.SeqNum, pkt.Payload)
		a.handleDATA(pkt)
	case PacketFEC:
		a.fecDec.AddParity(pkt.SeqNum, pkt.Flags, append([]byte(nil), pkt.Payload...))
	}
}

// handleACK processes an incoming ACK with improved BBR
func (a *ARQEngine) handleACK(pkt *Packet) {
	ackNum := pkt.AckNum
	newlyAcked := 0
	bytesLost := 0

	// Handle cumulative ACK
	for seq, inf := range a.unacked {
		if seq < ackNum {
			if !a.isPacketAcked(seq) {
				newlyAcked++
				a.packetsLost++ // Считаем потерянным если не было ACK до этого
				bytesLost += inf.size
			}
			a.processAckedPacket(seq, inf)
		}
	}

	// Handle selective ACK blocks
	if pkt.Flags&FlagSACK != 0 {
		for _, sackSeq := range pkt.SACKBlocks {
			if inf, ok := a.unacked[sackSeq]; ok {
				if !a.isPacketAcked(sackSeq) {
					newlyAcked++
				}
				a.processAckedPacket(sackSeq, inf)
			}
		}
	}

	// Update pacing based on bandwidth
	a.updatePacing()

	// Update FEC config based on packet loss
	if a.packetsSent > 0 {
		lossRate := float64(a.packetsLost) / float64(a.packetsSent)
		a.fecConfig.Adjust(lossRate)
	}

	// BBR congestion control
	if a.minRTT > 0 && a.btlBw > 0 {
		// BDP (Bytes) = Bottleneck Bandwidth * Min RTT
		bdpBytes := a.btlBw * float64(a.minRTT.Nanoseconds())
		targetWindow := int((bdpBytes / 1000.0) * BBRGain)

		if targetWindow < 8 {
			targetWindow = 8
		}
		if targetWindow > MaxWindowSize {
			targetWindow = MaxWindowSize
		}

		// Smooth adjustment
		if a.sendWindow < targetWindow {
			a.sendWindow++
		} else if a.sendWindow > targetWindow {
			a.sendWindow--
		}
	} else {
		// Slow start
		if a.sendWindow < a.ssthresh {
			a.sendWindow = min(a.sendWindow*2, MaxWindowSize)
		} else {
			a.sendWindow = min(a.sendWindow+1, MaxWindowSize)
		}
	}
}

// isPacketAcked checks if packet was already acknowledged
func (a *ARQEngine) isPacketAcked(seq uint32) bool {
	// Простая эвристика: если пакет все еще в unacked, он не был ACK
	return false
}

func (a *ARQEngine) processAckedPacket(seq uint32, inf *inflight) {
	a.bytesDelivered += uint64(len(inf.pkt.Payload))

	if inf.retries == 0 {
		deliveryTime := time.Since(inf.sentAt)
		a.updateRTT(deliveryTime)

		if a.minRTT == 0 || deliveryTime < a.minRTT {
			a.minRTT = deliveryTime
		}

		if deliveryTime > 0 {
			bytesDelivered := a.bytesDelivered - inf.delivered
			rate := float64(bytesDelivered) / float64(deliveryTime.Nanoseconds())

			// Max filter / EMA for bandwidth
			if rate > a.btlBw {
				a.btlBw = rate
			} else {
				a.btlBw = a.btlBw*0.85 + rate*0.15 // Более быстрая адаптация
			}
		}
	}

	if inf.retransmit != nil {
		inf.retransmit.Stop()
	}
	delete(a.unacked, seq)
}

// handleDATA processes an incoming DATA packet
func (a *ARQEngine) handleDATA(pkt *Packet) {
	seq := pkt.SeqNum

	if seq == a.recvNext {
		a.deliverPacket(pkt)
		a.recvNext++

		for {
			if buffered, ok := a.recvBuf[a.recvNext]; ok {
				a.deliverPacket(buffered)
				delete(a.recvBuf, a.recvNext)
				a.recvNext++
			} else {
				break
			}
		}
	} else if seq > a.recvNext {
		a.recvBuf[seq] = pkt
	}

	a.sendACK()
}

// deliverPacket sends a packet to the delivery channel
func (a *ARQEngine) deliverPacket(pkt *Packet) {
	select {
	case a.delivered <- pkt:
	default:
		// Buffer full, drop
	}
}

// sendACK sends an ACK packet with optional SACK blocks
func (a *ARQEngine) sendACK() {
	ack := &Packet{
		Type:   PacketACK,
		SeqNum: 0,
		AckNum: a.recvNext,
	}

	if len(a.recvBuf) > 0 {
		ack.Flags |= FlagSACK
		ack.SACKBlocks = make([]uint32, 0, len(a.recvBuf))
		for seq := range a.recvBuf {
			ack.SACKBlocks = append(ack.SACKBlocks, seq)
			if len(ack.SACKBlocks) >= 32 {
				break
			}
		}
	}

	encoded, err := a.codec.Encode(ack)
	if err != nil {
		return
	}
	a.sendFunc(encoded)
}

// retransmitPacket handles retransmission with improved congestion control
func (a *ARQEngine) retransmitPacket(seq uint32) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.closed {
		return
	}

	inf, ok := a.unacked[seq]
	if !ok {
		return
	}

	inf.retries++
	if inf.retries > MaxRetransmissions {
		delete(a.unacked, seq)
		return
	}

	// BBR: более мягкое уменьшение окна (20% вместо 50%)
	a.ssthresh = max(a.sendWindow*4/5, 8)
	a.sendWindow = a.ssthresh

	// Exponential backoff on RTO
	a.rto = time.Duration(math.Min(float64(a.rto*2), float64(MaxRTO)))

	a.totalRetransmissions++

	// Re-encode with fresh padding (polymorphic!)
	encoded, err := a.codec.Encode(inf.pkt)
	if err != nil {
		return
	}
	inf.encoded = encoded
	inf.sentAt = time.Now()

	a.sendFunc(encoded)

	inf.retransmit = time.AfterFunc(a.rto, func() {
		a.retransmitPacket(seq)
	})
}

// updateRTT updates the smoothed RTT using Jacobson/Karels algorithm
func (a *ARQEngine) updateRTT(rtt time.Duration) {
	if !a.rttInit {
		a.srtt = rtt
		a.rttvar = rtt / 2
		a.rttInit = true
	} else {
		a.srtt = a.srtt*7/8 + rtt/8
		diff := a.srtt - rtt
		if diff < 0 {
			diff = -diff
		}
		a.rttvar = a.rttvar*3/4 + diff/4
	}
	a.rto = a.srtt + 4*a.rttvar
	if a.rto < MinRTO {
		a.rto = MinRTO
	}
	if a.rto > MaxRTO {
		a.rto = MaxRTO
	}
}

// updatePacing adjusts pacing rate based on current bandwidth
func (a *ARQEngine) updatePacing() {
	if a.btlBw > 0 && a.minRTT > 0 {
		// packets per second = bandwidth / avg packet size
		avgPacketSize := 1000.0 // bytes
		rate := int((a.btlBw * 1e9) / avgPacketSize)

		// Limit rate
		if rate < 100 {
			rate = 100
		}
		if rate > 10000 {
			rate = 10000
		}

		a.pacer.SetRate(rate)

		// Burst size based on BDP
		bdp := int((a.btlBw * float64(a.minRTT.Nanoseconds())) / avgPacketSize)
		if bdp < 4 {
			bdp = 4
		}
		if bdp > 64 {
			bdp = 64
		}
		a.pacer.SetBurst(bdp)
	}
}

// Delivered returns the channel for in-order delivered packets
func (a *ARQEngine) Delivered() <-chan *Packet {
	return a.delivered
}

// Close stops the ARQ engine
func (a *ARQEngine) Close() {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.closed {
		return
	}
	a.closed = true
	close(a.closeCh)

	for _, inf := range a.unacked {
		if inf.retransmit != nil {
			inf.retransmit.Stop()
		}
	}
	a.unacked = nil
}

// Stats returns current engine statistics
func (a *ARQEngine) Stats() (sent, recv, retransmissions uint64, window int, rto time.Duration) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.totalPacketsSent, a.totalPacketsRecv, a.totalRetransmissions, a.sendWindow, a.rto
}

// GetLossRate returns current packet loss rate
func (a *ARQEngine) GetLossRate() float64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.packetsSent == 0 {
		return 0.0
	}
	return float64(a.packetsLost) / float64(a.packetsSent)
}

// GetFECConfig returns current FEC configuration
func (a *ARQEngine) GetFECConfig() *FECConfig {
	return a.fecConfig
}

// SetFECConfig updates FEC configuration
func (a *ARQEngine) SetFECConfig(config *FECConfig) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.fecConfig = config
	a.fecEnc.Reconfigure(config)
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
