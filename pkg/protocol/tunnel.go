package protocol

import (
	"encoding/binary"
	"fmt"
)

// TunnelRegister is sent by the CLI client to register a tunnel with the relay.
// Payload for MsgTunnelRegister.
type TunnelRegister struct {
	Slug         string // Tunnel slug (e.g., "bright-moon-7")
	LocalPort    uint16 // The local port being tunneled
	PasswordHash string // bcrypt hash of the access password (empty if none)
}

// EncodeTunnelRegister serializes a TunnelRegister into a message payload.
// Layout: [2 slug_len][slug][2 port][2 pw_len][password_hash]
func EncodeTunnelRegister(reg *TunnelRegister) []byte {
	slugBytes := []byte(reg.Slug)
	pwBytes := []byte(reg.PasswordHash)
	buf := make([]byte, 2+len(slugBytes)+2+2+len(pwBytes))
	offset := 0

	binary.BigEndian.PutUint16(buf[offset:], uint16(len(slugBytes)))
	offset += 2
	copy(buf[offset:], slugBytes)
	offset += len(slugBytes)

	binary.BigEndian.PutUint16(buf[offset:], reg.LocalPort)
	offset += 2

	binary.BigEndian.PutUint16(buf[offset:], uint16(len(pwBytes)))
	offset += 2
	copy(buf[offset:], pwBytes)

	return buf
}

// DecodeTunnelRegister deserializes a TunnelRegister from a message payload.
func DecodeTunnelRegister(data []byte) (*TunnelRegister, error) {
	if len(data) < 6 { // minimum: 2+0+2+2+0
		return nil, fmt.Errorf("tunnel register payload too short: %d bytes", len(data))
	}

	offset := 0
	slugLen := int(binary.BigEndian.Uint16(data[offset:]))
	offset += 2

	if offset+slugLen+4 > len(data) {
		return nil, fmt.Errorf("tunnel register payload truncated at slug")
	}

	reg := &TunnelRegister{}
	reg.Slug = string(data[offset : offset+slugLen])
	offset += slugLen

	reg.LocalPort = binary.BigEndian.Uint16(data[offset:])
	offset += 2

	pwLen := int(binary.BigEndian.Uint16(data[offset:]))
	offset += 2

	if offset+pwLen > len(data) {
		return nil, fmt.Errorf("tunnel register payload truncated at password")
	}

	reg.PasswordHash = string(data[offset : offset+pwLen])
	return reg, nil
}

// TunnelRegistered is sent by the relay to confirm tunnel registration.
// Payload for MsgTunnelResponse.
type TunnelRegistered struct {
	PublicURL string // The full public URL (e.g., "https://hop.to/t/bright-moon-7")
}

// EncodeTunnelRegistered serializes a TunnelRegistered.
func EncodeTunnelRegistered(reg *TunnelRegistered) []byte {
	urlBytes := []byte(reg.PublicURL)
	buf := make([]byte, 2+len(urlBytes))
	binary.BigEndian.PutUint16(buf[0:], uint16(len(urlBytes)))
	copy(buf[2:], urlBytes)
	return buf
}

// DecodeTunnelRegistered deserializes a TunnelRegistered.
func DecodeTunnelRegistered(data []byte) (*TunnelRegistered, error) {
	if len(data) < 2 {
		return nil, fmt.Errorf("tunnel registered payload too short")
	}
	urlLen := int(binary.BigEndian.Uint16(data[0:]))
	if 2+urlLen > len(data) {
		return nil, fmt.Errorf("tunnel registered payload truncated")
	}
	return &TunnelRegistered{
		PublicURL: string(data[2 : 2+urlLen]),
	}, nil
}

// TunnelHTTPRequest represents an HTTP request forwarded from the relay to the
// tunnel client. Payload for MsgTunnelRequest.
type TunnelHTTPRequest struct {
	RequestID uint32            // Unique ID for matching request/response pairs
	Method    string            // HTTP method
	Path      string            // Request path + query string
	Headers   map[string]string // Request headers (flattened, one value per key)
	Body      []byte            // Request body (may be empty)
}

