package auth

import (
	"strings"
	"testing"
	"time"
)

// RFC 6238 appendix B test vectors (SHA-1, truncated to 6 digits).
func TestTOTPVectors(t *testing.T) {
	secret := []byte("12345678901234567890")
	for _, v := range []struct {
		unix int64
		want string
	}{{59, "287082"}, {1111111109, "081804"}, {1111111111, "050471"}, {1234567890, "005924"}, {2000000000, "279037"}} {
		if got := TOTPCode(secret, TOTPStep(time.Unix(v.unix, 0))); got != v.want {
			t.Errorf("t=%d: %s want %s", v.unix, got, v.want)
		}
	}
}

func TestVerifyTOTPSkewAndReplay(t *testing.T) {
	secret, _ := NewTOTPSecret()
	now := time.Unix(1_800_000_000, 0)
	cur := TOTPStep(now)
	prev := TOTPCode(secret, cur-1)
	if step, ok := VerifyTOTP(secret, prev, now, 0); !ok || step != cur-1 {
		t.Fatal("previous step not accepted")
	}
	if _, ok := VerifyTOTP(secret, prev, now, cur-1); ok {
		t.Fatal("replayed code accepted")
	}
	if _, ok := VerifyTOTP(secret, TOTPCode(secret, cur-3), now, 0); ok {
		t.Fatal("stale code accepted")
	}
	if _, ok := VerifyTOTP(secret, "12345", now, 0); ok {
		t.Fatal("short code accepted")
	}
	if !strings.HasPrefix(TOTPURI(secret, "RivetPanel", "a@b.io"), "otpauth://totp/RivetPanel:a@b.io?") {
		t.Fatal(TOTPURI(secret, "RivetPanel", "a@b.io"))
	}
}

func TestRecoveryCodeFormat(t *testing.T) {
	c, err := NewRecoveryCode()
	if err != nil || len(c) != 14 || strings.Count(c, "-") != 2 {
		t.Fatalf("%q %v", c, err)
	}
	if NormalizeRecoveryCode(" "+strings.ToUpper(c)+" ") != strings.ReplaceAll(c, "-", "") {
		t.Fatal("normalize")
	}
}
