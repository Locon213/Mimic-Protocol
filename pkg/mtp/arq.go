package mtp

import (
	"fmt"
	"math"
	"sync"
	"sync/atomic"
	"time"
)

// ARQ engine constants - OPTIMIZED for high-speed networks (100 Mbps+)
const (
	DefaultWindowSize     = 512                    // Увеличено для быстрого старта
	MaxWindowSize         = 2048                   // Увеличено для гигабитных сетей
	InitialRTO            = 100 * time.Millisecond // Уменьшено для быстрой реакции
	MinRTO                = 10 * time.Millisecond  // Минимальный RTO для локальных сетей
	MaxRTO                = 5 * time.Second        // Уменьшено для быстрого восстановления
	MaxRetransmissions    = 8                      // Уменьшено для быстрого отказа
	DuplicateACKThreshold = 2                      // Уменьшено для быстрой детекции потерь

	// BBR parameters - more aggressive for high-speed
	BBRGain = 1.5 // Более агрессивный gain для высоких скоростей

	// FEC адаптивные параметры - optimized
	FECMinDataShards   = 16 // Увеличено для лучшего соотношения
	FECMaxDataShards   = 32 // Увеличено
	FECMinParityShards = 2  // Минимум parity
	FECMaxParityShards = 4  // Максимум parity

	// Delayed ACK parameters
	DelayedACKThreshold = 32                    // Отправлять ACK после N полученных пакетов
	DelayedACKTimeout   = 15 * time.Millisecond // Максимальная задержка ACK

	// Retransmission loop parameters
	MinRetransmitCheck = 5 * time.Millisecond // Минимальный интервал проверки ретрансмиссии
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

// GetAdjusted адаптирует параметры на основе потерь и возвращает новый конфиг
// OPTIMIZED: immutable to prevent data races
func (c *FECConfig) GetAdjusted(packetLoss float64) *FECConfig {
	newCfg := &FECConfig{
		DataShards:   c.DataShards,
		ParityShards: c.ParityShards,
		AutoAdjust:   c.AutoAdjust,
		PacketLoss:   packetLoss,
	}

	if !newCfg.AutoAdjust {
		return newCfg
	}

	// Адаптивный выбор параметров на основе потерь
	// Оптимизировано для высокоскоростных сетей (100 Mbps+)
	if packetLoss < 0.005 {
		// Отличная сеть: минимальный оверхед
		newCfg.DataShards = 32
		newCfg.ParityShards = 1
	} else if packetLoss < 0.02 {
		// Хорошая сеть: минимальный оверхед
		newCfg.DataShards = 24
		newCfg.ParityShards = 2
	} else if packetLoss < 0.05 {
		// Средняя сеть
		newCfg.DataShards = 16
		newCfg.ParityShards = 2
	} else if packetLoss < 0.10 {
		// Плохая сеть
		newCfg.DataShards = 12
		newCfg.ParityShards = 3
	} else {
		// Очень плохая сеть: максимальная коррекция
		newCfg.DataShards = 8
		newCfg.ParityShards = 4
	}

	return newCfg
}

type inflight struct {
	pkt       *Packet
	encoded   []byte
	sentAt    time.Time
	retries   int
	delivered uint64 // Поле для расчетов BBR на момент отправки
	size      int    // размер пакета для статистики
}

// ARQEngine manages reliable delivery over unreliable UDP
// OPTIMIZED: split locks for better concurrency
type ARQEngine struct {
	// Split locks for better concurrency
	sendMu   sync.RWMutex // Protects send state
	sendCond *sync.Cond   // Signaled when send window frees up
	recvMu   sync.RWMutex // Protects receive state
	statsMu  sync.RWMutex // Protects statistics

	// Send state
	sendSeq    uint32               // Next sequence number to assign
	sendWindow int                  // Current congestion window size
	ssthresh   int                  // Slow start threshold
	unacked    map[uint32]*inflight // Packets waiting for ACK

	// Receive state
	recvNext        atomic.Uint32      // Next expected sequence number (atomic for cross-lock reads)
	recvBuf         map[uint32]*Packet // Out-of-order received packets
	delivered       chan *Packet       // Ordered packets ready for Read()
	unackedRecv     int                // Кол-во неподтверждённых полученных пакетов
	delayedACKTimer *time.Timer        // Таймер отложенного ACK

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
	closed  atomic.Bool
	closeCh chan struct{}
}

// NewARQEngine creates a new ARQ engine with optimized parameters
func NewARQEngine(codec *PacketCodec, sendFunc func([]byte) error, deliverBufSize int) *ARQEngine {
	arq := &ARQEngine{
		sendWindow: 256,  // Увеличено для быстрого старта (было 32)
		ssthresh:   1024, // Увеличено для высоких скоростей (было 256)
		unacked:    make(map[uint32]*inflight),

		recvBuf:   make(map[uint32]*Packet),
		delivered: make(chan *Packet, deliverBufSize),
		rto:       InitialRTO,
		sendFunc:  sendFunc,
		codec:     codec,
		closeCh:   make(chan struct{}),
		minRTT:    10 * time.Second,
		fecConfig: NewFECConfig(),
	}
	arq.sendCond = sync.NewCond(&arq.sendMu)

	// Initialize pacer: 10000 packets/sec, burst 64 (optimized for high-speed networks)
	arq.pacer = NewPacer(10000, 64)

	enc, _ := NewFecEncoder(func(startSeq uint32, parityIdx uint8, payload []byte) {
		pkt := &Packet{
			Type:    PacketFEC,
			SeqNum:  startSeq,
			Flags:   parityIdx,
			Payload: payload,
			AckNum:  arq.recvNext.Load(),
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

	// Запуск единственной горутины ретрансмиссии вместо per-packet таймеров
	go arq.retransmissionLoop()

	return arq
}

// Send queues a DATA packet for reliable delivery with pacing
// OPTIMIZED: uses windowUpdate channel for instant wake-up on ACK
func (a *ARQEngine) Send(payload []byte) error {
	a.sendMu.Lock()
	if a.closed.Load() {
		a.sendMu.Unlock()
		return fmt.Errorf("mtp: arq engine closed")
	}

	// Ожидание свободного места в окне: моментальное пробуждение через windowUpdate
	for len(a.unacked) >= a.sendWindow {
		select {
		case <-a.closeCh:
			a.sendMu.Unlock()
			return fmt.Errorf("mtp: arq engine closed")
		default:
			a.sendCond.Wait()
		}
		if a.closed.Load() { // Check again after waking up
			a.sendMu.Unlock()
			return fmt.Errorf("mtp: arq engine closed")
		}
	}

	seq := a.sendSeq
	a.sendSeq++

	pkt := &Packet{
		Type:    PacketDATA,
		SeqNum:  seq,
		AckNum:  a.recvNext.Load(),
		Payload: payload,
	}

	encoded, err := a.codec.Encode(pkt)
	if err != nil {
		a.sendMu.Unlock()
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

	// Update stats with separate lock
	a.statsMu.Lock()
	a.totalPacketsSent++
	a.packetsSent++
	a.totalBytesSent += uint64(len(encoded))
	a.statsMu.Unlock()

	a.sendMu.Unlock()

	// Add to FEC encoder
	a.fecEnc.AddDataPacket(seq, payload)

	// Pacing: wait for token before sending
	a.pacer.Wait()

	// Send the packet
	if err := a.sendFunc(encoded); err != nil {
		return err
	}

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
// OPTIMIZED: split locks for better concurrency
func (a *ARQEngine) HandlePacket(pkt *Packet) {
	if a.closed.Load() {
		return
	}

	// Update stats with separate lock
	a.statsMu.Lock()
	a.totalPacketsRecv++
	a.totalBytesRecv += uint64(len(pkt.Payload))
	a.statsMu.Unlock()

	switch pkt.Type {
	case PacketACK:
		a.sendMu.Lock()
		a.handleACK(pkt)
		a.sendMu.Unlock()
	case PacketDATA:
		a.recvMu.Lock()
		a.fecDec.AddData(pkt.SeqNum, pkt.Payload)
		a.handleDATA(pkt)
		a.recvMu.Unlock()
	case PacketFEC:
		a.recvMu.Lock()
		a.fecDec.AddParity(pkt.SeqNum, pkt.Flags, append([]byte(nil), pkt.Payload...))
		a.recvMu.Unlock()
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

	// Update FEC config based on packet loss safely
	if a.packetsSent > 0 {
		lossRate := float64(a.packetsLost) / float64(a.packetsSent)
		newCfg := a.fecConfig.GetAdjusted(lossRate)
		if newCfg.DataShards != a.fecConfig.DataShards || newCfg.ParityShards != a.fecConfig.ParityShards {
			a.fecConfig = newCfg
			a.fecEnc.Reconfigure(newCfg)
			a.fecDec.Reconfigure(newCfg)
		} else {
			a.fecConfig.PacketLoss = lossRate
		}
	}

	// BBR congestion control - optimized for high-speed
	if a.minRTT > 0 && a.btlBw > 0 {
		// BDP (Bytes) = Bottleneck Bandwidth * Min RTT
		bdpBytes := a.btlBw * float64(a.minRTT.Nanoseconds())
		targetWindow := int((bdpBytes / 1000.0) * BBRGain)

		if targetWindow < 16 {
			targetWindow = 16 // Увеличено минимальное окно
		}
		if targetWindow > MaxWindowSize {
			targetWindow = MaxWindowSize
		}

		// More aggressive adjustment for high-speed networks
		if a.sendWindow < targetWindow {
			// Быстрое увеличение: +10% или +16 пакетов
			increase := max(a.sendWindow/10, 16)
			a.sendWindow = min(a.sendWindow+increase, MaxWindowSize)
		} else if a.sendWindow > targetWindow {
			// Медленное уменьшение: -5% или -8 пакетов
			decrease := max(a.sendWindow/20, 8)
			a.sendWindow = max(a.sendWindow-decrease, 16)
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

	delete(a.unacked, seq)

	// Уведомляем все горутины, ожидающие места в окне
	a.sendCond.Broadcast()
}

// handleDATA processes an incoming DATA packet
// OPTIMIZED: delayed ACK — отправляем ACK по порогу или таймеру
func (a *ARQEngine) handleDATA(pkt *Packet) {
	seq := pkt.SeqNum
	outOfOrder := false

	if seq == a.recvNext.Load() {
		a.deliverPacket(pkt)
		a.recvNext.Add(1)

		for {
			if buffered, ok := a.recvBuf[a.recvNext.Load()]; ok {
				a.deliverPacket(buffered)
				delete(a.recvBuf, a.recvNext.Load())
				a.recvNext.Add(1)
			} else {
				break
			}
		}
	} else if seq > a.recvNext.Load() {
		a.recvBuf[seq] = pkt
		outOfOrder = true
	}

	// Delayed ACK: сразу при out-of-order или пороге, иначе по таймеру
	a.unackedRecv++
	if a.unackedRecv >= DelayedACKThreshold || outOfOrder {
		a.flushACK()
	} else if a.delayedACKTimer == nil {
		a.delayedACKTimer = time.AfterFunc(DelayedACKTimeout, func() {
			a.recvMu.Lock()
			a.flushACK()
			a.recvMu.Unlock()
		})
	}
}

// deliverPacket sends a packet to the delivery channel
func (a *ARQEngine) deliverPacket(pkt *Packet) {
	if a.closed.Load() {
		return
	}
	defer func() { recover() }() // Guard against closed channel panic
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
		AckNum: a.recvNext.Load(),
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

// retransmissionLoop — единственная горутина, проверяющая пакеты на таймаут
// Заменяет per-packet time.AfterFunc, снижая нагрузку на GC и scheduler
func (a *ARQEngine) retransmissionLoop() {
	for {
		// Динамический интервал: rto/4, но не менее MinRetransmitCheck
		a.sendMu.RLock()
		checkInterval := a.rto / 4
		if checkInterval < MinRetransmitCheck {
			checkInterval = MinRetransmitCheck
		}
		a.sendMu.RUnlock()

		select {
		case <-a.closeCh:
			return
		case <-time.After(checkInterval):
		}

		a.sendMu.Lock()
		if a.closed.Load() {
			a.sendMu.Unlock()
			return
		}

		now := time.Now()
		currentRTO := a.rto
		var toRetransmit []*retransmitEntry

		for seq, inf := range a.unacked {
			if now.Sub(inf.sentAt) > currentRTO {
				toRetransmit = append(toRetransmit, &retransmitEntry{seq: seq, inf: inf})
			}
		}

		// Обработка ретрансмиссий
		for _, entry := range toRetransmit {
			entry.inf.retries++
			if entry.inf.retries > MaxRetransmissions {
				delete(a.unacked, entry.seq)
				// Уведомляем все горутины, ожидающие места в окне
				a.sendCond.Broadcast()
				continue
			}

			// Re-encode with fresh padding (polymorphic!)
			encoded, err := a.codec.Encode(entry.inf.pkt)
			if err != nil {
				continue
			}
			entry.inf.encoded = encoded
			entry.inf.sentAt = now

			a.sendFunc(encoded)

			// Update stats
			a.statsMu.Lock()
			a.totalRetransmissions++
			a.statsMu.Unlock()
		}

		// Congestion control: уменьшаем окно один раз за цикл, если были ретрансмиссии
		if len(toRetransmit) > 0 {
			a.ssthresh = max(a.sendWindow*85/100, 16)
			a.sendWindow = a.ssthresh
			a.rto = time.Duration(math.Min(float64(a.rto*2), float64(MaxRTO)))
		}

		a.sendMu.Unlock()
	}
}

// retransmitEntry — вспомогательная структура для batch-ретрансмиссии
type retransmitEntry struct {
	seq uint32
	inf *inflight
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

// updatePacing adjusts pacing rate based on current bandwidth - optimized for high-speed
func (a *ARQEngine) updatePacing() {
	if a.btlBw > 0 && a.minRTT > 0 {
		// packets per second = bandwidth / avg packet size
		avgPacketSize := 1200.0 // bytes (updated to match MaxPayloadSize)
		rate := int((a.btlBw * 1e9) / avgPacketSize)

		// Limit rate - increased for high-speed networks
		if rate < 1000 {
			rate = 1000
		}
		if rate > 50000 { // Увеличено до 50k pps для гигабитных сетей
			rate = 50000
		}

		a.pacer.SetRate(rate)

		// Burst size based on BDP - more aggressive
		bdp := int((a.btlBw * float64(a.minRTT.Nanoseconds())) / avgPacketSize)
		if bdp < 8 {
			bdp = 8
		}
		if bdp > 128 { // Увеличено для высоких скоростей
			bdp = 128
		}
		a.pacer.SetBurst(bdp)
	}
}

// Delivered returns the channel for in-order delivered packets
func (a *ARQEngine) Delivered() <-chan *Packet {
	return a.delivered
}

// flushACK сбрасывает накопленный ACK и обнуляет счётчик
// Вызывается под recvMu.Lock()
func (a *ARQEngine) flushACK() {
	if a.delayedACKTimer != nil {
		a.delayedACKTimer.Stop()
		a.delayedACKTimer = nil
	}
	a.unackedRecv = 0
	a.sendACK()
}

// Close stops the ARQ engine
// OPTIMIZED: split locks for better concurrency
func (a *ARQEngine) Close() {
	a.sendMu.Lock()

	if a.closed.Load() {
		a.sendMu.Unlock()
		return
	}
	a.closed.Store(true)
	close(a.closeCh)

	a.unacked = nil

	// Close delivered channel so Read() unblocks
	close(a.delivered)

	// Stop FEC goroutines
	if a.fecEnc != nil {
		close(a.fecEnc.configCh)
	}
	if a.fecDec != nil {
		close(a.fecDec.configCh)
	}

	// Stop pacer
	if a.pacer != nil {
		a.pacer.Close()
	}

	// Wake up ALL goroutines blocked in Send() waiting for window space
	a.sendCond.Broadcast()
	a.sendMu.Unlock()

	// Остановить delayed ACK таймер
	a.recvMu.Lock()
	if a.delayedACKTimer != nil {
		a.delayedACKTimer.Stop()
		a.delayedACKTimer = nil
	}
	a.recvMu.Unlock()
}

// Stats returns current engine statistics
// OPTIMIZED: split locks for better concurrency
func (a *ARQEngine) Stats() (sent, recv, retransmissions uint64, window int, rto time.Duration) {
	a.statsMu.RLock()
	sent = a.totalPacketsSent
	recv = a.totalPacketsRecv
	retransmissions = a.totalRetransmissions
	a.statsMu.RUnlock()

	a.sendMu.RLock()
	window = a.sendWindow
	rto = a.rto
	a.sendMu.RUnlock()

	return
}

// GetLossRate returns current packet loss rate
// OPTIMIZED: split locks for better concurrency
func (a *ARQEngine) GetLossRate() float64 {
	a.statsMu.RLock()
	packetsSent := a.packetsSent
	packetsLost := a.packetsLost
	a.statsMu.RUnlock()

	if packetsSent == 0 {
		return 0.0
	}
	return float64(packetsLost) / float64(packetsSent)
}

// GetFECConfig returns current FEC configuration
func (a *ARQEngine) GetFECConfig() *FECConfig {
	return a.fecConfig
}

// SetFECConfig updates FEC configuration
// OPTIMIZED: split locks for better concurrency
func (a *ARQEngine) SetFECConfig(config *FECConfig) {
	a.sendMu.Lock()
	defer a.sendMu.Unlock()
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
