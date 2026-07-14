package utils

import (
	"fmt"
	"math/rand/v2"
	"strings"
	"sync"
)

// lockedRand makes rand.Rand safe for concurrent client construction.
type lockedRand struct {
	mu sync.Mutex
	r  *rand.Rand
}

func (r *lockedRand) IntN(n int) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.r.IntN(n)
}

func (r *lockedRand) Uint32() uint32 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.r.Uint32()
}

var rng = &lockedRand{r: rand.New(rand.NewPCG(rand.Uint64(), rand.Uint64()))}

// randBytes fills buf with random bytes from the shared RNG.
func randBytes(buf []byte) {
	for i := range buf {
		buf[i] = byte(rng.Uint32())
	}
}

func randomChoice(lengths []int, separator string, choiceSet []string) string {
	var parts []string
	for _, length := range lengths {
		var part strings.Builder
		for i := 0; i < length; i++ {
			part.WriteString(choiceSet[rng.IntN(len(choiceSet))])
		}
		parts = append(parts, part.String())
	}
	return strings.Join(parts, separator)
}

// RandomString generates a random string of given length from the charset.
func RandomString(charset string, length int) string {
	var output strings.Builder
	for i := 0; i < length; i++ {
		output.WriteByte(charset[rng.IntN(len(charset))])
	}
	return output.String()
}

func generateRandomMAC() string {
	mac := make([]byte, 6)
	randBytes(mac)
	mac[0] = (mac[0] | 2) & 0xfe
	return fmt.Sprintf("%02x:%02x:%02x:%02x:%02x:%02x", mac[0], mac[1], mac[2], mac[3], mac[4], mac[5])
}

// GenerateRandomDRMID generates a random DRM ID of given length.
func GenerateRandomDRMID(length int) []byte {
	buf := make([]byte, length)
	randBytes(buf)
	return buf
}
