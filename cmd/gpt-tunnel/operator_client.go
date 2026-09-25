package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/service"
)

const (
	cliOperatorTokenHeader = "X-GPT-Tunnel-Operator-Token"
	cliOperatorBodyLimit   = 1 << 20
	cliOperatorTimeout     = 20 * time.Second
)

type operatorCLIEnvelope[T any] struct {
	OK     bool `json:"ok"`
	Result T    `json:"result"`
	Error  struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func operatorCLIRequest[T any](ctx context.Context, c config.Config, path string, input any) (T, error) {
	var zero T
	token, err := service.ReadOperatorToken(c.StateDir)
	if err != nil {
		return zero, fmt.Errorf("Gateway daemon/control-plane unavailable")
	}
	body, err := json.Marshal(input)
	if err != nil {
		return zero, fmt.Errorf("encode Gateway operator request: %w", err)
	}
	if len(body) > cliOperatorBodyLimit {
		return zero, fmt.Errorf("Gateway operator request exceeds bounds")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+c.ListenAddr+path, bytes.NewReader(body))
	if err != nil {
		return zero, fmt.Errorf("create Gateway operator request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set(cliOperatorTokenHeader, token)
	client := &http.Client{
		Timeout: cliOperatorTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	response, err := client.Do(request)
	if err != nil {
		return zero, fmt.Errorf("Gateway daemon/control-plane unavailable: %w", err)
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, cliOperatorBodyLimit+1))
	if err != nil || len(responseBody) > cliOperatorBodyLimit {
		return zero, fmt.Errorf("Gateway operator response exceeds bounds")
	}
	decoder := json.NewDecoder(bytes.NewReader(responseBody))
	decoder.DisallowUnknownFields()
	var envelope operatorCLIEnvelope[T]
	if err := decoder.Decode(&envelope); err != nil {
		return zero, fmt.Errorf("Gateway operator response is invalid")
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return zero, fmt.Errorf("Gateway operator response is invalid")
	}
	if response.StatusCode != http.StatusOK || !envelope.OK {
		if envelope.Error.Message == "" {
			return zero, fmt.Errorf("Gateway operator request failed")
		}
		return zero, fmt.Errorf("Gateway operator request failed: %s", envelope.Error.Message)
	}
	return envelope.Result, nil
}