// EncodeTunnelHTTPRequest serializes a TunnelHTTPRequest.
// Layout: [4 req_id][1 method_len][method][2 path_len][path][2 header_count][headers...][4 body_len][body]
// Each header: [2 key_len][key][2 val_len][val]
func EncodeTunnelHTTPRequest(req *TunnelHTTPRequest) []byte {
	methodBytes := []byte(req.Method)
	pathBytes := []byte(req.Path)

	// Calculate total size
	size := 4 + 1 + len(methodBytes) + 2 + len(pathBytes) + 2
	headerPairs := make([][2][]byte, 0, len(req.Headers))
	for k, v := range req.Headers {
		kb := []byte(k)
		vb := []byte(v)
		headerPairs = append(headerPairs, [2][]byte{kb, vb})
		size += 2 + len(kb) + 2 + len(vb)
	}
	size += 4 + len(req.Body)

	buf := make([]byte, size)
	offset := 0

	binary.BigEndian.PutUint32(buf[offset:], req.RequestID)
	offset += 4

	buf[offset] = byte(len(methodBytes))
	offset++
	copy(buf[offset:], methodBytes)
	offset += len(methodBytes)

	binary.BigEndian.PutUint16(buf[offset:], uint16(len(pathBytes)))
	offset += 2
	copy(buf[offset:], pathBytes)
	offset += len(pathBytes)

	binary.BigEndian.PutUint16(buf[offset:], uint16(len(headerPairs)))
	offset += 2
	for _, hp := range headerPairs {
		binary.BigEndian.PutUint16(buf[offset:], uint16(len(hp[0])))
		offset += 2
		copy(buf[offset:], hp[0])
		offset += len(hp[0])
		binary.BigEndian.PutUint16(buf[offset:], uint16(len(hp[1])))
		offset += 2
		copy(buf[offset:], hp[1])
		offset += len(hp[1])
	}

	binary.BigEndian.PutUint32(buf[offset:], uint32(len(req.Body)))
	offset += 4
	copy(buf[offset:], req.Body)

	return buf
}

// DecodeTunnelHTTPRequest deserializes a TunnelHTTPRequest.
func DecodeTunnelHTTPRequest(data []byte) (*TunnelHTTPRequest, error) {
	if len(data) < 9 { // 4+1+0+2+0+2
		return nil, fmt.Errorf("tunnel HTTP request too short: %d bytes", len(data))
	}

	offset := 0
	req := &TunnelHTTPRequest{}

	req.RequestID = binary.BigEndian.Uint32(data[offset:])
	offset += 4

	methodLen := int(data[offset])
	offset++
	if offset+methodLen > len(data) {
		return nil, fmt.Errorf("tunnel HTTP request truncated at method")
	}
	req.Method = string(data[offset : offset+methodLen])
	offset += methodLen

	if offset+2 > len(data) {
		return nil, fmt.Errorf("tunnel HTTP request truncated at path length")
	}
	pathLen := int(binary.BigEndian.Uint16(data[offset:]))
	offset += 2
	if offset+pathLen > len(data) {
		return nil, fmt.Errorf("tunnel HTTP request truncated at path")
	}
	req.Path = string(data[offset : offset+pathLen])
	offset += pathLen

	if offset+2 > len(data) {
		return nil, fmt.Errorf("tunnel HTTP request truncated at header count")
	}
	headerCount := int(binary.BigEndian.Uint16(data[offset:]))
	offset += 2

	req.Headers = make(map[string]string, headerCount)
	for i := 0; i < headerCount; i++ {
		if offset+2 > len(data) {
			return nil, fmt.Errorf("tunnel HTTP request truncated at header key %d", i)
		}
		keyLen := int(binary.BigEndian.Uint16(data[offset:]))
		offset += 2
		if offset+keyLen > len(data) {
			return nil, fmt.Errorf("tunnel HTTP request truncated at header key %d data", i)
		}
		key := string(data[offset : offset+keyLen])
		offset += keyLen

		if offset+2 > len(data) {
			return nil, fmt.Errorf("tunnel HTTP request truncated at header val %d", i)
		}
		valLen := int(binary.BigEndian.Uint16(data[offset:]))
		offset += 2
		if offset+valLen > len(data) {
			return nil, fmt.Errorf("tunnel HTTP request truncated at header val %d data", i)
		}
		val := string(data[offset : offset+valLen])
		offset += valLen

		req.Headers[key] = val
	}

	if offset+4 > len(data) {
		return nil, fmt.Errorf("tunnel HTTP request truncated at body length")
	}
	bodyLen := int(binary.BigEndian.Uint32(data[offset:]))
	offset += 4
	if offset+bodyLen > len(data) {
		return nil, fmt.Errorf("tunnel HTTP request truncated at body")
	}
	req.Body = data[offset : offset+bodyLen]

	return req, nil
}

