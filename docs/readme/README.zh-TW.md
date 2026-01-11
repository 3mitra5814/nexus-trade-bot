# nexus-trade-bot

<p align="center">
  <img src="../../logo/logo.png" alt="Nexus Trade Bot" width="720">
</p>

**網格機器人控制中心專為那些想要交易量、自動化和風險可見性而無需照顧每個訂單的交易者而建造。期貨為預設模式；主要中心化交易所均支援現貨網格。 **

[![Go](https://img.shields.io/badge/Go-1.26%2B-00ADD8?logo=go&logoColor=white)](https://go.dev/)
[![License](https://img.shields.io/badge/license-MIT-green)](../../LICENSE)
[![One Command](https://img.shields.io/badge/install-one%20command-blue)](#one-command-install)
[![Languages](https://img.shields.io/badge/languages-11-orange)](#languages)

## 加入用戶群

部署問題、交易所介面細節、實盤經驗和新版本回饋，放在一個用戶群裡會更有效率。歡迎加入 Nexus Trade Bot 用戶群：[https://t.me/nexustradebot8](https://t.me/nexustradebot8)。

## 語言

[English](../../README.md) | [简体中文](README.zh-CN.md) | [Русский](README.ru.md) | [한국어](README.ko.md) | [日本語](README.ja.md) | [Español](README.es.md) | [Tiếng Việt](README.vi.md) | [हिन्दी](README.hi.md) | [Português](README.pt.md) | [العربية](README.ar.md) | 繁體中文


## 一鍵安裝

在新的 Ubuntu 伺服器上運行：

```bash
wget -O nexus-trade-bot.sh https://raw.githubusercontent.com/haohaoi34/nexus-trade-bot/main/scripts/nexus-trade-bot.sh && chmod +x nexus-trade-bot.sh && ./nexus-trade-bot.sh install && ./nexus-trade-bot.sh start
```

伺服器自動運行：

- 安裝缺少的 Ubuntu 依賴項。
- 如果伺服器沒有相容版本，則安裝 Go。
- 當 `https://github.com/haohaoi34/nexus-trade-bot.git` 尚未在源簽出內運行時克隆 `https://github.com/haohaoi34/nexus-trade-bot.git`。
- 從原始碼建立機器人，或使用發布包中捆綁的二進位檔案。
- 如果需要，請從 `config.example.yaml` 建立 `config.yaml` 並將其保留在本地。
- 在背景啟動 Web 控制台並將日誌寫入 `logs/`。
- 自動識別伺服器公網 IP，並用醒目的訪問提示區塊列印本機 URL、伺服器 URL、PID 檔案和日誌路徑。

有用的伺服器命令：

```bash
./nexus-trade-bot.sh install
./nexus-trade-bot.sh start
./nexus-trade-bot.sh status
./nexus-trade-bot.sh logs
./nexus-trade-bot.sh restart
./nexus-trade-bot.sh stop
./nexus-trade-bot.sh update
```

預設網頁登入：

```text
username: admin
password: admin
```

首次登入後立即更改預設密碼。


## 支援的交易所

|交流 |支援|
| --- | --- |
| Binance | 期貨：穩定。現貨：穩定。最適合高流動性 USDT/USDC 永續和現貨網格。 |
| Bitget | 期貨：穩定。現貨：穩定。最適合網格交易和返傭交易量策略。 |
| Gate.io | 期貨：穩定。現貨：穩定。對於多交易所多元化很有用。 |
| Bybit | 期貨：測試版。現貨：穩定。首先用較小的尺寸進行測試。 |
| OKX | 期貨：測試版。現貨：穩定。需要 API 金鑰、金鑰和密碼。 |
| Hyperliquid | 期貨：測試版。現貨：測試版。使用基於錢包的 API 設定和 USDC 現貨對。 |

Bitget 返利連結：[最高 70% 手續費返利，邀請碼 `4n9z`](https://partner.hdmune.cn/bg/3DLRKF)。


## 它的作用

nexus-trade-bot 可協助您從乾淨的 Web 控制台執行網格策略：

- 新增交易所API一次並驗證後再使用。
- 為不同的符號、帳戶和方向創建多個機器人。
- 選擇期貨或現貨。預設選擇期貨。
- 期貨使用多頭、空頭或中性模式；當場使用長模式。
- 自動載入 Binance、Bitget、Bybit、OKX、Gate 和 Hyperliquid 現貨符號。
- 即時觀察餘額、交易量、機器人狀態和損益。
- 暫停機器人，更改參數，然後使用最新設定重新啟動。
- 讓風險監控器在市場異常波動時停止交易。

它是為關心執行、營業額和控制的交易者設計的，而不是為那些想要整天編輯個人資料的人設計的。

## 核心思想

網格機器人以固定的價格間隔下達買賣訂單。它不是試圖預測確切的頂部或底部，而是圍繞一個價格範圍進行工作：

- 當價格下降時，機器人會根據您的網格設定逐漸購買。
- 當價格反彈時，機器人會逐步賣出更高的價格。
- 在橫盤整理或向上復甦的市場中，這可能會將波動性轉化為重複的已實現交易。
- 在單向下跌趨勢中，機器人累積頭寸，需要足夠的保證金、風險限制和耐心。

目標不是神奇的利潤。目標是嚴格執行：一致的訂單間隔、受控的訂單規模、可見的風險以及市場異常時的自動反應。


## 策略範例：高交易量的 ETH 網格

這是一個實際範例，用於了解交易者如何使用此類機器人。

假設 ETH 的交易價格接近 `3000`，並且您配置：

| 參數 | 範例 |
| --- | --- |
| 交易對 | `ETHUSDT` 或 `ETHUSDC` |
| 方向 | 做多網格 |
| 價格間隔 | `1 USDT` |
| 下單金額 | 每個網格訂單 `300 USDT` |
| 市場風格 | 橫盤或向上修復的市場 |

由於 `1 USDT` 間隔較窄且 ETH 流動性活躍，該機器人可能會產生非常高的換手率。在繁忙的市場中，這種配置可以達到數百萬美元的每日交易量，以及數千萬美元的月交易量，具體取決於波動性、費用、流動性和帳戶規模。

這就是為什麼許多交易者使用網格系統有兩個目的：

- **交易量建置**：增加交易所 VIP 等級或活動的期貨交易量。
- **波動收穫**：在一定範圍內重複低買高賣。


## 回撤邏輯範例

網格交易必須圍繞回撤進行規劃。

假設 ETH 從 `3000` 附近開始，然後跌至 `2700`。長網格通常會承受浮動損失，因為它是在下跌過程中購買的。但它也累積了較低的條目。如果價格隨後從 `2700` 反彈至 `2850`，平均成本可能會被拉低到足以使帳戶比單次進入 `3000` 更早達到盈虧平衡。

如果 ETH 返回接近原始 `3000` 區域，則該策略可能會從以下兩個方面受益：

- 庫存從反彈中恢復；
- 實現了運動期間收集的網格價差。

一些交易者保留了更大的保證金緩衝，例如在 `30,000 USDT` 周圍，以設計一個可以容忍更深層波動的網格，例如 `1000 USDT` ETH 回撤。這是否足夠取決於槓桿、保證金模式、頭寸規模、費用、交易所維持保證金規則以及您的網格的激進程度。

重要的一點是：電網獲利來自於準備，而不是樂觀。在運行規模之前，請計算市場可以向對您不利的方向移動多遠，機器人可以累積多少頭寸，以及如果市場沒有快速反彈會發生什麼。


## 內建風險保護

快速單向下降對於激進的長網格來說是最糟糕的環境。 nexus-trade-bot 包含一個市場風險監視器，旨在減少此問題：

- 關注 BTC、ETH、SOL、XRP 和 DOGE 等主要交易品種；
- 檢測異常的價格和成交量行為；
- 當市場狀況變得危險時暫停交易；
- 僅在足夠多的受監控交易品種恢復後才允許再次交易。

這並不能消除風險，但它使機器人有機會在突然的清算式移動期間停止增加風險。


## 常用方法

### 1. 銷售與 VIP 等級建設

對深度流動性符號使用嚴格的間隔和受控的訂單大小。目標是高週轉率和可預測的執行。費率在這裡很重要，因此請盡可能使用低費用對或回扣計劃。

### 2. 市場回調後的長網格

在有意義的下降後開始，而不是追逐垂直泵。機器人分層買入並反彈賣出。這種風格需要足夠的保證金才能承受更深的回調。

### 3.幣安現貨網格

當您希望機器人買賣實際硬幣而不是開設槓桿期貨部位時，請使用現貨模式。現貨模式只做多：機器人首先購買較低水準的股票，然後在反彈時出售庫存。它比期貨簡單，但仍需要足夠的報價餘額和長期下跌趨勢的計劃。

### 4.庫存退出

如果您已經持有倉位，機器人可以隨著價格上漲幫助逐漸賣出。當部位完全減少時，您可以停止機器人。

### 5.中性網格

當您需要長邊和短邊網格行為時，請使用中性模式。從較小的尺寸開始，觀察交易所在縮放之前如何處理頭寸模式。


## 參數指南

| 設定 | 含義 | 實用建議 |
| --- | --- | --- |
| `symbol` | 交易對 | 從 BTC 或 ETH 等高流動性交易對開始。 |
| `app.market_type` | `futures` 或 `spot` | 預設為 `futures`。現貨實盤交易透過專用適配器支援 Binance、Bitget、Bybit、OKX、Gate 和 Hyperliquid。 |
| `direction` | `long`、`short` 或 `neutral` | 做多網格需要為回撤預留保證金。做空網格不應誤接管無關的手動空頭底倉，除非你明確啟用該行為。 |
| `price_interval` | 網格層之間的價格距離 | 間隔越小，交易越多，手續費也越多。 |
| `order_quantity` | 每筆訂單使用的金額 | 金額越大，成交量和回撤都會放大。確認介面在你的交易所和市場類型下顯示的是報價幣金額還是基礎幣數量。 |
| `min_order_value` | 最小訂單名義價值 | 必須滿足交易所最小下單要求。 |
| `trading.adopt_existing_position` | 機器人是否應把交易所已有倉位接管為機器人庫存 | 預設是 `false`，所以手動 Bitget 底倉不會被當作網格庫存，也不會被網格退出單意外平掉。只有你明確想讓機器人管理已有倉位時才開啟。 |
| `risk_control.enabled` | 市場異常保護 | 除非你非常清楚原因，否則保持開啟。 |


## 網頁控制台

控制台支援 11 種語言：

英語、簡體中文、俄語、韓語、日語、西班牙語、越南語、印地語、葡萄牙語、阿拉伯語和繁體中文。

Web 控制台模式顯示：

- API管理
- 機器人創建和編輯
- 交換標誌
- 即時餘額
- 今天和已實現的總盈虧
- 今日及總交易量
- 運行、暫停和停止機器人狀態


## 手動安裝

```bash
git clone https://github.com/haohaoi34/nexus-trade-bot.git
cd nexus-trade-bot
go mod download
go build -o nexus-trade-bot .
```

啟動 Web 控制台：

```bash
./nexus-trade-bot
```

預設本機 URL：

```text
http://127.0.0.1:8080
```

在伺服器上公開：

```bash
NEXUS_TRADE_BOT_ADDR=0.0.0.0:8080 ./nexus-trade-bot
```

來自來源結帳的單一命令伺服器執行程式：

```bash
chmod +x scripts/nexus-trade-bot.sh
scripts/nexus-trade-bot.sh install
scripts/nexus-trade-bot.sh start
scripts/nexus-trade-bot.sh status
scripts/nexus-trade-bot.sh logs
scripts/nexus-trade-bot.sh stop
```

該運行程序可以從原始碼簽出和發布包中運行。在來源模式下，它建立 `./nexus-trade-bot`；在發布模式下，它直接使用捆綁的二進位。

運行 CLI 工作模式：

```bash
./nexus-trade-bot worker config.yaml
```


## 在即時交易之前

首先檢查這些：

- API key有交易權限，但沒有提現權限。
- 保證金模式正是您所期望的。
- 槓桿不要太激進。
- 此符號具有足夠的流動性。
- 訂單大小符合交易所最低要求。
- 您了解網格可以累積多少位置。
- 您有一個單向市場計劃。
- 您的伺服器防火牆僅在需要時才會公開 Web 連接埠。
- 對於Bitget期貨，先用小倉進行測試，確認機器人方向、部位模式、`trading.adopt_existing_position`是否符合您的要求。


## 免責聲明

期貨交易可能會造成重大損失。網格策略可以在區間波動或復甦的市場中表現良好，但它們也可以在強勁的單向趨勢中累積大量部位。 Nexus-trade-bot 是執行軟體；您負責策略設定、交易所配置、帳戶風險以及透過 API 金鑰進行的每筆交易。
