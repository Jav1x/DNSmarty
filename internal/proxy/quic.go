package proxy

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"io"
	"sort"
	"strings"
	"unicode"

	"golang.org/x/crypto/hkdf"
)

const (
	quicV1 uint32 = 0x00000001
	quicV2 uint32 = 0x6b3343cf
)

var (
	quicSaltV1 = []byte{0x38, 0x76, 0x2c, 0xf7, 0xf5, 0x59, 0x34, 0xb3, 0x4d, 0x17, 0x9a, 0xe6, 0xa4, 0xc8, 0x0c, 0xad, 0xcc, 0xbb, 0x7f, 0x0a}
	quicSaltV2 = []byte{0x0d, 0xed, 0xe3, 0xd2, 0xc5, 0xd3, 0x0d, 0xf6, 0xdd, 0x4e, 0xa4, 0x02, 0x61, 0x94, 0xc0, 0x23, 0x11, 0xdc, 0xad, 0xcb}
)

var errNotInitial = errors.New("not a quic initial")

// initialSNI decrypts a QUIC Initial (v1 or v2) and returns the TLS SNI.
// A datagram may start with a greased unknown-version packet; scan for a
// real Initial rather than requiring it at offset 0.
type cryptoFrag struct {
	off  int
	data []byte
}

func sniFromInitials(pkts [][]byte) (string, error) {
	var frags []cryptoFrag
	ietf := false
	for _, pkt := range pkts {
		f, ok := cryptoFragsFromPkt(pkt)
		if !ok {
			continue
		}
		ietf = true
		frags = append(frags, f...)
	}
	if ietf {
		if name, err := sniFromHandshake(assembleCrypto(frags)); err == nil && name != "" {
			return name, nil
		}
	}
	for _, pkt := range pkts {
		if name, err := gquicSNI(pkt); err == nil && name != "" {
			return name, nil
		}
	}
	return "", errNotInitial
}

func initialSNI(pkt []byte) (string, error) {
	return sniFromInitials([][]byte{pkt})
}

func cryptoFragsFromPkt(pkt []byte) ([]cryptoFrag, bool) {
	for off := 0; off+6 < len(pkt); off++ {
		if pkt[off]&0xc0 != 0xc0 {
			continue
		}
		ver := binary.BigEndian.Uint32(pkt[off+1 : off+5])
		if _, _, _, _, ok := initialParams(ver); !ok {
			continue
		}
		frames, err := openInitial(pkt[off:])
		if err != nil {
			continue
		}
		return collectCrypto(frames), true
	}
	return nil, false
}

func quicHead(pkt []byte) (head, ver string) {
	n := 16
	if len(pkt) < n {
		n = len(pkt)
	}
	if n > 0 {
		head = hex.EncodeToString(pkt[:n])
	}
	if len(pkt) >= 5 {
		v := pkt[1:5]
		if printableASCII(v) {
			ver = string(v)
		} else {
			ver = hex.EncodeToString(v)
		}
	}
	return head, ver
}

func printableASCII(b []byte) bool {
	for _, c := range b {
		if c < 32 || c > 126 || unicode.IsSpace(rune(c)) {
			return false
		}
	}
	return len(b) > 0
}

