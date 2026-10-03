package novel

import (
	"encoding/base64"
	"errors"
	"strings"
	"unicode/utf8"
)

const (
	keyF1   = "dW6b7giHMbnsF9PsU9mQpupjC2PMx7rNLH9R32jBEjuJgi6VXNLNK"
	keyH1B  = "6Ri3BDgg4yaE83CKcttxMfuxuE94snhRMmdz9ttcdi9fpZ2"
	keyH4   = "ijVPbK92bQZDH3Krxuxomab9eeF8WeReDmVV6djgMXq77cYs4s6u6Vt9yuxJa7"
	keyI140 = "qww2DxQeMivZ8yjire3J6wdVpKGu37kNGQRanw"
	keyI148 = "u236Adaknew3TDp4jJdyBpcQUUF9"
)

var errInvalidContent = errors.New("novel chapter content could not be decoded")

func urlKeyPart(sourceURL string) (string, error) {
	slashes := 0
	start := -1
	for i := range sourceURL {
		if sourceURL[i] != '/' {
			continue
		}
		slashes++
		if slashes == 5 {
			start = i + 1
		}
		if slashes == 6 && start >= 0 {
			return sourceURL[start:i], nil
		}
	}
	return "", errInvalidContent
}

func decodeBase64(value []byte) ([]byte, error) {
	data := strings.TrimRight(string(value), "=")
	if data == "" {
		return nil, errInvalidContent
	}
	decoded, err := base64.RawStdEncoding.DecodeString(data)
	if err != nil {
		return nil, errInvalidContent
	}
	return decoded, nil
}

func decodeCiphertext(ciphertext []byte, key []byte) []byte {
	var state [256]byte
	for i := range state {
		state[i] = byte(i)
	}
	j := 0
	for i := range state {
		j = (j + int(state[i]) + int(key[i%len(key)])) & 0xff
		state[i], state[j] = state[j], state[i]
	}
	result := make([]byte, len(ciphertext))
	i := 0
	j = 0
	for index, value := range ciphertext {
		i = (i + 1) & 0xff
		j = (j + int(state[i])) & 0xff
		state[i], state[j] = state[j], state[i]
		keystream := state[(int(state[i])+int(state[j]))&0xff]
		if keystream == value {
			keystream = 0
		}
		result[index] = keystream ^ value
	}
	return result
}

// Decrypt decodes a novel body returned for a chapter the current account can access.
func Decrypt(sourceURL string, body []byte) ([]byte, error) {
	part, err := urlKeyPart(sourceURL)
	if err != nil {
		return nil, err
	}
	body = []byte(strings.Join(strings.Fields(string(body)), ""))
	if len(body) < 8 {
		return nil, errInvalidContent
	}
	key := string(body[:2]) + part + keyF1 + keyH1B + keyH4 + keyI140 + keyI148 + string(body[2:4])
	ciphertext, err := decodeBase64(body[4:])
	if err != nil {
		return nil, err
	}
	intermediate := decodeCiphertext(ciphertext, []byte(key))
	plaintext, err := decodeBase64(intermediate)
	if err != nil || !utf8.Valid(plaintext) || (!strings.HasPrefix(string(plaintext), "<!--V4-->") && !strings.Contains(strings.ToLower(string(plaintext[:min(len(plaintext), 512)])), "<html")) {
		return nil, errInvalidContent
	}
	return plaintext, nil
}
