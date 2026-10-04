package gamequery

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"net"
	"testing"
	"time"
)

func a2sInfoPayload(name, mapName, version string, appID uint16, players, max byte) []byte {
	var b bytes.Buffer
	b.Write([]byte{0xFF, 0xFF, 0xFF, 0xFF, 0x49, 17})
	for _, s := range []string{name, mapName, "valheim", "Valheim"} {
		b.WriteString(s)
		b.WriteByte(0)
	}
	_ = binary.Write(&b, binary.LittleEndian, appID)
	b.Write([]byte{players, max, 0, 'd', 'l', 0, 0})
	b.WriteString(version)
	b.WriteByte(0)
	b.WriteByte(0x80) // EDF: port follows (ignored)
	_ = binary.Write(&b, binary.LittleEndian, uint16(2456))
	return b.Bytes()
}

// fakeA2S answers A2S_INFO on a local UDP port. With challenge set, the first
// request is answered with a challenge that must be echoed.
func fakeA2S(t *testing.T, challenge bool, reply func(req []byte) []byte) string {
	t.Helper()
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pc.Close() })
	go func() {
		buf := make([]byte, 2048)
		for {
			n, from, err := pc.ReadFrom(buf)
			if err != nil {
				return
			}
			req := append([]byte(nil), buf[:n]...)
			if !bytes.HasPrefix(req, a2sInfoReq) {
				continue
			}
			if challenge && !bytes.Equal(req[len(a2sInfoReq):], []byte{1, 2, 3, 4}) {
				_, _ = pc.WriteTo([]byte{0xFF, 0xFF, 0xFF, 0xFF, 0x41, 1, 2, 3, 4}, from)
				continue
			}
			_, _ = pc.WriteTo(reply(req), from)
		}
	}()
	return pc.LocalAddr().String()
}

func TestSteamA2SInfo(t *testing.T) {
	for _, challenge := range []bool{false, true} {
		addr := fakeA2S(t, challenge, func([]byte) []byte {
			return a2sInfoPayload("My \x01Server", "Dedicated", "0.220.5", 2457, 3, 10)
		})
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		st, err := SteamA2S(ctx, addr)
		cancel()
		if err != nil {
			t.Fatalf("challenge=%v: %v", challenge, err)
		}
		if !st.Online || st.Players != 3 || st.Max != 10 || st.MOTD != "My Server" || st.Map != "Dedicated" || st.Version != "0.220.5" {
			t.Fatalf("challenge=%v: unexpected status %+v", challenge, st)
		}
	}
}

func TestSteamA2SRefusesBadAnswers(t *testing.T) {
	cases := map[string][]byte{
		"split":     {0xFE, 0xFF, 0xFF, 0xFF, 1, 0, 0, 0, 2, 0},
		"truncated": {0xFF, 0xFF, 0xFF, 0xFF, 0x49, 17, 'a'},
		"type":      {0xFF, 0xFF, 0xFF, 0xFF, 0x6D, 0},
		"header":    {0x00, 0xFF, 0xFF, 0xFF, 0x49},
	}
	for name, pkt := range cases {
		addr := fakeA2S(t, false, func([]byte) []byte { return pkt })
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		_, err := SteamA2S(ctx, addr)
		cancel()
		if err == nil {
			t.Fatalf("%s: expected an error", name)
		}
		if name == "split" && !errors.Is(err, ErrA2SSplit) {
			t.Fatalf("split: got %v", err)
		}
	}
}

func TestSteamA2SNoAnswerTimesOut(t *testing.T) {
	pc, err := net.ListenPacket("udp", "127.0.0.1:0") // reads, never answers
	if err != nil {
		t.Fatal(err)
	}
	defer pc.Close()
	silent := pc.LocalAddr().String()
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	if _, err := SteamA2S(ctx, silent); err == nil {
		t.Fatal("expected a timeout or refusal")
	}
}
