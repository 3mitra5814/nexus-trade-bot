package bybit

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
	bybitBaseURL       = "https://api.bybit.com"
	bybitPublicLinear  = "wss://stream.bybit.com/v5/public/linear"
	bybitPrivateWS     = "wss://stream.bybit.com/v5/private"
	bybitBrokerReferer = "Eu000962"
)

type Response struct {
	RetCode int             `json:"retCode"`
	RetMsg  string          `json:"retMsg"`
	Result  json.RawMessage `json:"result"`
	Time    int64           `json:"time"`
}

type Client struct {
	httpClient *http.Client
	signer     *Signer
	baseURL    string
}

func NewClient(apiKey, secretKey string) *Client {
	return &Client{
		httpClient: &http.Client{Timeout: 10 * time.Second},
		signer:     NewSigner(apiKey, secretKey),
		baseURL:    bybitBaseURL,
	}
}

func (c *Client) WebSocketAuthArgs() []any {
	return c.signer.WebSocketAuthArgs()
}

func (c *Client) DoPublicRequest(ctx context.Context, method, path string, query map[string]string) (*Response, error) {
	return c.doRequest(ctx, method, path, query, nil, false)
}

func (c *Client) DoSignedRequest(ctx context.Context, method, path string, query map[string]string, body map[string]any) (*Response, error) {
	return c.doRequest(ctx, method, path, query, body, true)
}

func (c *Client) doRequest(ctx context.Context, method, path string, query map[string]string, body map[string]any, signed bool) (*Response, error) {
	encodedQuery := encodeQuery(query)
	requestURL := c.baseURL + path
	if encodedQuery != "" {
		requestURL += "?" + encodedQuery
	}

	var bodyBytes []byte
	var err error
	if body != nil {
		bodyBytes, err = json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("序列化 Bybit 请求体失败: %w", err)
		}
	}

	req, err := http.NewRequestWithContext(ctx, method, requestURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("创建 Bybit 请求失败: %w", err)
	}

	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	if signed {
		timestamp := c.signer.Timestamp()
		payload := encodedQuery
		if method != http.MethodGet {
			payload = string(bodyBytes)
		}
		req.Header.Set("X-BAPI-API-KEY", c.signer.APIKey())
		req.Header.Set("X-BAPI-TIMESTAMP", fmt.Sprintf("%d", timestamp))
		req.Header.Set("X-BAPI-RECV-WINDOW", fmt.Sprintf("%d", c.signer.RecvWindow()))
		req.Header.Set("X-BAPI-SIGN", c.signer.SignPayload(timestamp, payload))
		req.Header.Set("X-Referer", bybitBrokerReferer)
		req.Header.Set("Referer", bybitBrokerReferer)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("Bybit 请求失败: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("读取 Bybit 响应失败: %w", err)
	}

	var response Response
	if err := json.Unmarshal(respBody, &response); err != nil {
		return nil, fmt.Errorf("解析 Bybit 响应失败: %w, 响应体: %s", err, string(respBody))
	}

	if response.RetCode != 0 {
		return nil, fmt.Errorf("Bybit API 错误: code=%d, msg=%s", response.RetCode, strings.TrimSpace(response.RetMsg))
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
