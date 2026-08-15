package adapters

import (
	"bufio"
	"context"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestCDPWebSocketRoundTripMasksClientAndHandlesPingAndFragments(t *testing.T) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	serverErrors := make(chan error, 1)
	go func() {
		connection, acceptErr := listener.Accept()
		if acceptErr != nil {
			serverErrors <- acceptErr
			return
		}
		defer connection.Close()
		reader := bufio.NewReader(connection)
		request, readErr := http.ReadRequest(reader)
		if readErr != nil {
			serverErrors <- readErr
			return
		}
		key := request.Header.Get("Sec-WebSocket-Key")
		accept := sha1.Sum([]byte(key + webSocketGUID))
		if _, writeErr := fmt.Fprintf(connection,
			"HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: %s\r\n\r\n",
			base64.StdEncoding.EncodeToString(accept[:])); writeErr != nil {
			serverErrors <- writeErr
			return
		}

		opcode, requestPayload, frameErr := readMaskedClientFrame(reader)
		if frameErr != nil {
			serverErrors <- frameErr
			return
		}
		if opcode != 0x1 {
			serverErrors <- fmt.Errorf("unexpected client opcode %d", opcode)
			return
		}
		var cdpRequest struct {
			ID     int64  `json:"id"`
			Method string `json:"method"`
		}
		if err := json.Unmarshal(requestPayload, &cdpRequest); err != nil || cdpRequest.ID != 1 || cdpRequest.Method != "Runtime.evaluate" {
			serverErrors <- fmt.Errorf("unexpected CDP request: %s", requestPayload)
			return
		}

		if err := writeServerFrame(connection, true, 0x9, []byte("ping")); err != nil {
			serverErrors <- err
			return
		}
		pongOpcode, pongPayload, pongErr := readMaskedClientFrame(reader)
		if pongErr != nil || pongOpcode != 0xA || string(pongPayload) != "ping" {
			serverErrors <- fmt.Errorf("invalid pong: opcode=%d payload=%q err=%v", pongOpcode, pongPayload, pongErr)
			return
		}

		response := []byte(`{"id":1,"result":{"result":{"type":"number","value":42}}}`)
		middle := len(response) / 2
		if err := writeServerFrame(connection, false, 0x1, response[:middle]); err != nil {
			serverErrors <- err
			return
		}
		if err := writeServerFrame(connection, true, 0x0, response[middle:]); err != nil {
			serverErrors <- err
			return
		}
		serverErrors <- nil
	}()

	port := listener.Addr().(*net.TCPAddr).Port
	endpoint, _ := url.Parse(fmt.Sprintf("ws://127.0.0.1:%d/devtools/page/synthetic", port))
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	socket, err := dialCDPWebSocket(ctx, endpoint, 1<<20)
	if err != nil {
		t.Fatalf("dialCDPWebSocket: %v", err)
	}
	client := &cdpClient{socket: socket}
	defer client.Close()
	var value int
	if err := client.Evaluate(ctx, "40 + 2", false, &value); err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if value != 42 {
		t.Fatalf("unexpected evaluation value %d", value)
	}
	if err := <-serverErrors; err != nil {
		t.Fatal(err)
	}
}

func TestValidateLoopbackWebSocketURL(t *testing.T) {
	if _, err := validateLoopbackWebSocketURL("ws://127.0.0.1:9229/devtools/page/id", 9229); err != nil {
		t.Fatalf("valid loopback URL rejected: %v", err)
	}
	if _, err := validateLoopbackWebSocketURL("ws://127.0.0.1:9229/synthetic-node-target", 9229); err != nil {
		t.Fatalf("valid Node inspector URL rejected: %v", err)
	}
	for _, endpoint := range []string{
		"wss://127.0.0.1:9229/devtools/page/id",
		"ws://localhost:9229/devtools/page/id",
		"ws://192.0.2.1:9229/devtools/page/id",
		"ws://127.0.0.1:9339/devtools/page/id",
		"ws://127.0.0.1:9229/",
	} {
		if _, err := validateLoopbackWebSocketURL(endpoint, 9229); err == nil {
			t.Fatalf("unsafe or mismatched endpoint accepted: %s", endpoint)
		}
	}
}

