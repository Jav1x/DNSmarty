package proxy

import (
	"bytes"
	"errors"
	"io"
)

var ErrNeedMore = errors.New("need more")

// maxRecord is the largest TLS record payload (2^14) plus the 5-byte header.
const maxRecord = 5 + 16384

func ParseClientHello(b []byte) (string, error) {
	if len(b) < 5 {
		return "", ErrNeedMore
	}
	if b[0] != 22 {
		return "", errors.New("not a handshake")
	}
	recLen := int(b[3])<<8 | int(b[4])
	if recLen <= 0 || 5+recLen > maxRecord {
		return "", errors.New("bad record")
	}
	if len(b) < 5+recLen {
		return "", ErrNeedMore
	}
	body := b[5 : 5+recLen]
	if len(body) < 4 || body[0] != 1 {
		return "", errors.New("not a client hello")
	}
	hsLen := int(body[1])<<16 | int(body[2])<<8 | int(body[3])
	if hsLen < 0 || 4+hsLen > len(body) {
		return "", errors.New("bad handshake")
	}
	ch := body[4 : 4+hsLen]
	if len(ch) < 35 {
		return "", errors.New("short hello")
	}
	i := 34
	sidLen := int(ch[i])
	i++
	if i+sidLen > len(ch) {
		return "", errors.New("session id")
	}
	i += sidLen
	if i+2 > len(ch) {
		return "", errors.New("ciphers")
	}
	csLen := int(ch[i])<<8 | int(ch[i+1])
	i += 2
	if csLen < 2 || i+csLen > len(ch) {
		return "", errors.New("ciphers")
	}
	i += csLen
	if i >= len(ch) {
		return "", errors.New("compression")
	}
	compLen := int(ch[i])
	i++
	if compLen < 1 || i+compLen > len(ch) {
		return "", errors.New("compression")
	}
	i += compLen
	if i == len(ch) {
		return "", errors.New("no sni")
	}
	if i+2 > len(ch) {
		return "", errors.New("extensions")
	}
	extLen := int(ch[i])<<8 | int(ch[i+1])
	i += 2
	if extLen < 0 || i+extLen > len(ch) {
		return "", errors.New("extensions")
	}
	ext := ch[i : i+extLen]
	j := 0
	for j+4 <= len(ext) {
		typ := int(ext[j])<<8 | int(ext[j+1])
		l := int(ext[j+2])<<8 | int(ext[j+3])
		j += 4
		if l < 0 || j+l > len(ext) {
			return "", errors.New("extension")
		}
		if typ == 0 {
			return parseSNI(ext[j : j+l])
		}
		j += l
	}
	return "", errors.New("no sni")
}

func parseSNI(b []byte) (string, error) {
	if len(b) < 5 {
		return "", errors.New("sni")
	}
	listLen := int(b[0])<<8 | int(b[1])
	if listLen < 3 || 2+listLen > len(b) {
		return "", errors.New("sni list")
	}
	p := b[2 : 2+listLen]
	if p[0] != 0 {
		return "", errors.New("sni type")
	}
	n := int(p[1])<<8 | int(p[2])
	if n <= 0 || 3+n > len(p) {
		return "", errors.New("sni name")
	}
	return string(p[3 : 3+n]), nil
}

func ReadClientHello(r io.Reader) (sni string, raw []byte, err error) {
	buf := make([]byte, 0, 512)
	tmp := make([]byte, 1024)
	for len(buf) < maxRecord {
		n, readErr := r.Read(tmp)
		if n > 0 {
			buf = append(buf, tmp[:n]...)
		}
		name, perr := ParseClientHello(buf)
		if perr == nil {
			return name, buf, nil
		}
		if !errors.Is(perr, ErrNeedMore) {
			return "", buf, perr
		}
		if readErr != nil {
			return "", buf, readErr
		}
	}
	return "", buf, errors.New("client hello too large")
}

func ParseHost(header []byte) (string, error) {
	idx := bytes.Index(header, []byte("\r\n\r\n"))
	if idx < 0 {
		return "", ErrNeedMore
	}
	lines := bytes.Split(header[:idx], []byte("\r\n"))
	for _, line := range lines[1:] {
		k, v, ok := bytes.Cut(line, []byte(":"))
		if !ok {
			continue
		}
		if bytes.EqualFold(bytes.TrimSpace(k), []byte("host")) {
			host := string(bytes.TrimSpace(v))
			if host == "" {
				return "", errors.New("empty host")
			}
			return host, nil
		}
	}
	return "", errors.New("no host")
}

func ReadHTTPHost(r io.Reader) (host string, raw []byte, err error) {
	buf := make([]byte, 0, 512)
	tmp := make([]byte, 1024)
	for len(buf) < 16384 {
		n, readErr := r.Read(tmp)
		if n > 0 {
			buf = append(buf, tmp[:n]...)
		}
		h, perr := ParseHost(buf)
		if perr == nil {
			return h, buf, nil
		}
		if !errors.Is(perr, ErrNeedMore) {
			return "", buf, perr
		}
		if readErr != nil {
			return "", buf, readErr
		}
	}
	return "", buf, errors.New("headers too large")
}
