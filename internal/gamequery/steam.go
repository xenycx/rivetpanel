package gamequery

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"time"
)

// Steam A2S (the Source/Steam server query protocol over UDP). Only A2S_INFO
// is implemented: name, map, players and version. Split (multi-packet)
// responses are refused; A2S_INFO answers fit in a single packet.

var (
	a2sHeader  = []byte{0xFF, 0xFF, 0xFF, 0xFF}
	a2sInfoReq = append(append([]byte{}, a2sHeader...), append([]byte{'T'}, []byte("Source Engine Query\x00")...)...)
)

const (
	a2sInfo      = 0x49 // 'I'
	a2sChallenge = 0x41 // 'A'
	a2sMaxPacket = 1400 * 2
)

// ErrA2SSplit reports a multi-packet response, which this client does not
// reassemble.
var ErrA2SSplit = errors.New("a2s: split responses are not supported")

// SteamA2S asks the server at addr ("host:port", the game's query port) for
// A2S_INFO, answering one challenge if the server requires it.
func SteamA2S(ctx context.Context, addr string) (Status, error) {
	d := net.Dialer{Timeout: 3 * time.Second}
	conn, err := d.DialContext(ctx, "udp", addr)
	if err != nil {
		return Status{}, err
	}
	defer conn.Close()
	deadline := time.Now().Add(4 * time.Second)
	if dl, ok := ctx.Deadline(); ok && dl.Before(deadline) {
		deadline = dl
	}
	_ = conn.SetDeadline(deadline)

	start := time.Now()
	req := a2sInfoReq
	buf := make([]byte, a2sMaxPacket)
	for attempt := 0; attempt < 3; attempt++ {
		if _, err := conn.Write(req); err != nil {
			return Status{}, err
		}
		n, err := conn.Read(buf)
		if err != nil {
			return Status{}, err
		}
		pkt := buf[:n]
		if len(pkt) < 5 {
			return Status{}, errors.New("a2s: short response")
		}
		if bytes.Equal(pkt[:4], []byte{0xFE, 0xFF, 0xFF, 0xFF}) {
			return Status{}, ErrA2SSplit
		}
		if !bytes.Equal(pkt[:4], a2sHeader) {
			return Status{}, errors.New("a2s: unexpected packet header")
		}
		switch pkt[4] {
		case a2sChallenge:
			if len(pkt) < 9 {
				return Status{}, errors.New("a2s: short challenge")
			}
			req = append(append([]byte{}, a2sInfoReq...), pkt[5:9]...)
			continue
		case a2sInfo:
			st, err := parseA2SInfo(pkt[5:])
			if err != nil {
				return Status{}, err
			}
			st.LatencyMS = time.Since(start).Milliseconds()
			return st, nil
		default:
			return Status{}, fmt.Errorf("a2s: unexpected response type 0x%02x", pkt[4])
		}
	}
	return Status{}, errors.New("a2s: too many challenges")
}

type a2sReader struct {
	b   []byte
	err error
}

func (r *a2sReader) byte() byte {
	if r.err != nil || len(r.b) < 1 {
		r.err = errors.New("a2s: truncated response")
		return 0
	}
	v := r.b[0]
	r.b = r.b[1:]
	return v
}

func (r *a2sReader) short() uint16 {
	if r.err != nil || len(r.b) < 2 {
		r.err = errors.New("a2s: truncated response")
		return 0
	}
	v := binary.LittleEndian.Uint16(r.b)
	r.b = r.b[2:]
	return v
}

func (r *a2sReader) str() string {
	if r.err != nil {
		return ""
	}
	i := bytes.IndexByte(r.b, 0)
	if i < 0 {
		r.err = errors.New("a2s: unterminated string")
		return ""
	}
	v := r.b[:i]
	r.b = r.b[i+1:]
	return clean(string(v))
}

// parseA2SInfo decodes the A2S_INFO payload after the 0x49 type byte.
func parseA2SInfo(b []byte) (Status, error) {
	r := &a2sReader{b: b}
	r.byte() // protocol
	name := r.str()
	mapName := r.str()
	r.str() // folder
	r.str() // game
	appID := r.short()
	players := int(r.byte())
	maxPlayers := int(r.byte())
	// bots, server type, environment, visibility, VAC
	for i := 0; i < 5; i++ {
		r.byte()
	}
	if appID == 2400 { // The Ship: mode, witnesses, duration
		r.byte()
		r.byte()
		r.byte()
	}
	version := r.str()
	if r.err != nil {
		return Status{}, r.err
	}
	return Status{Online: true, Players: players, Max: maxPlayers, Version: version, MOTD: name, Map: mapName}, nil
}

func clean(s string) string {
	out := make([]rune, 0, len(s))
	for _, c := range s {
		if c == '�' || c < 0x20 || c == 0x7f {
			continue
		}
		out = append(out, c)
		if len(out) >= 200 {
			break
		}
	}
	return string(out)
}
