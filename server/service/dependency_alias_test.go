package service

import (
	"encoding/json"
	"testing"
)

func TestResolvePythonAutoInstallPackage(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		expect string
	}{
		{name: "crypto alias", input: "Crypto", expect: "pycryptodome"},
		{name: "cryptodome alias maps to pycryptodomex not pycryptodome", input: "Cryptodome", expect: "pycryptodomex"},
		{name: "execjs alias", input: "execjs", expect: "pyexecjs"},
		{name: "case insensitive", input: "crypto", expect: "pycryptodome"},
		{name: "socks alias", input: "socks", expect: "pysocks"},
		{name: "cv2 alias uppercase", input: "CV2", expect: "opencv-python"},
		{name: "bs4 alias", input: "bs4", expect: "beautifulsoup4"},
		{name: "pil alias", input: "PIL", expect: "pillow"},
		{name: "yaml alias", input: "yaml", expect: "pyyaml"},
		{name: "dateutil alias", input: "dateutil", expect: "python-dateutil"},
		{name: "jwt alias", input: "jwt", expect: "pyjwt"},
		{name: "websocket alias", input: "websocket", expect: "websocket-client"},
		{name: "attr alias", input: "attr", expect: "attrs"},
		{name: "passthrough", input: "requests", expect: "requests"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := ResolvePythonAutoInstallPackage(tc.input); got != tc.expect {
				t.Fatalf("expected %q, got %q", tc.expect, got)
			}
		})
	}
}

func TestEncodePythonAutoInstallAliases(t *testing.T) {
	var decoded map[string]string
	if err := json.Unmarshal([]byte(EncodePythonAutoInstallAliases()), &decoded); err != nil {
		t.Fatalf("decode aliases json: %v", err)
	}
	expected := map[string]string{
		"crypto": "pycryptodome",
		"execjs": "pyexecjs",
		"socks":  "pysocks",
	}
	for key, want := range expected {
		if got := decoded[key]; got != want {
			t.Fatalf("expected alias %q -> %q, got %q", key, want, got)
		}
	}
}
