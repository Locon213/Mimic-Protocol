package mimic

import (
	"math/rand"
	"time"

	"github.com/Locon213/Mimic-Protocol/pkg/presets"
)

// Generator handles the logic for mimicking traffic patterns
type Generator struct {
	currentPreset *presets.Preset
	rnd           *rand.Rand
}

// NewGenerator creates a new traffic mimicry generator
func NewGenerator(preset *presets.Preset) *Generator {
	return &Generator{
		currentPreset: preset,
		rnd:           rand.New(rand.NewSource(time.Now().UnixNano())),
	}
}

// NextPacketDelay calculates the delay before sending the next packet
// to simulate realistic traffic flow (e.g., reading time, video buffering)
func (g *Generator) NextPacketDelay() time.Duration {
	// Basic implementation: use PacketsPerSecond to determine interval
	// In a real scenario, this would be much more complex, involving bursts and idle times
	minPPS := g.currentPreset.PacketsPerSecond.Min
	maxPPS := g.currentPreset.PacketsPerSecond.Max

	if minPPS <= 0 {
		minPPS = 1
	}
	if maxPPS <= 0 {
		maxPPS = 1
	}

	pps := g.randomInt(minPPS, maxPPS)
	if pps == 0 {
		return time.Second // Avoid division by zero
	}

	return time.Second / time.Duration(pps)
}

// NextPacketSize returns a recommended size for the next packet payload
func (g *Generator) NextPacketSize() int {
	minSize := g.currentPreset.PacketSize.Min
	maxSize := g.currentPreset.PacketSize.Max
	return g.randomInt(minSize, maxSize)
}

// SetPreset updates the current behavior profile
func (g *Generator) SetPreset(p *presets.Preset) {
	g.currentPreset = p
}

func (g *Generator) randomInt(min, max int) int {
	if min >= max {
		return min
	}
	return g.rnd.Intn(max-min+1) + min
}
