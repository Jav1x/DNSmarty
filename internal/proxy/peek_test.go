package proxy

import "testing"

func TestParseClientHello(t *testing.T) {
	raw := clientHello("www.example.com")
	if _, err := ParseClientHello(raw[:10]); err != ErrNeedMore {
		t.Fatalf("short: %v", err)
	}
	name, err := ParseClientHello(raw)
	if err != nil || name != "www.example.com" {
		t.Fatalf("sni %q %v", name, err)
	}
}

func TestParseHost(t *testing.T) {
	raw := []byte("GET / HTTP/1.1\r\nHost: www.example.com:80\r\nUser-Agent: x\r\n\r\n")
	if _, err := ParseHost(raw[:12]); err != ErrNeedMore {
		t.Fatal(err)
	}
	host, err := ParseHost(raw)
	if err != nil || host != "www.example.com:80" {
		t.Fatalf("%q %v", host, err)
	}
}

func clientHello(sni string) []byte {
	name := []byte(sni)
	item := make([]byte, 3+len(name))
	item[0] = 0
	item[1] = byte(len(name) >> 8)
	item[2] = byte(len(name))
	copy(item[3:], name)
	list := make([]byte, 2+len(item))
	list[0] = byte(len(item) >> 8)
	list[1] = byte(len(item))
	copy(list[2:], item)
	ext := make([]byte, 4+len(list))
	ext[2] = byte(len(list) >> 8)
	ext[3] = byte(len(list))
	copy(ext[4:], list)

	ch := []byte{0x03, 0x03}
	ch = append(ch, make([]byte, 32)...)
	ch = append(ch, 0)
	ch = append(ch, 0, 2, 0x00, 0x2f)
	ch = append(ch, 1, 0)
	ch = append(ch, byte(len(ext)>>8), byte(len(ext)))
	ch = append(ch, ext...)

	hs := []byte{1, byte(len(ch) >> 16), byte(len(ch) >> 8), byte(len(ch))}
	hs = append(hs, ch...)
	rec := []byte{22, 0x03, 0x01, byte(len(hs) >> 8), byte(len(hs))}
	return append(rec, hs...)
}
