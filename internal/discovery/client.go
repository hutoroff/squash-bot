package discovery

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const maxControlResponseBytes = 64 << 10

type ControlError struct {
	Status int
}

func (e *ControlError) Error() string {
	return fmt.Sprintf("service registry returned status %d", e.Status)
}

type HTTPRegistrar struct {
	baseURL string
	secret  string
	client  *http.Client
}

func NewHTTPRegistrar(baseURL, secret string) (*HTTPRegistrar, error) {
	parsed, err := url.Parse(strings.TrimRight(baseURL, "/"))
	if err != nil || parsed.Scheme != "http" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") {
		return nil, fmt.Errorf("discovery URL must be an HTTP origin")
	}
	if len(secret) < 32 {
		return nil, fmt.Errorf("discovery internal secret is invalid")
	}
	return &HTTPRegistrar{baseURL: strings.TrimRight(baseURL, "/"), secret: secret, client: &http.Client{
		Timeout:       3 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}, nil
}

func (c *HTTPRegistrar) Register(ctx context.Context, descriptor Descriptor) (*Lease, error) {
	return c.doLease(ctx, http.MethodPut, "/internal/discovery/v1/instances/"+url.PathEscape(descriptor.InstanceID), descriptor)
}
func (c *HTTPRegistrar) Heartbeat(ctx context.Context, instanceID string, state InstanceState) (*Lease, error) {
	return c.doLease(ctx, http.MethodPost, "/internal/discovery/v1/instances/"+url.PathEscape(instanceID)+"/heartbeat", map[string]InstanceState{"state": state})
}
func (c *HTTPRegistrar) Drain(ctx context.Context, instanceID string) error {
	return c.doEmpty(ctx, http.MethodPost, "/internal/discovery/v1/instances/"+url.PathEscape(instanceID)+"/drain")
}
func (c *HTTPRegistrar) Deregister(ctx context.Context, instanceID string) error {
	return c.doEmpty(ctx, http.MethodDelete, "/internal/discovery/v1/instances/"+url.PathEscape(instanceID))
}

func (c *HTTPRegistrar) doLease(ctx context.Context, method, path string, body any) (*Lease, error) {
	response, err := c.do(ctx, method, path, body)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	var lease Lease
	if err := decodeBounded(response.Body, &lease); err != nil {
		return nil, err
	}
	return &lease, nil
}
func (c *HTTPRegistrar) doEmpty(ctx context.Context, method, path string) error {
	response, err := c.do(ctx, method, path, nil)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	written, err := io.Copy(io.Discard, io.LimitReader(response.Body, maxControlResponseBytes+1))
	if err != nil {
		return err
	}
	if written > maxControlResponseBytes {
		return fmt.Errorf("service registry response exceeds bound")
	}
	return nil
}
func (c *HTTPRegistrar) do(ctx context.Context, method, path string, body any) (*http.Response, error) {
	var payload io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		payload = bytes.NewReader(raw)
	}
	request, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, payload)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Authorization", "Bearer "+c.secret)
	request.Header.Set("Accept", "application/json")
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := c.client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("service registry request: %w", err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		response.Body.Close()
		return nil, &ControlError{Status: response.StatusCode}
	}
	return response, nil
}
func decodeBounded(reader io.Reader, target any) error {
	raw, err := io.ReadAll(io.LimitReader(reader, maxControlResponseBytes+1))
	if err != nil {
		return err
	}
	if len(raw) > maxControlResponseBytes {
		return fmt.Errorf("service registry response exceeds bound")
	}
	if err := json.Unmarshal(raw, target); err != nil {
		return fmt.Errorf("decode service registry response: %w", err)
	}
	return nil
}
