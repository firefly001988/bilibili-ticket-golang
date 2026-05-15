package utils

import (
	"fmt"
	"math/rand"
	"strings"
	"time"
)

func randomChoice(lengths []int, separator string, choiceSet []string) string {
	var parts []string
	for _, length := range lengths {
		var part strings.Builder
		for i := 0; i < length; i++ {
			part.WriteString(choiceSet[rand.Intn(len(choiceSet))])
		}
		parts = append(parts, part.String())
	}
	return strings.Join(parts, separator)
}

// RandomString generates a random string of given length from the charset.
func RandomString(charset string, length int) string {
	var output strings.Builder
	for i := 0; i < length; i++ {
		output.WriteByte(charset[rand.Intn(len(charset))])
	}
	return output.String()
}

func generateRandomMAC() string {
	r := rand.New(rand.NewSource(time.Now().UnixNano()))
	mac := make([]byte, 6)
	r.Read(mac)
	mac[0] = (mac[0] | 2) & 0xfe
	return fmt.Sprintf("%02x:%02x:%02x:%02x:%02x:%02x", mac[0], mac[1], mac[2], mac[3], mac[4], mac[5])
}

// GenerateRandomDRMID generates a random DRM ID of given length.
func GenerateRandomDRMID(length int) []byte {
	r := rand.New(rand.NewSource(time.Now().UnixNano()))
	buf := make([]byte, length)
	r.Read(buf)
	return buf
}