// TunnelHTTPResponse represents the HTTP response sent from the tunnel client
// back to the relay. Payload for MsgTunnelResponse.
type TunnelHTTPResponse struct {
	RequestID  uint32            // Matches the TunnelHTTPRequest.RequestID
	StatusCode uint16            // HTTP status code
	Headers    map[string]string // Response headers
	Body       []byte            // Response body
}

// EncodeTunnelHTTPResponse serializes a TunnelHTTPResponse.
// Layout: [4 req_id][2 status][2 header_count][headers...][4 body_len][body]
func EncodeTunnelHTTPResponse(resp *TunnelHTTPResponse) []byte {
	// Calculate size
	size := 4 + 2 + 2
	headerPairs := make([][2][]byte, 0, len(resp.Headers))
	for k, v := range resp.Headers {
		kb := []byte(k)
		vb := []byte(v)
		headerPairs = append(headerPairs, [2][]byte{kb, vb})
		size += 2 + len(kb) + 2 + len(vb)
	}
	size += 4 + len(resp.Body)

	buf := make([]byte, size)
	offset := 0

	binary.BigEndian.PutUint32(buf[offset:], resp.RequestID)
	offset += 4

	binary.BigEndian.PutUint16(buf[offset:], resp.StatusCode)
	offset += 2

	binary.BigEndian.PutUint16(buf[offset:], uint16(len(headerPairs)))
	offset += 2
	for _, hp := range headerPairs {
		binary.BigEndian.PutUint16(buf[offset:], uint16(len(hp[0])))
		offset += 2
		copy(buf[offset:], hp[0])
		offset += len(hp[0])
		binary.BigEndian.PutUint16(buf[offset:], uint16(len(hp[1])))
		offset += 2
		copy(buf[offset:], hp[1])
		offset += len(hp[1])
	}

	binary.BigEndian.PutUint32(buf[offset:], uint32(len(resp.Body)))
	offset += 4
	copy(buf[offset:], resp.Body)

	return buf
}

// DecodeTunnelHTTPResponse deserializes a TunnelHTTPResponse.
func DecodeTunnelHTTPResponse(data []byte) (*TunnelHTTPResponse, error) {
	if len(data) < 8 { // 4+2+2
		return nil, fmt.Errorf("tunnel HTTP response too short: %d bytes", len(data))
	}

	offset := 0
	resp := &TunnelHTTPResponse{}

	resp.RequestID = binary.BigEndian.Uint32(data[offset:])
	offset += 4

	resp.StatusCode = binary.BigEndian.Uint16(data[offset:])
	offset += 2

	headerCount := int(binary.BigEndian.Uint16(data[offset:]))
	offset += 2

	resp.Headers = make(map[string]string, headerCount)
	for i := 0; i < headerCount; i++ {
		if offset+2 > len(data) {
			return nil, fmt.Errorf("tunnel HTTP response truncated at header key %d", i)
		}
		keyLen := int(binary.BigEndian.Uint16(data[offset:]))
		offset += 2
		if offset+keyLen > len(data) {
			return nil, fmt.Errorf("tunnel HTTP response truncated at header key %d data", i)
		}
		key := string(data[offset : offset+keyLen])
		offset += keyLen

		if offset+2 > len(data) {
			return nil, fmt.Errorf("tunnel HTTP response truncated at header val %d", i)
		}
		valLen := int(binary.BigEndian.Uint16(data[offset:]))
		offset += 2
		if offset+valLen > len(data) {
			return nil, fmt.Errorf("tunnel HTTP response truncated at header val %d data", i)
		}
		val := string(data[offset : offset+valLen])
		offset += valLen

		resp.Headers[key] = val
	}

	if offset+4 > len(data) {
		return nil, fmt.Errorf("tunnel HTTP response truncated at body length")
	}
	bodyLen := int(binary.BigEndian.Uint32(data[offset:]))
	offset += 4
	if offset+bodyLen > len(data) {
		return nil, fmt.Errorf("tunnel HTTP response truncated at body")
	}
	resp.Body = data[offset : offset+bodyLen]

	return resp, nil
}
