package apptemplates

import (
	"crypto/rand"
	"fmt"
	"math/big"
)

var alphabets = map[string]string{
	// Passwords avoid characters that need quoting in shells, URLs or YAML.
	"password":  "ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz23456789-_.",
	"hex":       "0123456789abcdef",
	"alnum":     "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789",
	"base64url": "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_",
}

// Random returns a value for a generator, from crypto/rand.
func Random(g Generate) (string, error) {
	alpha, ok := alphabets[g.Kind]
	if !ok {
		return "", fmt.Errorf("unknown generator %q", g.Kind)
	}
	n := big.NewInt(int64(len(alpha)))
	out := make([]byte, g.Length)
	for i := range out {
		k, err := rand.Int(rand.Reader, n)
		if err != nil {
			return "", err
		}
		out[i] = alpha[k.Int64()]
	}
	if g.Kind == "password" {
		// Start with a letter: some apps reject a leading symbol.
		out[0] = alpha[int(out[0])%48]
	}
	return string(out), nil
}
