package adapters

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	cdpMaxTargetResponseBytes = 1 << 20
	cdpMaxMessageBytes        = 16 << 20
	webSocketGUID             = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"
)

type cdpTarget struct {
	Type                 string `json:"type"`
	WebSocketDebuggerURL string `json:"webSocketDebuggerUrl"`
}

func fetchCDPTargets(ctx context.Context, port int) ([]cdpTarget, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d/json/list", port), nil)
	if err != nil {
		return nil, err
	}
	client := &http.Client{
		Transport: &http.Transport{
			Proxy:             nil,
			DisableKeepAlives: true,
			DialContext: (&net.Dialer{
				Timeout: time.Second,
			}).DialContext,
		},
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("debug target endpoint returned HTTP %d", response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, cdpMaxTargetResponseBytes+1))
	if err != nil {
		return nil, err
	}
	if len(body) > cdpMaxTargetResponseBytes {
		return nil, fmt.Errorf("debug target response exceeded the size limit")
	}
	var targets []cdpTarget
	if err := json.Unmarshal(body, &targets); err != nil {
		return nil, fmt.Errorf("invalid debug target response")
	}
	return targets, nil
}

func waitForDebugTargets(ctx context.Context, port int) ([]cdpTarget, error) {
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		targets, err := fetchCDPTargets(ctx, port)
		if err == nil && len(targets) > 0 {
			return targets, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
		}
	}
}

func dialFirstCDPTarget(ctx context.Context, targets []cdpTarget, expectedPort int) (*cdpClient, error) {
	for _, target := range targets {
		client, err := dialCDPTarget(ctx, target, expectedPort)
		if err == nil {
			return client, nil
		}
	}
	return nil, fmt.Errorf("no compatible CDP target")
}

func dialCDPTarget(ctx context.Context, target cdpTarget, expectedPort int) (*cdpClient, error) {
	endpoint, err := validateLoopbackWebSocketURL(target.WebSocketDebuggerURL, expectedPort)
	if err != nil {
		return nil, err
	}
	socket, err := dialCDPWebSocket(ctx, endpoint, cdpMaxMessageBytes)
	if err != nil {
		return nil, err
	}
	return &cdpClient{socket: socket}, nil
}

func validateLoopbackWebSocketURL(raw string, expectedPort int) (*url.URL, error) {
	endpoint, err := url.Parse(raw)
	if err != nil || endpoint.Scheme != "ws" || endpoint.User != nil || endpoint.Fragment != "" {
		return nil, fmt.Errorf("invalid CDP WebSocket endpoint")
	}
	hostIP := net.ParseIP(endpoint.Hostname())
	if hostIP == nil || !hostIP.IsLoopback() {
		return nil, fmt.Errorf("CDP WebSocket endpoint is not loopback")
	}
	port, err := strconv.Atoi(endpoint.Port())
	if err != nil || port != expectedPort {
		return nil, fmt.Errorf("CDP WebSocket endpoint uses an unexpected port")
	}
	if endpoint.Path == "" || endpoint.Path == "/" || strings.Contains(endpoint.Path, "..") || strings.ContainsAny(endpoint.Host, "\r\n") || strings.ContainsAny(endpoint.RequestURI(), "\r\n") {
		return nil, fmt.Errorf("invalid CDP WebSocket endpoint")
	}
	return endpoint, nil
}

type cdpClient struct {
	socket *cdpWebSocket
	nextID int64
}

func (c *cdpClient) Call(ctx context.Context, method string, params any, result any) error {
	if c == nil || c.socket == nil {
		return fmt.Errorf("CDP client is closed")
	}
	c.nextID++
	requestID := c.nextID
	request := struct {
		ID     int64  `json:"id"`
		Method string `json:"method"`
		Params any    `json:"params,omitempty"`
	}{ID: requestID, Method: method, Params: params}
	payload, err := json.Marshal(request)
	if err != nil {
		return err
	}
	if err := c.socket.WriteText(ctx, payload); err != nil {
		return fmt.Errorf("CDP request write failed")
	}

	for {
		message, err := c.socket.ReadMessage(ctx)
		if err != nil {
			return fmt.Errorf("CDP response read failed")
		}
		var envelope struct {
			ID     int64           `json:"id"`
			Result json.RawMessage `json:"result"`
			Error  *struct {
				Code int `json:"code"`
			} `json:"error"`
		}
		if err := json.Unmarshal(message, &envelope); err != nil {
			return fmt.Errorf("invalid CDP response")
		}
		if envelope.ID != requestID {
			continue
		}
		if envelope.Error != nil {
			return fmt.Errorf("CDP method failed with code %d", envelope.Error.Code)
		}
		if len(envelope.Result) == 0 {
			return fmt.Errorf("CDP response has no result")
		}
		if result == nil {
			return nil
		}
		if err := json.Unmarshal(envelope.Result, result); err != nil {
			return fmt.Errorf("invalid CDP method result")
		}
		return nil
	}
}

