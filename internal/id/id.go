package id

import (
	"encoding/hex"

	"github.com/google/uuid"
)

func New() string {
	u, err := uuid.NewV7()
	if err != nil {
		u = uuid.New()
	}
	var buf [32]byte
	hex.Encode(buf[:], u[:])
	return string(buf[:])
}
