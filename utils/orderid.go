package utils

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"sync"
	"time"
)

// OrderIDGenerator 订单ID生成器
// 生成紧凑的 ClientOrderID，尽量控制在交易所自定义订单ID限制内
type OrderIDGenerator struct {
	mu       sync.Mutex
	lastSec  int64
	sequence int
}

var globalIDGen = &OrderIDGenerator{}

// GenerateOrderID 生成紧凑的订单ID
// 格式: {price_int}_{side}_{book}_{timestamp}{seq}
//
// 参数:
//
//	price: 订单价格
//	side: 订单方向 (BUY/SELL)
//	priceDecimals: 价格精度
//
// 返回值示例:
//
//	1e240_B_L_t6z1ab01  (价格65000，买单，long账本)
//	qe_S_S_t6z1ab02      (价格0.950，卖单，short账本)
//
// 注意: 价格整数和时间戳使用 base36 压缩，避免交易所截断导致无法解析。
func GenerateOrderID(price float64, side, book string, priceDecimals int) string {
	return GenerateOrderIDWithTag(price, side, book, priceDecimals, "")
}

// GenerateOrderIDWithTag 生成带归属标签的订单ID。
// 带标签格式: {tag}_{price_int}_{side}_{book}_{timestamp}{seq}
func GenerateOrderIDWithTag(price float64, side, book string, priceDecimals int, tag string) string {
	globalIDGen.mu.Lock()
	defer globalIDGen.mu.Unlock()

	// 1. 将价格转为整数字符串（避免浮点数）
	multiplier := math.Pow(10, float64(priceDecimals))
	priceInt := int64(math.Round(price * multiplier))

	// 2. 方向编码（单字符）
	sideCode := "B"
	if side == "SELL" {
		sideCode = "S"
	}

	bookCode := "L"
	if strings.EqualFold(book, "SHORT") {
		bookCode = "S"
	}

	pricePart := strconv.FormatInt(priceInt, 36)

	// 3. 生成紧凑的时间戳 + 序列号
	now := time.Now()
	currentSec := now.Unix()

	// 重置序列号（每秒重置）
	if currentSec != globalIDGen.lastSec {
		globalIDGen.lastSec = currentSec
		globalIDGen.sequence = 0
	}

	globalIDGen.sequence++

	seqPart := strconv.FormatInt(int64(globalIDGen.sequence%1296), 36)
	if len(seqPart) < 2 {
		seqPart = "0" + seqPart
	}
	timestampSeq := strconv.FormatInt(currentSec, 36) + seqPart

	tag = NormalizeOrderTag(tag)
	if tag != "" {
		return fmt.Sprintf("%s_%s_%s_%s_%s", tag, pricePart, sideCode, bookCode, timestampSeq)
	}
	return fmt.Sprintf("%s_%s_%s_%s", pricePart, sideCode, bookCode, timestampSeq)
}

// ParseOrderID 解析紧凑的订单ID
// 返回: price, side, book, timestamp, valid
func ParseOrderID(clientOrderID string, priceDecimals int) (float64, string, string, int64, bool) {
	price, side, book, timestamp, _, ok := ParseOrderIDWithTag(clientOrderID, priceDecimals)
	return price, side, book, timestamp, ok
}

