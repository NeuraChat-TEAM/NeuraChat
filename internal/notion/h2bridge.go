package notion

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"neura/internal/h2sidecar"
)

type h2Bridge struct {
	cmd    *exec.Cmd
	guard  io.Closer
	base   string
	token  string
	client *http.Client
}

func (b *h2Bridge) close() {
	if b == nil {
		return
	}
	if b.cmd != nil && b.cmd.Process != nil {
		_ = b.cmd.Process.Kill()
		_, _ = b.cmd.Process.Wait()
	}
	if b.guard != nil {
		_ = b.guard.Close()
	}
}

type h2BridgeResponse struct {
	Status  int               `json:"status"`
	Headers map[string]string `json:"headers"`
	Body    string            `json:"body"`
	Error   string            `json:"error"`
}

func randomBridgeToken() string {
	var value [32]byte
	if _, err := rand.Read(value[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(value[:])
}

func startH2Bridge() (*h2Bridge, error) {
	cache, err := os.UserCacheDir()
	if err != nil {
		return nil, err
	}
	binary, err := h2sidecar.Ensure(filepath.Join(cache, "Neura", "tools"))
	if err != nil {
		return nil, err
	}
	token := randomBridgeToken()
	cmd := exec.Command(binary)
	cmd.Env = append(os.Environ(),
		"NEURA_H2_TOKEN="+token,
		"NEURA_PARENT_PID="+strconv.Itoa(os.Getpid()),
	)
	prepareBackgroundCommand(cmd)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	// Windows Job убивает sidecar вместе с Neura даже при аварийном завершении;
	// NEURA_PARENT_PID внутри sidecar остаётся вторым уровнем страховки.
	guard, _ := bindBackgroundCommand(cmd)
	ready := make(chan string, 1)
	go func() {
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			if strings.HasPrefix(scanner.Text(), "NEURA_H2_READY ") {
				ready <- strings.TrimSpace(strings.TrimPrefix(scanner.Text(), "NEURA_H2_READY "))
				return
			}
		}
		ready <- ""
	}()
	select {
	case port := <-ready:
		if port == "" {
			_ = cmd.Process.Kill()
			if guard != nil {
				_ = guard.Close()
			}
			return nil, fmt.Errorf("HTTP/2 sidecar не запустился: %s", strings.TrimSpace(stderr.String()))
		}
		return &h2Bridge{
			cmd: cmd, guard: guard, base: "http://127.0.0.1:" + port, token: token,
			client: &http.Client{Timeout: 6 * time.Minute},
		}, nil
	case <-time.After(15 * time.Second):
		_ = cmd.Process.Kill()
		if guard != nil {
			_ = guard.Close()
		}
		return nil, errors.New("таймаут запуска HTTP/2 sidecar")
	}
}

func (b *h2Bridge) requestBody(origin, path string, headers map[string]string, body interface{}) ([]byte, error) {
	return json.Marshal(map[string]interface{}{
		"origin": origin, "path": path, "headers": headers, "body": body,
	})
}

// postStream keeps the local HTTP response body connected to the upstream
// HTTP/2 stream. Unlike post, it does not buffer the complete inference in the
// sidecar, so NDJSON patches reach the UI as soon as Notion emits them.
func (b *h2Bridge) postStream(ctx context.Context, origin, path string, headers map[string]string, body interface{}) (*http.Response, error) {
	requestBody, err := b.requestBody(origin, path, headers, body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, b.base+"/stream", bytes.NewReader(requestBody))
	if err != nil {
		return nil, err
	}
	req.Header.Set("content-type", "application/json")
	req.Header.Set("x-neura-token", b.token)
	streamClient := *b.client
	streamClient.Timeout = 0
	return streamClient.Do(req)
}

func (b *h2Bridge) post(ctx context.Context, origin, path string, headers map[string]string, body interface{}) (*http.Response, error) {
	requestBody, err := b.requestBody(origin, path, headers, body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, b.base+"/request", bytes.NewReader(requestBody))
	if err != nil {
		return nil, err
	}
	req.Header.Set("content-type", "application/json")
	req.Header.Set("x-neura-token", b.token)
	response, err := b.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, err
	}
	var result h2BridgeResponse
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("HTTP/2 sidecar вернул некорректный ответ: %w", err)
	}
	if response.StatusCode != http.StatusOK || result.Error != "" {
		if result.Error == "" {
			result.Error = string(raw)
		}
		return nil, errors.New(result.Error)
	}
	out := &http.Response{
		StatusCode: result.Status,
		Status:     fmt.Sprintf("%d %s", result.Status, http.StatusText(result.Status)),
		Proto:      "HTTP/2.0",
		ProtoMajor: 2,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(result.Body)),
	}
	for name, value := range result.Headers {
		out.Header.Set(name, value)
	}
	return out, nil
}