// gquicSNI reads the SNI tag from a Google QUIC CRYPTO CHLO (Q046 and kin).
// The inchoate client hello is sent in the clear with a FNV checksum; Xbox
// YouTube still uses this instead of IETF Initial + TLS.
func gquicSNI(pkt []byte) (string, error) {
	i := bytes.Index(pkt, []byte("CHLO"))
	if i < 0 || i+8 > len(pkt) {
		return "", errNotInitial
	}
	nent := int(binary.LittleEndian.Uint16(pkt[i+4 : i+6]))
	if nent < 1 || nent > 64 {
		return "", errNotInitial
	}
	tbl := i + 8
	if tbl+nent*8 > len(pkt) {
		return "", errNotInitial
	}
	values := tbl + nent*8
	var start uint32
	for t := 0; t < nent; t++ {
		off := tbl + t*8
		tag := pkt[off : off+4]
		end := binary.LittleEndian.Uint32(pkt[off+4 : off+8])
		if end < start || values+int(end) > len(pkt) {
			return "", errNotInitial
		}
		if tag[0] == 'S' && tag[1] == 'N' && tag[2] == 'I' {
			host := strings.TrimRight(string(pkt[values+int(start):values+int(end)]), "\x00")
			host = strings.ToLower(strings.TrimSpace(host))
			if host == "" || strings.ContainsAny(host, " \t\r\n") {
				return "", errNotInitial
			}
			return host, nil
		}
		start = end
	}
	return "", errNotInitial
}

func openInitial(pkt []byte) ([]byte, error) {
	if len(pkt) < 7 || pkt[0]&0xc0 != 0xc0 {
		return nil, errNotInitial
	}
	version := binary.BigEndian.Uint32(pkt[1:5])
	salt, keyLabel, ivLabel, hpLabel, ok := initialParams(version)
	if !ok {
		return nil, errNotInitial
	}
	if !isInitialType(pkt[0], version) {
		return nil, errNotInitial
	}
	i := 5
	if i >= len(pkt) {
		return nil, errNotInitial
	}
	dcidLen := int(pkt[i])
	i++
	if dcidLen > 20 || i+dcidLen >= len(pkt) {
		return nil, errNotInitial
	}
	dcid := pkt[i : i+dcidLen]
	i += dcidLen
	if i >= len(pkt) {
		return nil, errNotInitial
	}
	scidLen := int(pkt[i])
	i++
	if scidLen > 20 || i+scidLen > len(pkt) {
		return nil, errNotInitial
	}
	i += scidLen
	tokenLen, n, err := readVarint(pkt[i:])
	if err != nil {
		return nil, err
	}
	i += n
	if tokenLen > 4096 || i+int(tokenLen) > len(pkt) {
		return nil, errNotInitial
	}
	i += int(tokenLen)
	payLen, n, err := readVarint(pkt[i:])
	if err != nil {
		return nil, err
	}
	i += n
	pnOff := i
	end := pnOff + int(payLen)
	if payLen < 20 || end > len(pkt) {
		return nil, errNotInitial
	}
	if pnOff+4+16 > end {
		return nil, errNotInitial
	}

	secret := hkdf.Extract(sha256.New, dcid, salt)
	clientSecret := hkdfExpandLabel(secret, "client in", 32)
	key := hkdfExpandLabel(clientSecret, keyLabel, 16)
	iv := hkdfExpandLabel(clientSecret, ivLabel, 12)
	hp := hkdfExpandLabel(clientSecret, hpLabel, 16)

	block, err := aes.NewCipher(hp)
	if err != nil {
		return nil, err
	}
	var sample [16]byte
	copy(sample[:], pkt[pnOff+4:pnOff+4+16])
	var mask [16]byte
	block.Encrypt(mask[:], sample[:])

	first := pkt[0] ^ (mask[0] & 0x0f)
	pnLen := int(first&0x03) + 1
	if pnOff+pnLen > end {
		return nil, errNotInitial
	}
	pnBytes := make([]byte, pnLen)
	copy(pnBytes, pkt[pnOff:pnOff+pnLen])
	for j := 0; j < pnLen; j++ {
		pnBytes[j] ^= mask[1+j]
	}
	var pn uint64
	for _, b := range pnBytes {
		pn = pn<<8 | uint64(b)
	}

	hdr := make([]byte, pnOff+pnLen)
	copy(hdr, pkt[:pnOff])
	hdr[0] = first
	copy(hdr[pnOff:], pnBytes)

	aeadBlock, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(aeadBlock)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, 12)
	copy(nonce, iv)
	for j := 0; j < 8; j++ {
		nonce[11-j] ^= byte(pn >> (8 * j))
	}
	plain, err := aead.Open(nil, nonce, pkt[pnOff+pnLen:end], hdr)
	if err != nil {
		return nil, errNotInitial
	}
	return plain, nil
}

