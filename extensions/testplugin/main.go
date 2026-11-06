//go:build wasip1

// Command testplugin is a deliberately misbehaving extension used by the
// runtime's sandbox tests. Each path exercises one limit.
package main

import (
	"fmt"
	"net"
	"os"
	"strings"

	"github.com/xenycx/rivetpanel/extensions/sdk"
)

var counter int

func init() {
	sdk.Handle(func(r *sdk.Request) *sdk.Response {
		switch r.Path {
		case "/echo":
			var echo map[string]any
			err := sdk.Call("echo", map[string]string{"path": r.Path}, &echo)
			counter++
			return sdk.JSON(200, map[string]any{"echo": echo, "err": errString(err), "counter": counter})
		case "/denied":
			err := sdk.Call("bots.power", map[string]string{"bot_id": "x", "action": "stop"}, nil)
			return sdk.JSON(200, map[string]any{"denied": sdk.IsDenied(err), "err": errString(err)})
		case "/loop":
			for {
				counter++
			}
		case "/memory":
			var hold [][]byte
			for {
				hold = append(hold, make([]byte, 1<<20))
				counter += len(hold)
			}
		case "/flood":
			for i := 0; i < 100000; i++ {
				fmt.Println(strings.Repeat("x", 100))
			}
			return sdk.JSON(200, "done")
		case "/file":
			_, err := os.ReadFile("/etc/passwd")
			env := os.Environ()
			_, nerr := net.Dial("tcp", "1.1.1.1:80")
			return sdk.JSON(200, map[string]any{"file_err": errString(err), "env": env, "net_err": errString(nerr), "args": os.Args})
		case "/hostcalls":
			for i := 0; i < 1000; i++ {
				_ = sdk.Call("echo", map[string]int{"i": i}, nil)
			}
			return sdk.JSON(200, "done")
		case "/big":
			return &sdk.Response{Status: 200, Text: strings.Repeat("y", 2<<20)}
		case "/panic":
			panic("boom")
		}
		return sdk.Error(404, "no such path")
	})
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func main() {}
