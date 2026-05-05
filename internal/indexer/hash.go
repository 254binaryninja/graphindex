package indexer

import (
	"crypto/sha256"
	"fmt"
)

func contentHash(content []byte) string {
	return fmt.Sprintf("%x", sha256.Sum256(content))
}
