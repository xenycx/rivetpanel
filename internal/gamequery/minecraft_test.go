package gamequery

import (
	"bufio"
	"bytes"
	"context"
	"net"
	"testing"
	"time"
)

// fakeServer answers one Server List Ping with body.
func fakeServer(t *testing.T, body string) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skip("no loopback listener:", err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		r := bufio.NewReader(c)
		for i := 0; i < 2; i++ { // handshake, status request
			n, err := readVarInt(r)
			if err != nil {
				return
			}
			buf := make([]byte, n)
			if _, err := r.Read(buf); err != nil {
				return
			}
		}
		var p bytes.Buffer
		writeVarInt(&p, 0)
		writeString(&p, body)
		writePacket(c, p.Bytes())
	}()
	return ln.Addr().String()
}

func TestMinecraftJavaPing(t *testing.T) {
	addr := fakeServer(t, `{"version":{"name":"Paper 1.21.11","protocol":774},"players":{"max":20,"online":2,"sample":[{"name":"Alex","id":"x"},{"name":"Steve","id":"y"}]},"description":{"text":"§aHello ","extra":[{"text":"world"}]}}`)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	st, err := MinecraftJava(ctx, addr)
	if err != nil {
		t.Fatal(err)
	}
	if !st.Online || st.Players != 2 || st.Max != 20 || st.Version != "Paper 1.21.11" || st.MOTD != "Hello world" || len(st.Sample) != 2 {
		t.Fatalf("%+v", st)
	}
}

func TestMinecraftJavaRejectsGarbage(t *testing.T) {
	addr := fakeServer(t, `not json`)
	if _, err := MinecraftJava(context.Background(), addr); err == nil {
		t.Fatal("garbage accepted")
	}
}