func TestCDPWebSocketRejectsOversizedServerFrame(t *testing.T) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	serverErrors := make(chan error, 1)
	go func() {
		connection, acceptErr := listener.Accept()
		if acceptErr != nil {
			serverErrors <- acceptErr
			return
		}
		defer connection.Close()
		reader := bufio.NewReader(connection)
		request, readErr := http.ReadRequest(reader)
		if readErr != nil {
			serverErrors <- readErr
			return
		}
		accept := sha1.Sum([]byte(request.Header.Get("Sec-WebSocket-Key") + webSocketGUID))
		if _, writeErr := fmt.Fprintf(connection,
			"HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: %s\r\n\r\n",
			base64.StdEncoding.EncodeToString(accept[:])); writeErr != nil {
			serverErrors <- writeErr
			return
		}
		serverErrors <- writeServerFrame(connection, true, 0x1, []byte("123456789"))
	}()

	port := listener.Addr().(*net.TCPAddr).Port
	endpoint, _ := url.Parse(fmt.Sprintf("ws://127.0.0.1:%d/devtools/page/synthetic", port))
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	socket, err := dialCDPWebSocket(ctx, endpoint, 8)
	if err != nil {
		t.Fatal(err)
	}
	defer socket.Close()
	if _, err := socket.ReadMessage(ctx); err == nil || !strings.Contains(err.Error(), "size limit") {
		t.Fatalf("oversized frame was not rejected: %v", err)
	}
	if err := <-serverErrors; err != nil {
		t.Fatal(err)
	}
}

func readMaskedClientFrame(reader *bufio.Reader) (byte, []byte, error) {
	header := make([]byte, 2)
	if _, err := io.ReadFull(reader, header); err != nil {
		return 0, nil, err
	}
	if header[0]&0x80 == 0 || header[1]&0x80 == 0 {
		return 0, nil, fmt.Errorf("client frame is not final and masked")
	}
	opcode := header[0] & 0x0f
	length := uint64(header[1] & 0x7f)
	switch length {
	case 126:
		extended := make([]byte, 2)
		if _, err := io.ReadFull(reader, extended); err != nil {
			return 0, nil, err
		}
		length = uint64(binary.BigEndian.Uint16(extended))
	case 127:
		extended := make([]byte, 8)
		if _, err := io.ReadFull(reader, extended); err != nil {
			return 0, nil, err
		}
		length = binary.BigEndian.Uint64(extended)
	}
	mask := make([]byte, 4)
	if _, err := io.ReadFull(reader, mask); err != nil {
		return 0, nil, err
	}
	payload := make([]byte, int(length))
	if _, err := io.ReadFull(reader, payload); err != nil {
		return 0, nil, err
	}
	for index := range payload {
		payload[index] ^= mask[index%4]
	}
	return opcode, payload, nil
}

func writeServerFrame(writer io.Writer, fin bool, opcode byte, payload []byte) error {
	first := opcode & 0x0f
	if fin {
		first |= 0x80
	}
	header := []byte{first}
	switch {
	case len(payload) <= 125:
		header = append(header, byte(len(payload)))
	case len(payload) <= int(^uint16(0)):
		header = append(header, 126, 0, 0)
		binary.BigEndian.PutUint16(header[len(header)-2:], uint16(len(payload)))
	default:
		header = append(header, 127, 0, 0, 0, 0, 0, 0, 0, 0)
		binary.BigEndian.PutUint64(header[len(header)-8:], uint64(len(payload)))
	}
	if err := writeAll(writer, header); err != nil {
		return err
	}
	return writeAll(writer, payload)
}

func writeTestWebSocketHandshake(writer io.Writer, key string) error {
	accept := sha1.Sum([]byte(key + webSocketGUID))
	_, err := fmt.Fprintf(writer,
		"HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: %s\r\n\r\n",
		base64.StdEncoding.EncodeToString(accept[:]))
	return err
}
