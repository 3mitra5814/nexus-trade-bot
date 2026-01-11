package okx

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

const (
	okxBaseURL   = "https://www.okx.com"
	okxPublicWS  = "wss://ws.okx.com:8443/ws/v5/public"
	okxPrivateWS = "wss://ws.okx.com:8443/ws/v5/private"
)

type Response struct {
	Code string          `json:"code"`
	Msg  string          `json:"msg"`
	Data json.RawMessage `json:"data"`
}

type Client struct {
	httpClient *http.Client
	signer     *Signer
	baseURL    string
}

func NewClient(apiKey, secretKey, passphrase string) *Client {
	return &Client{
		httpClient: &http.Client{Timeout: 10 * time.Second},
		signer:     NewSigner(apiKey, secretKey, passphrase),
		baseURL:    okxBaseURL,
	}
}

func (c *Client) WebSocketLoginArgs() []map[string]string {
	return c.signer.WebSocketLoginArgs()
}

func (c *Client) DoPublicRequest(ctx context.Context, method, path string, query map[string]string) (*Response, error) {
	return c.doRequest(ctx, method, path, query, nil, false)
}

func (c *Client) DoSignedRequest(ctx context.Context, method, path string, query map[string]string, body any) (*Response, error) {
	return c.doRequest(ctx, method, path, query, body, true)
}

func (c *Client) doRequest(ctx context.Context, method, path string, query map[string]string, body any, signed bool) (*Response, error) {
	encodedQuery := encodeQuery(query)
	requestPath := path
	if encodedQuery != "" {
		requestPath += "?" + encodedQuery
	}

	var bodyBytes []byte
	var err error
	if body != nil {
		bodyBytes, err = json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("序列化 OKX 请求体失败: %w", err)
		}
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+requestPath, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("创建 OKX 请求失败: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	if signed {
		timestamp := c.signer.RestTimestamp()
		req.Header.Set("OK-ACCESS-KEY", c.signer.APIKey())
		req.Header.Set("OK-ACCESS-SIGN", c.signer.Sign(timestamp, method, requestPath, string(bodyBytes)))
		req.Header.Set("OK-ACCESS-TIMESTAMP", timestamp)
		req.Header.Set("OK-ACCESS-PASSPHRASE", c.signer.Passphrase())
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("OKX 请求失败: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("读取 OKX 响应失败: %w", err)
	}

	var response Response
	if err := json.Unmarshal(respBody, &response); err != nil {
		return nil, fmt.Errorf("解析 OKX 响应失败: %w, 响应体: %s", err, string(respBody))
	}
	if response.Code != "0" {
		return nil, fmt.Errorf("OKX API 错误: code=%s, msg=%s", response.Code, strings.TrimSpace(response.Msg))
	}

	return &response, nil
}

func encodeQuery(query map[string]string) string {
	if len(query) == 0 {
		return ""
	}

	keys := make([]string, 0, len(query))
	for key := range query {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	values := url.Values{}
	for _, key := range keys {
		values.Set(key, query[key])
	}
	return values.Encode()
}
