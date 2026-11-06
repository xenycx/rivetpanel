package runtime

import "crypto/rand"

func cryptoRead(p []byte) (int, error) { return rand.Read(p) }
