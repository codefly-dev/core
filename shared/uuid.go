package shared

import (
	"math/big"

	"github.com/google/uuid"
)

const base26Chars = "abcdefghijklmnopqrstuvwxyz"

// ShortLowerUUID returns a UUID of 10 characters in base26 (lowercase letters only)
func ShortLowerUUID() (string, error) {
	id, err := uuid.NewUUID()
	if err != nil {
		return "", err
	}

	uuidInt := big.NewInt(0)
	uuidInt.SetString(id.String(), 16)

	base26UUID := ""
	for i := 0; i < 10; i++ {
		remainder := new(big.Int)
		uuidInt.DivMod(uuidInt, big.NewInt(26), remainder)
		base26UUID = string(base26Chars[remainder.Int64()]) + base26UUID
	}

	return base26UUID, nil
}
