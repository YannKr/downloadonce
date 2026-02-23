package watermark

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
)

const (
	PayloadVersion = 0x0001
	PayloadLength  = 16
)

// BuildPayload constructs the 16-byte watermark payload per spec section 7.3:
//
//	Bytes 0–1:   Format version (0x0001)
//	Bytes 2–9:   Token ID (8 bytes, truncated SHA-256 of UUID string)
//	Bytes 10–13: Campaign ID (4 bytes, truncated SHA-256 of UUID string)
//	Bytes 14–15: CRC-16 checksum of bytes 0–13
func BuildPayload(tokenID, campaignID string) []byte {
	p := make([]byte, PayloadLength)

	// Version
	binary.BigEndian.PutUint16(p[0:2], PayloadVersion)

	// Token ID hash (8 bytes)
	th := sha256.Sum256([]byte(tokenID))
	copy(p[2:10], th[:8])

	// Campaign ID hash (4 bytes)
	ch := sha256.Sum256([]byte(campaignID))
	copy(p[10:14], ch[:4])

	// CRC-16 of bytes 0–13
	crc := crc16(p[0:14])
	binary.BigEndian.PutUint16(p[14:16], crc)

	return p
}

// PayloadHex returns the hex-encoded payload string.
func PayloadHex(tokenID, campaignID string) string {
	return hex.EncodeToString(BuildPayload(tokenID, campaignID))
}

// ParsePayload validates and extracts fields from a 16-byte payload.
// Returns the hex-encoded token ID hash (8 bytes) and campaign ID hash (4 bytes),
// plus a boolean indicating whether the CRC validated.
func ParsePayload(data []byte) (tokenIDHex string, campaignIDHex string, valid bool) {
	if len(data) != PayloadLength {
		return "", "", false
	}

	// Check version (allow a few bit errors: version should be 0x0001)
	version := binary.BigEndian.Uint16(data[0:2])
	if bitDiffU16(version, PayloadVersion) > 2 {
		return "", "", false
	}

	// Validate CRC
	expected := binary.BigEndian.Uint16(data[14:16])
	actual := crc16(data[0:14])
	if expected != actual {
		return "", "", false
	}

	tokenIDHex = hex.EncodeToString(data[2:10])
	campaignIDHex = hex.EncodeToString(data[10:14])
	return tokenIDHex, campaignIDHex, true
}

// ParsePayloadFuzzy extracts token and campaign ID hashes from a 16-byte payload
// without requiring CRC validation. Used as a fallback when CRC fails due to
// minor bit errors from JPEG re-compression. Returns the hex-encoded hashes
// and whether the version field is close enough to be plausible.
func ParsePayloadFuzzy(data []byte) (tokenIDHex string, campaignIDHex string, plausible bool) {
	if len(data) != PayloadLength {
		return "", "", false
	}

	// Check if version is close enough (within 4 bit errors)
	version := binary.BigEndian.Uint16(data[0:2])
	if bitDiffU16(version, PayloadVersion) > 4 {
		return "", "", false
	}

	tokenIDHex = hex.EncodeToString(data[2:10])
	campaignIDHex = hex.EncodeToString(data[10:14])
	return tokenIDHex, campaignIDHex, true
}

// bitDiffU16 counts the number of differing bits between two uint16 values.
func bitDiffU16(a, b uint16) int {
	diff := a ^ b
	count := 0
	for diff != 0 {
		count += int(diff & 1)
		diff >>= 1
	}
	return count
}

// crc16 computes CRC-16/CCITT-FALSE over the given data.
func crc16(data []byte) uint16 {
	var crc uint16 = 0xFFFF
	for _, b := range data {
		crc ^= uint16(b) << 8
		for i := 0; i < 8; i++ {
			if crc&0x8000 != 0 {
				crc = (crc << 1) ^ 0x1021
			} else {
				crc <<= 1
			}
		}
	}
	return crc
}
