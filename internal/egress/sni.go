package egress

import (
	"bytes"
	"encoding/binary"
	"strings"
)

// ExtractSNI parses the Server Name Indication (SNI) host from the bytes of a
// TLS ClientHello record. It returns the host and true on success, or "" and
// false if data is not a TLS ClientHello or carries no SNI extension. Only the
// leading record is inspected; data may contain trailing bytes.
//
// This is used by the transparent proxy to recover the destination hostname
// from an intercepted TLS connection whose original destination is only known
// by IP.
func ExtractSNI(data []byte) (string, bool) {
	// TLS record header: type(1) version(2) length(2). Handshake = 22.
	if len(data) < 5 || data[0] != 0x16 {
		return "", false
	}
	recLen := int(binary.BigEndian.Uint16(data[3:5]))
	buf := data[5:]
	if len(buf) > recLen {
		buf = buf[:recLen]
	}
	// Handshake header: type(1) length(3). ClientHello = 1.
	if len(buf) < 4 || buf[0] != 0x01 {
		return "", false
	}
	hs := buf[4:]
	// ClientHello: version(2) random(32) then session_id, cipher_suites,
	// compression_methods, extensions.
	if len(hs) < 34 {
		return "", false
	}
	p := hs[34:]
	// session_id
	if len(p) < 1 {
		return "", false
	}
	sidLen := int(p[0])
	p = p[1:]
	if len(p) < sidLen {
		return "", false
	}
	p = p[sidLen:]
	// cipher_suites
	if len(p) < 2 {
		return "", false
	}
	csLen := int(binary.BigEndian.Uint16(p[:2]))
	p = p[2:]
	if len(p) < csLen {
		return "", false
	}
	p = p[csLen:]
	// compression_methods
	if len(p) < 1 {
		return "", false
	}
	cmLen := int(p[0])
	p = p[1:]
	if len(p) < cmLen {
		return "", false
	}
	p = p[cmLen:]
	// extensions
	if len(p) < 2 {
		return "", false
	}
	extTotal := int(binary.BigEndian.Uint16(p[:2]))
	p = p[2:]
	if len(p) > extTotal {
		p = p[:extTotal]
	}
	for len(p) >= 4 {
		extType := binary.BigEndian.Uint16(p[:2])
		extLen := int(binary.BigEndian.Uint16(p[2:4]))
		p = p[4:]
		if len(p) < extLen {
			return "", false
		}
		body := p[:extLen]
		p = p[extLen:]
		if extType != 0x0000 { // server_name
			continue
		}
		// ServerNameList: list_length(2) then entries of type(1) len(2) name.
		if len(body) < 2 {
			return "", false
		}
		listLen := int(binary.BigEndian.Uint16(body[:2]))
		snl := body[2:]
		if len(snl) > listLen {
			snl = snl[:listLen]
		}
		for len(snl) >= 3 {
			nameType := snl[0]
			nameLen := int(binary.BigEndian.Uint16(snl[1:3]))
			snl = snl[3:]
			if len(snl) < nameLen {
				return "", false
			}
			name := snl[:nameLen]
			snl = snl[nameLen:]
			if nameType == 0x00 { // host_name
				return strings.ToLower(string(name)), true
			}
		}
	}
	return "", false
}

// httpHostFromPeek extracts the Host header value from the leading bytes of a
// plain HTTP request. It returns the host (without any trailing port) and true
// when data looks like an HTTP request carrying a Host header.
func httpHostFromPeek(data []byte) (string, bool) {
	// Only treat data as HTTP if it begins with a known method token.
	methods := []string{"GET ", "POST ", "PUT ", "HEAD ", "DELETE ", "PATCH ", "OPTIONS ", "TRACE ", "CONNECT "}
	isHTTP := false
	for _, m := range methods {
		if bytes.HasPrefix(data, []byte(m)) {
			isHTTP = true
			break
		}
	}
	if !isHTTP {
		return "", false
	}
	// Scan header lines up to the end of headers.
	for _, line := range bytes.Split(data, []byte("\r\n")) {
		if len(line) == 0 {
			break
		}
		const key = "host:"
		if len(line) >= len(key) && strings.EqualFold(string(line[:len(key)]), key) {
			host := strings.TrimSpace(string(line[len(key):]))
			if i := strings.LastIndexByte(host, ':'); i >= 0 && !strings.Contains(host[i:], "]") {
				host = host[:i]
			}
			return strings.ToLower(strings.Trim(host, "[]")), true
		}
	}
	return "", false
}