// ParseOrderIDWithTag 解析新旧订单ID，兼容无标签的4段旧格式。
func ParseOrderIDWithTag(clientOrderID string, priceDecimals int) (float64, string, string, int64, string, bool) {
	parts := strings.Split(clientOrderID, "_")
	tag := ""
	if len(parts) == 5 {
		tag = parts[0]
		parts = parts[1:]
	} else if len(parts) != 4 {
		return 0, "", "", 0, "", false
	}

	isLegacyDecimal := len(parts[3]) >= 10 && isDecimal(parts[3])
	base := 36
	if isLegacyDecimal && isDecimal(parts[0]) {
		base = 10
	}
	priceInt, err := strconv.ParseInt(parts[0], base, 64)
	if err != nil {
		return 0, "", "", 0, "", false
	}

	// 还原为浮点数价格
	multiplier := math.Pow(10, float64(priceDecimals))
	price := float64(priceInt) / multiplier

	// 2. 解析方向
	sideCode := parts[1]
	side := "BUY"
	if sideCode == "S" {
		side = "SELL"
	}

	bookCode := parts[2]
	book := "LONG"
	if bookCode == "S" {
		book = "SHORT"
	}

	// 3. 解析时间戳（旧格式为10位秒级时间戳，新格式为base36秒级时间戳+2位序列号）
	timestampSeq := parts[3]
	if len(timestampSeq) < 3 {
		return 0, "", "", 0, "", false
	}
	var timestamp int64
	if isLegacyDecimal {
		timestamp, err = strconv.ParseInt(timestampSeq[:10], 10, 64)
		if err != nil {
			return 0, "", "", 0, "", false
		}
	} else {
		timestamp, err = strconv.ParseInt(timestampSeq[:len(timestampSeq)-2], 36, 64)
		if err != nil {
			return 0, "", "", 0, "", false
		}
	}

	return price, side, book, timestamp, tag, true
}

// OrderIDTag returns the optional robot ownership tag encoded in a ClientOrderID.
func OrderIDTag(clientOrderID string) (string, bool) {
	parts := strings.Split(clientOrderID, "_")
	if len(parts) == 5 {
		return parts[0], true
	}
	if len(parts) == 4 {
		return "", true
	}
	return "", false
}

func NormalizeOrderTag(tag string) string {
	tag = strings.ToLower(strings.TrimSpace(tag))
	if tag == "" {
		return ""
	}
	var b strings.Builder
	for _, ch := range tag {
		if (ch >= 'a' && ch <= 'z') || (ch >= '0' && ch <= '9') {
			b.WriteRune(ch)
		}
	}
	normalized := b.String()
	if len(normalized) > 6 {
		normalized = normalized[:6]
	}
	return normalized
}

func isDecimal(value string) bool {
	if value == "" {
		return false
	}
	for _, ch := range value {
		if ch < '0' || ch > '9' {
			return false
		}
	}
	return true
}

// AddBrokerPrefix 为不同交易所添加返佣前缀
//
// 交易所限制:
//   - Binance: 36字符限制，返佣前缀 "x-zdfVM8vY" (10字符)
//   - Gate.io: 30字符限制，返佣前缀 "t-" (2字符)
func AddBrokerPrefix(exchange, clientOrderID string) string {
	switch exchange {
	case "binance":
		// 币安返佣前缀: x-zdfVM8vY (10字符)
		prefix := "x-zdfVM8vY"
		result := prefix + clientOrderID

		// 长度检查（币安限制36字符）
		if len(result) > 36 {
			// 如果超长，截断 clientOrderID 部分
			maxIDLen := 36 - len(prefix)
			if maxIDLen > 0 {
				result = prefix + clientOrderID[:maxIDLen]
			} else {
				result = prefix
			}
		}
		return result

	case "gate":
		// Gate.io 返佣前缀: t- (2字符)
		prefix := "t-"
		result := prefix + clientOrderID

		// 长度检查（Gate.io 限制30字符）
		if len(result) > 30 {
			// 如果超长，截断 clientOrderID 部分
			maxIDLen := 30 - len(prefix)
			if maxIDLen > 0 {
				result = prefix + clientOrderID[:maxIDLen]
			} else {
				result = prefix
			}
		}
		return result

	default:
		return clientOrderID
	}
}

// RemoveBrokerPrefix 移除交易所返佣前缀
func RemoveBrokerPrefix(exchange, clientOrderID string) string {
	exchange = strings.ToLower(strings.TrimSpace(exchange))
	switch {
	case exchange == "binance" || strings.Contains(exchange, "binance"):
		prefix := "x-zdfVM8vY"
		if strings.HasPrefix(clientOrderID, prefix) {
			return clientOrderID[len(prefix):]
		}
		return clientOrderID

	case exchange == "gate" || strings.Contains(exchange, "gate"):
		if strings.HasPrefix(clientOrderID, "t-") {
			return clientOrderID[2:]
		}
		return clientOrderID

	default:
		return clientOrderID
	}
}
