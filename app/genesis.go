package app

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// ChainIDFromHome reads the chain ID from the node's genesis file.
func ChainIDFromHome(home string) (string, error) {
	bz, err := os.ReadFile(filepath.Join(home, "config", "genesis.json"))
	if err != nil {
		return "", fmt.Errorf("read genesis: %w", err)
	}
	var doc struct {
		ChainID string `json:"chain_id"`
	}
	if err := json.Unmarshal(bz, &doc); err != nil {
		return "", fmt.Errorf("parse genesis: %w", err)
	}
	if doc.ChainID == "" {
		return "", fmt.Errorf("genesis chain_id is empty")
	}
	return doc.ChainID, nil
}
