package utils

import (
	"crypto/md5"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// InfocDigitMap is the hex digit set used for infoc UUID generation.
var InfocDigitMap = []string{
	"1", "2", "3", "4", "5", "6", "7", "8", "9", "A", "B", "C", "D", "E", "F", "10",
}

// GenerateXUBUVID generates a Bilibili XU-format BUVID for device identification.
//
// The XU format is: "XU" + 3 bytes selected from MD5 hash + full MD5 hash (uppercase).
// This mimics Bilibili's client-side BUVID generation algorithm.
func GenerateXUBUVID() string {
	randomBytes := GenerateRandomDRMID(16)
	hashHex := fmt.Sprintf("%x", md5.Sum(randomBytes))
	var selectedBytes string
	selectedBytes += hashHex[2:3]
	selectedBytes += hashHex[12:13]
	selectedBytes += hashHex[22:23]
	return strings.ToUpper("XU" + selectedBytes + hashHex)
}

// GetFpLocal generates a Bilibili fp_local fingerprint value.
//
// Algorithm: MD5(BUVID + model + firmwareVersion) + current timestamp +
// random hex string + verification code (sum of hex pairs mod 256).
func GetFpLocal(buvid, deviceModel, firmwareVersion string) string {
	combined := fmt.Sprintf("%s%s%s", buvid, deviceModel, firmwareVersion)
	combinedMD5 := fmt.Sprintf("%x", md5.Sum([]byte(combined)))
	fpRaw := fmt.Sprintf("%s%s%s",
		combinedMD5,
		time.Now().Format("20060102150405"),
		RandomString("0123456789abcdef", 16),
	)
	return fpRaw + calculateFpFinal(fpRaw)
}

// calculateFpFinal computes the verification code for a fingerprint raw string.
// It sums every 2-character hex chunk (mod 256) and returns a 2-digit hex string.
func calculateFpFinal(rawFingerprint string) string {
	var checksum int
	pairCount := 31
	if len(rawFingerprint) < 62 {
		pairCount = (len(rawFingerprint) - len(rawFingerprint)%2) / 2
	}
	for i := 0; i < pairCount; i++ {
		start := i * 2
		end := start + 2
		if end > len(rawFingerprint) {
			end = len(rawFingerprint)
		}
		chunk := rawFingerprint[start:end]
		if num, err := strconv.ParseInt(chunk, 16, 32); err == nil {
			checksum += int(num)
		}
	}
	checksum %= 256
	return fmt.Sprintf("%02x", checksum)
}

// GenerateUUIDInfoc generates a Bilibili-style infoc UUID for device identification.
//
// Format: "<UUID-v4-like hex>" + "<5-digit timestamp>" + "infoc"
// The UUID portion is generated using InfocDigitMap (hex-like digit set).
func GenerateUUIDInfoc() string {
	millisMod := time.Now().UnixMilli() % 100000
	return strings.Join([]string{
		randomChoice([]int{8, 4, 4, 4, 12}, "-", InfocDigitMap),
		fmt.Sprintf("%05d", millisMod),
		"infoc",
	}, "")
}

// IsTicketOnSale checks whether a ticket sale flag value indicates the ticket is available.
//
// Bilibili sale_flag values:
//
//	1 - 未开售 (not yet on sale)
//	2 - 预售中 (pre-sale)
//	3 - 已停售 (stopped)
//	4 - 已售罄 (sold out)
//	5 - 不可售 (not for sale)
func IsTicketOnSale(flag int) bool {
	switch flag {
	case 1: // 未开售
		return true
	case 2: // 预售中
		return true
	case 3: // 已停售
		return false
	case 4: // 已售罄
		return true
	case 5: // 不可售
		return false
	case 8: // 暂时售罄
		return true
	default:
		return false
	}
}