func initialParams(version uint32) (salt []byte, key, iv, hp string, ok bool) {
	switch version {
	case quicV1:
		return quicSaltV1, "quic key", "quic iv", "quic hp", true
	case quicV2:
		return quicSaltV2, "quicv2 key", "quicv2 iv", "quicv2 hp", true
	default:
		return nil, "", "", "", false
	}
}

func isInitialType(first byte, version uint32) bool {
	typ := (first >> 4) & 0x03
	switch version {
	case quicV1:
		return typ == 0
	case quicV2:
		return typ == 1
	default:
		return false
	}
}

func hkdfExpandLabel(secret []byte, label string, length int) []byte {
	full := "tls13 " + label
	info := make([]byte, 0, 4+len(full))
	info = append(info, byte(length>>8), byte(length))
	info = append(info, byte(len(full)))
	info = append(info, full...)
	info = append(info, 0)
	out := make([]byte, length)
	_, _ = io.ReadFull(hkdf.Expand(sha256.New, secret, info), out)
	return out
}

func readVarint(b []byte) (uint64, int, error) {
	if len(b) == 0 {
		return 0, 0, errNotInitial
	}
	n := 1 << (b[0] >> 6)
	if len(b) < n {
		return 0, 0, errNotInitial
	}
	var v uint64
	for i := 0; i < n; i++ {
		v = v<<8 | uint64(b[i])
	}
	v &= (1 << (8*n - 2)) - 1
	return v, n, nil
}

func cryptoStream(frames []byte) ([]byte, error) {
	buf := assembleCrypto(collectCrypto(frames))
	if len(buf) == 0 {
		return nil, errNotInitial
	}
	return buf, nil
}

func collectCrypto(frames []byte) []cryptoFrag {
	var out []cryptoFrag
	i := 0
	for i < len(frames) {
		t := frames[i]
		i++
		switch t {
		case 0x00, 0x01:
			continue
		case 0x06:
			off, n, err := readVarint(frames[i:])
			if err != nil {
				return out
			}
			i += n
			ln, n, err := readVarint(frames[i:])
			if err != nil {
				return out
			}
			i += n
			if ln > 16384 || i+int(ln) > len(frames) {
				return out
			}
			out = append(out, cryptoFrag{off: int(off), data: frames[i : i+int(ln)]})
			i += int(ln)
		case 0x02, 0x03:
			if err := skipACK(frames, &i, t == 0x03); err != nil {
				return out
			}
		default:
			return out
		}
	}
	return out
}

func assembleCrypto(frags []cryptoFrag) []byte {
	if len(frags) == 0 {
		return nil
	}
	sort.Slice(frags, func(i, j int) bool { return frags[i].off < frags[j].off })
	var out []byte
	pos := 0
	for _, f := range frags {
		if f.off > pos {
			break
		}
		if f.off+len(f.data) <= pos {
			continue
		}
		data := f.data
		if f.off < pos {
			data = data[pos-f.off:]
		}
		out = append(out, data...)
		pos += len(data)
	}
	return out
}

func skipACK(b []byte, i *int, ecn bool) error {
	var rangeCount uint64
	for k := 0; k < 4; k++ {
		v, n, err := readVarint(b[*i:])
		if err != nil {
			return err
		}
		*i += n
		if k == 2 {
			rangeCount = v
		}
	}
	for r := uint64(0); r < rangeCount; r++ {
		for k := 0; k < 2; k++ {
			_, n, err := readVarint(b[*i:])
			if err != nil {
				return err
			}
			*i += n
		}
	}
	if ecn {
		for k := 0; k < 3; k++ {
			_, n, err := readVarint(b[*i:])
			if err != nil {
				return err
			}
			*i += n
		}
	}
	return nil
}