func (c *cdpClient) Evaluate(ctx context.Context, expression string, includeCommandLineAPI bool, out any) error {
	params := map[string]any{
		"expression":            expression,
		"awaitPromise":          true,
		"returnByValue":         true,
		"includeCommandLineAPI": includeCommandLineAPI,
	}
	var evaluation struct {
		Result struct {
			Value json.RawMessage `json:"value"`
		} `json:"result"`
		ExceptionDetails json.RawMessage `json:"exceptionDetails"`
	}
	if err := c.Call(ctx, "Runtime.evaluate", params, &evaluation); err != nil {
		return err
	}
	if len(evaluation.ExceptionDetails) > 0 && string(evaluation.ExceptionDetails) != "null" {
		return fmt.Errorf("runtime evaluation failed")
	}
	if len(evaluation.Result.Value) == 0 {
		return fmt.Errorf("runtime evaluation returned no value")
	}
	if err := json.Unmarshal(evaluation.Result.Value, out); err != nil {
		return fmt.Errorf("runtime evaluation returned an unexpected value")
	}
	return nil
}

func (c *cdpClient) Close() {
	if c == nil || c.socket == nil {
		return
	}
	c.socket.Close()
	c.socket = nil
}

type cdpWebSocket struct {
	connection net.Conn
	reader     *bufio.Reader
	maxMessage int64
	writeMu    sync.Mutex
}

func dialCDPWebSocket(ctx context.Context, endpoint *url.URL, maxMessage int64) (*cdpWebSocket, error) {
	dialer := net.Dialer{Timeout: time.Second}
	connection, err := dialer.DialContext(ctx, "tcp", endpoint.Host)
	if err != nil {
		return nil, err
	}
	failed := true
	defer func() {
		if failed {
			_ = connection.Close()
		}
	}()
	if err := setConnectionDeadline(connection, ctx); err != nil {
		return nil, err
	}

	nonce := make([]byte, 16)
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	key := base64.StdEncoding.EncodeToString(nonce)
	request := &http.Request{
		Method: http.MethodGet,
		URL:    endpoint,
		Host:   endpoint.Host,
		Header: http.Header{
			"Upgrade":               []string{"websocket"},
			"Connection":            []string{"Upgrade"},
			"Sec-Websocket-Key":     []string{key},
			"Sec-Websocket-Version": []string{"13"},
		},
	}
	if err := request.Write(connection); err != nil {
		return nil, err
	}
	reader := bufio.NewReader(connection)
	response, err := http.ReadResponse(reader, request)
	if err != nil {
		return nil, err
	}
	if response.StatusCode != http.StatusSwitchingProtocols || !headerContainsToken(response.Header, "Upgrade", "websocket") || !headerContainsToken(response.Header, "Connection", "upgrade") {
		return nil, fmt.Errorf("CDP WebSocket upgrade was rejected")
	}
	expectedAccept := sha1.Sum([]byte(key + webSocketGUID))
	if response.Header.Get("Sec-WebSocket-Accept") != base64.StdEncoding.EncodeToString(expectedAccept[:]) {
		return nil, fmt.Errorf("CDP WebSocket accept key mismatch")
	}
	if err := connection.SetDeadline(time.Time{}); err != nil {
		return nil, err
	}
	failed = false
	return &cdpWebSocket{connection: connection, reader: reader, maxMessage: maxMessage}, nil
}

func headerContainsToken(header http.Header, name, token string) bool {
	for _, value := range header.Values(name) {
		for _, part := range strings.Split(value, ",") {
			if strings.EqualFold(strings.TrimSpace(part), token) {
				return true
			}
		}
	}
	return false
}

func (w *cdpWebSocket) WriteText(ctx context.Context, payload []byte) error {
	return w.writeFrame(ctx, 0x1, payload)
}

