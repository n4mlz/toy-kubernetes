package api

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
)

func LabelsMatch(selector, labels map[string]string) bool {
	for key, expected := range selector {
		if labels[key] != expected {
			return false
		}
	}
	return true
}

func GenerateName(prefix string) (string, error) {
	suffix, err := randomHex(4)
	if err != nil {
		return "", fmt.Errorf("generate name suffix: %w", err)
	}
	return prefix + suffix, nil
}

func NewUID() (string, error) {
	return randomHex(16)
}

func randomHex(size int) (string, error) {
	bytes := make([]byte, size)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes), nil
}
