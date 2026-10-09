package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
)

type Client struct {
	BaseURL string
	HTTP    *http.Client
}

func New(baseURL string) *Client {
	return &Client{
		BaseURL: strings.TrimRight(baseURL, "/"),
		HTTP:    &http.Client{Timeout: 0}, // no timeout — approvals can block
	}
}

// RequestApproval calls the Telegram MCP server's tool directly via JSON-RPC over SSE.
// For pipeline use, we expose a simple HTTP shim on the MCP server (see §14).
type approvalReq struct {
	Prompt         string   `json:"prompt"`
	Context        string   `json:"context"`
	Options        []string `json:"options"`
	TaskTag        string   `json:"task_tag"`
	TimeoutSeconds int      `json:"timeout_seconds"`
}

type approvalResp struct {
	Status string `json:"status"`
	Choice string `json:"choice"`
	Error  string `json:"error,omitempty"`
}

func (c *Client) RequestApproval(ctx context.Context, prompt, contextStr, taskTag string,
	options []string, timeoutS int) (string, error) {

	id := uuid.NewString()[:8]
	body, _ := json.Marshal(approvalReq{
		Prompt: prompt, Context: contextStr,
		Options: options, TaskTag: fmt.Sprintf("%s/%s", taskTag, id),
		TimeoutSeconds: timeoutS,
	})
	req, _ := http.NewRequestWithContext(ctx, "POST",
		c.BaseURL+"/pipeline/approve", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	// Wait a maximum of timeout+30s
	cl := *c.HTTP
	cl.Timeout = time.Duration(timeoutS+30) * time.Second

	resp, err := cl.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)

	var ar approvalResp
	if err := json.Unmarshal(b, &ar); err != nil {
		return "", fmt.Errorf("decode: %w (body=%s)", err, string(b))
	}
	if ar.Error != "" {
		return "", fmt.Errorf("%s", ar.Error)
	}
	if ar.Status != "answered" {
		return "", fmt.Errorf("approval not granted: %s", ar.Status)
	}
	return ar.Choice, nil
}

type notifyReq struct {
	Message string `json:"message"`
	TaskTag string `json:"task_tag"`
}

func (c *Client) Notify(ctx context.Context, message, taskTag string) error {
	body, _ := json.Marshal(notifyReq{Message: message, TaskTag: taskTag})
	req, _ := http.NewRequestWithContext(ctx, "POST",
		c.BaseURL+"/pipeline/notify", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	cl := *c.HTTP
	cl.Timeout = 10 * time.Second
	resp, err := cl.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}