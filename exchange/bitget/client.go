package bitget

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"nexus-trade-bot/exchange/ratelimit"
)

const (
	BitgetBaseURL = "https://api.bitget.com"
)

// Client Bitget HTTP 客户端
type Client struct {
	httpClient *http.Client
	signer     *Signer
	baseURL    string
}

const (
	defaultBitgetTradeQPS     = 9
	defaultBitgetPositionQPS  = 4
	defaultBitgetMarketQPS    = 18
	defaultBitgetRESTMaxRetry = 5
)

// NewClient 创建 Bitget 客户端
func NewClient(apiKey, secretKey, passphrase string) *Client {
	return &Client{
		httpClient: &http.Client{Timeout: 10 * time.Second},
		signer:     NewSigner(apiKey, secretKey, passphrase),
		baseURL:    BitgetBaseURL,
	}
}

// BitgetResponse Bitget API 通用响应结构
type BitgetResponse struct {
	Code    string          `json:"code"`
	Msg     string          `json:"msg"`
	Data    json.RawMessage `json:"data"`
	ReqTime int64           `json:"requestTime"`
}

// DoRequest 发送 HTTP 请求（带签名）
func (c *Client) DoRequest(ctx context.Context, method, path string, body interface{}) (*BitgetResponse, error) {
	var bodyBytes []byte
	var err error

	if body != nil {
		bodyBytes, err = json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("序列化请求体失败: %w", err)
		}
	}

	bodyStr := string(bodyBytes)
	if bodyStr == "" {
		bodyStr = ""
	}

	var lastErr error
	profile := bitgetRateLimitProfile(method, path)
	for attempt := 0; attempt <= bitgetRESTMaxRetry(); attempt++ {
		if err := ratelimit.Wait(ctx, profile); err != nil {
			return nil, err
		}

		timestamp := c.signer.GetTimestamp()
		signature := c.signer.Sign(timestamp, method, path, bodyStr)

		req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, bytes.NewBuffer(bodyBytes))
		if err != nil {
			return nil, fmt.Errorf("创建请求失败: %w", err)
		}

		// 添加 Bitget 必需的请求头
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("ACCESS-KEY", c.signer.GetAPIKey())
		req.Header.Set("ACCESS-SIGN", signature)
		req.Header.Set("ACCESS-TIMESTAMP", timestamp)
		req.Header.Set("ACCESS-PASSPHRASE", c.signer.GetPassphrase())
		req.Header.Set("locale", "en-US")
		req.Header.Set("X-CHANNEL-API-CODE", "3xh1b")

		resp, err := c.httpClient.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("请求失败: %w", err)
			if ctx.Err() != nil {
				return nil, lastErr
			}
			sleepBitgetRESTBackoff(ctx, attempt, "")
			continue
		}

		respBody, readErr := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if readErr != nil {
			return nil, fmt.Errorf("读取响应失败: %w", readErr)
		}

		var bitgetResp BitgetResponse
		if err := json.Unmarshal(respBody, &bitgetResp); err != nil {
			if resp.StatusCode == http.StatusTooManyRequests {
				lastErr = fmt.Errorf("bitget API 错误: http=%d, msg=Too Many Requests", resp.StatusCode)
				sleepBitgetRESTBackoff(ctx, attempt, resp.Header.Get("Retry-After"))
				continue
			}
			return nil, fmt.Errorf("解析响应失败: %w, 响应体: %s", err, string(respBody))
		}

		if bitgetResp.Code != "00000" || resp.StatusCode == http.StatusTooManyRequests {
			lastErr = fmt.Errorf("bitget API 错误: code=%s, msg=%s", bitgetResp.Code, bitgetResp.Msg)
			if isBitgetRateLimitResponse(resp.StatusCode, bitgetResp) && attempt < bitgetRESTMaxRetry() {
				sleepBitgetRESTBackoff(ctx, attempt, resp.Header.Get("Retry-After"))
				continue
			}
			return nil, lastErr
		}

		return &bitgetResp, nil
	}

	return nil, lastErr
}

func isBitgetRateLimitResponse(statusCode int, resp BitgetResponse) bool {
	msg := strings.ToLower(resp.Msg)
	return statusCode == http.StatusTooManyRequests ||
		resp.Code == "429" ||
		strings.Contains(msg, "too many requests") ||
		strings.Contains(msg, "rate limit")
}

func bitgetRateLimitProfile(method, path string) ratelimit.Profile {
	bucket := "GENERAL"
	defaultQPS := defaultBitgetTradeQPS
	if strings.Contains(path, "/market/") {
		bucket = "MARKET"
		defaultQPS = defaultBitgetMarketQPS
	} else if strings.Contains(path, "/position/") || strings.Contains(path, "/account/") {
		bucket = "POSITION"
		defaultQPS = defaultBitgetPositionQPS
	} else if strings.Contains(path, "/order/") || method != http.MethodGet {
		bucket = "TRADE"
		defaultQPS = defaultBitgetTradeQPS
	}
	return ratelimit.Profile{
		Exchange:    "BITGET",
		Bucket:      bucket,
		DefaultQPS:  defaultQPS,
		EnvOverride: "NEXUS_BITGET_REST_QPS",
	}
}

func bitgetRESTMaxRetry() int {
	if raw := strings.TrimSpace(os.Getenv("NEXUS_BITGET_REST_MAX_RETRY")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed >= 0 {
			if parsed > 10 {
				return 10
			}
			return parsed
		}
	}
	return defaultBitgetRESTMaxRetry
}

func sleepBitgetRESTBackoff(ctx context.Context, attempt int, retryAfter string) {
	delay := time.Duration(300*(attempt+1)) * time.Millisecond
	if retryAfter != "" {
		if seconds, err := strconv.Atoi(strings.TrimSpace(retryAfter)); err == nil && seconds > 0 {
			delay = time.Duration(seconds) * time.Second
		}
	}
	if delay > 5*time.Second {
		delay = 5 * time.Second
	}
	timer := time.NewTimer(delay)
	select {
	case <-ctx.Done():
		timer.Stop()
	case <-timer.C:
	}
}
