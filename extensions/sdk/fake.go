//go:build !wasip1

package sdk

import "encoding/json"

// HostFunc answers capability calls in tests.
type HostFunc func(name string, req json.RawMessage) (any, *HostError)

var fake HostFunc

// SetHost installs a fake host for unit tests (off wasip1 only).
func SetHost(h HostFunc) { fake = h }

func hostCall(name string, req []byte) []byte {
	if fake == nil {
		b, _ := json.Marshal(envelope{Error: &HostError{Code: "unavailable", Message: "no fake host installed"}})
		return b
	}
	v, he := fake(name, req)
	if he != nil {
		b, _ := json.Marshal(envelope{Error: he})
		return b
	}
	ok, _ := json.Marshal(v)
	b, _ := json.Marshal(envelope{OK: ok})
	return b
}

// Invoke runs the registered handler as the host would (tests only).
func Invoke(req *Request) *Response {
	in, _ := json.Marshal(req)
	var resp Response
	json.Unmarshal(dispatch(in), &resp)
	return &resp
}