func (w *cdpWebSocket) writeFrame(ctx context.Context, opcode byte, payload []byte) error {
	if w == nil || w.connection == nil {
		return net.ErrClosed
	}
	if int64(len(payload)) > w.maxMessage {
		return fmt.Errorf("WebSocket payload exceeds the size limit")
	}
	w.writeMu.Lock()
	defer w.writeMu.Unlock()
	if err := setConnectionDeadline(w.connection, ctx); err != nil {
		return err
	}

	header := make([]byte, 0, 14)
	header = append(header, 0x80|(opcode&0x0f))
	length := len(payload)
	switch {
	case length <= 125:
		header = append(header, 0x80|byte(length))
	case uint64(length) <= uint64(^uint16(0)):
		header = append(header, 0x80|126, 0, 0)
		binary.BigEndian.PutUint16(header[len(header)-2:], uint16(length))
	default:
		header = append(header, 0x80|127, 0, 0, 0, 0, 0, 0, 0, 0)
		binary.BigEndian.PutUint64(header[len(header)-8:], uint64(length))
	}
	mask := make([]byte, 4)
	if _, err := rand.Read(mask); err != nil {
		return err
	}
	header = append(header, mask...)
	masked := make([]byte, len(payload))
	for index := range payload {
		masked[index] = payload[index] ^ mask[index%4]
	}
	if err := writeAll(w.connection, header); err != nil {
		return err
	}
	return writeAll(w.connection, masked)
}

func (w *cdpWebSocket) ReadMessage(ctx context.Context) ([]byte, error) {
	if w == nil || w.connection == nil {
		return nil, net.ErrClosed
	}
	var message bytes.Buffer
	started := false
	for {
		fin, opcode, payload, err := w.readFrame(ctx)
		if err != nil {
			return nil, err
		}
		switch opcode {
		case 0x8:
			return nil, io.EOF
		case 0x9:
			if err := w.writeFrame(ctx, 0xA, payload); err != nil {
				return nil, err
			}
			continue
		case 0xA:
			continue
		case 0x1, 0x2:
			if started {
				return nil, fmt.Errorf("unexpected WebSocket data frame")
			}
			started = true
		case 0x0:
			if !started {
				return nil, fmt.Errorf("unexpected WebSocket continuation frame")
			}
		default:
			return nil, fmt.Errorf("unsupported WebSocket opcode")
		}
		if int64(message.Len()+len(payload)) > w.maxMessage {
			return nil, fmt.Errorf("WebSocket message exceeds the size limit")
		}
		_, _ = message.Write(payload)
		if fin {
			return message.Bytes(), nil
		}
	}
}

func (w *cdpWebSocket) readFrame(ctx context.Context) (bool, byte, []byte, error) {
	if err := setConnectionDeadline(w.connection, ctx); err != nil {
		return false, 0, nil, err
	}
	header := make([]byte, 2)
	if _, err := io.ReadFull(w.reader, header); err != nil {
		return false, 0, nil, err
	}
	fin := header[0]&0x80 != 0
	if header[0]&0x70 != 0 {
		return false, 0, nil, fmt.Errorf("unsupported WebSocket extension bits")
	}
	opcode := header[0] & 0x0f
	masked := header[1]&0x80 != 0
	if masked {
		return false, 0, nil, fmt.Errorf("server WebSocket frame must not be masked")
	}
	length := uint64(header[1] & 0x7f)
	switch length {
	case 126:
		extended := make([]byte, 2)
		if _, err := io.ReadFull(w.reader, extended); err != nil {
			return false, 0, nil, err
		}
		length = uint64(binary.BigEndian.Uint16(extended))
	case 127:
		extended := make([]byte, 8)
		if _, err := io.ReadFull(w.reader, extended); err != nil {
			return false, 0, nil, err
		}
		length = binary.BigEndian.Uint64(extended)
		if length&(uint64(1)<<63) != 0 {
			return false, 0, nil, fmt.Errorf("invalid WebSocket payload length")
		}
	}
	if opcode >= 0x8 && (!fin || length > 125) {
		return false, 0, nil, fmt.Errorf("invalid WebSocket control frame")
	}
	if length > uint64(w.maxMessage) {
		return false, 0, nil, fmt.Errorf("WebSocket frame exceeds the size limit")
	}
	payload := make([]byte, int(length))
	if _, err := io.ReadFull(w.reader, payload); err != nil {
		return false, 0, nil, err
	}
	return fin, opcode, payload, nil
}

func (w *cdpWebSocket) Close() {
	if w == nil || w.connection == nil {
		return
	}
	_ = w.connection.Close()
	w.connection = nil
}

func setConnectionDeadline(connection net.Conn, ctx context.Context) error {
	if deadline, ok := ctx.Deadline(); ok {
		return connection.SetDeadline(deadline)
	}
	return connection.SetDeadline(time.Time{})
}

func writeAll(writer io.Writer, payload []byte) error {
	for len(payload) > 0 {
		written, err := writer.Write(payload)
		if err != nil {
			return err
		}
		if written == 0 {
			return io.ErrShortWrite
		}
		payload = payload[written:]
	}
	return nil
}
