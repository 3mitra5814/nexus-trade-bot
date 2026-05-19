# nexus-trade-bot

<p align="center">
  <img src="../../logo/logo.png" alt="Nexus Trade Bot" width="720">
</p>

**网格机器人控制中心专为那些想要交易量、自动化和风险可见性而无需照管每个订单的交易者而构建。期货为默认模式；主要中心化交易所均支持现货网格。**

[![Go](https://img.shields.io/badge/Go-1.26%2B-00ADD8?logo=go&logoColor=white)](https://go.dev/)
[![License](https://img.shields.io/badge/license-GPL--3.0-green)](../../LICENSE)
[![One Command](https://img.shields.io/badge/install-one%20command-blue)](#一键安装)
[![Languages](https://img.shields.io/badge/languages-11-orange)](#语言)

## 加入用户群

部署问题、交易所接口细节、实盘经验和新版本反馈，放在一个用户群里会更高效。欢迎加入 Nexus Trade Bot 用户群：[https://t.me/nexustradebot8](https://t.me/nexustradebot8)。

## 语言

[English](../../README.md) | 简体中文 | [Русский](README.ru.md) | [한국어](README.ko.md) | [日本語](README.ja.md) | [Español](README.es.md) | [Tiếng Việt](README.vi.md) | [हिन्दी](README.hi.md) | [Português](README.pt.md) | [العربية](README.ar.md) | [繁體中文](README.zh-TW.md)

## 先看这里

如果你是普通用户，建议先用一键安装启动网页控制台，只添加“允许交易、禁止提现”的 API，然后用小资金创建一个测试机器人，确认逻辑和风险都符合预期后再放大。

如果你是开发者，建议从源码构建开始，先看 `config.example.yaml`，运行 `go test ./...`，再根据需要用 worker 模式加载指定配置文件。

这份 README 的阅读顺序是：安装、支持交易所、功能说明、策略例子、参数说明、手动安装、实盘前检查。

## 一键安装

在新的 Ubuntu 服务器上运行：

```bash
wget -O nexus-trade-bot.sh https://raw.githubusercontent.com/haohaoi34/nexus-trade-bot/main/scripts/nexus-trade-bot.sh && chmod +x nexus-trade-bot.sh && ./nexus-trade-bot.sh install && ./nexus-trade-bot.sh start
```

服务器自动运行：

- 安装缺少的 Ubuntu 依赖项。
- 如果服务器没有兼容版本，则安装 Go。
- 当 `https://github.com/haohaoi34/nexus-trade-bot.git` 尚未在源签出内运行时克隆 `https://github.com/haohaoi34/nexus-trade-bot.git`。
- 从源代码构建机器人，或使用发布包中捆绑的二进制文件。
- 如果需要，从 `config.example.yaml` 创建 `config.yaml` 并将其保留在本地。
- 在后台启动 Web 控制台并将日志写入 `logs/`。
- 打印清晰的访问提示块，包含本地 URL、监听地址、PID 文件和日志路径；除非显式绑定公网地址，否则默认不开放远程访问。

有用的服务器命令：

```bash
./nexus-trade-bot.sh install
./nexus-trade-bot.sh start
./nexus-trade-bot.sh status
./nexus-trade-bot.sh logs
./nexus-trade-bot.sh restart
./nexus-trade-bot.sh stop
./nexus-trade-bot.sh update
```

默认网页登录：

```text
username: admin
password: admin
```

首次登录后立即更改默认密码。

## 支持的交易所

- Binance ☑️
- Bitget ☑️
- Gate.io ☑️
- Bybit ☑️
- OKX ☑️
- Hyperliquid ☑️



## 它的作用

nexus-trade-bot 可帮助您从干净的 Web 控制台运行网格策略：

- 添加交易所API一次并验证后再使用。
- 为不同交易对、账户和方向创建多个机器人。
- 选择期货或现货。默认选择期货。
- 合约支持做多、做空或中性模式；现货仅支持做多模式。
- 自动加载 Binance、Bitget、Bybit、OKX、Gate 和 Hyperliquid 现货交易对。
- 实时观察余额、交易量、机器人状态和盈亏。
- 暂停机器人，更改参数，然后使用最新设置重新启动。
- 让风险监控器在市场异常波动时停止交易。

它面向重视执行效率、交易量和风险控制的交易者，而不是让用户整天手动编辑配置文件。

## 核心思想

网格机器人以固定的价格间隔下达买卖订单。它不是试图预测确切的顶部或底部，而是围绕一个价格范围进行工作：

- 当价格下降时，机器人会根据您的网格设置逐渐购买。
- 当价格反弹时，机器人会逐步卖出更高的价格。
- 在横盘整理或向上复苏的市场中，这可能会将波动性转化为重复的已实现交易。
- 在单向下跌趋势中，机器人积累头寸，需要足够的保证金、风险限制和耐心。

目标不是神奇的利润。目标是严格执行：一致的订单间隔、受控的订单规模、可见的风险以及市场异常时的自动反应。


## 策略示例：高交易量的 ETH 网格

这是一个实际示例，用于了解交易者如何使用此类机器人。

假设 ETH 的交易价格接近 `3000`，并且您配置：

| 参数 | 示例 |
| --- | --- |
| 交易对 | `ETHUSDT` 或 `ETHUSDC` |
| 方向 | 做多网格 |
| 价格间隔 | `1 USDT` |
| 下单金额 | 每个网格订单 `300 USDT` |
| 市场风格 | 横盘或向上修复的市场 |

由于 `1 USDT` 间隔较窄且 ETH 流动性活跃，该机器人可能会产生非常高的换手率。在繁忙的市场中，这种配置可以达到数百万美元的日交易量，以及数千万美元的月交易量，具体取决于波动性、费用、流动性和账户规模。

这就是为什么许多交易者使用网格系统有两个目的：

- **交易量建设**：增加交易所 VIP 级别或活动的期货交易量。
- **波动收获**：在一定范围内反复低买高卖。


## 回撤逻辑示例

网格交易必须围绕回撤进行规划。

假设 ETH 从 `3000` 附近开始，然后跌至 `2700`。做多网格通常会承受浮动亏损，因为它会在下跌过程中分层买入；但这些低位买入也会降低平均成本。如果价格随后从 `2700` 反弹至 `2850`，账户可能比单次在 `3000` 买入更早接近盈亏平衡。

如果 ETH 返回接近原始 `3000` 区域，该策略可能会从以下两个方面受益：

- 库存从反弹中恢复；
- 实现了运动期间收集的网格价差。

一些交易者保留了更大的保证金缓冲，例如在 `30,000 USDT` 周围，以设计一个可以容忍更深层次波动的网格，例如 `1000 USDT` ETH 回撤。这是否足够取决于杠杆、保证金模式、头寸规模、费用、交易所维持保证金规则以及您的网格的激进程度。

重要的一点是：网格收益来自准备，而不是乐观。在放大仓位之前，请先计算市场可能向不利方向移动多远、机器人最多会累积多少仓位，以及如果市场迟迟不反弹会发生什么。


## 内置风险保护

快速单边下跌是激进做多网格最不友好的环境。nexus-trade-bot 内置市场风险监控，用来降低这种风险：

- 关注 BTC、ETH、SOL、XRP 和 DOGE 等主要交易品种；
- 检测异常的价格和成交量行为；
- 当市场状况变得危险时暂停交易；
- 仅在足够多的受监控交易品种恢复后才允许再次交易。

这并不能消除风险，但它使机器人有机会在突然的清算式移动期间停止增加风险。


## 常用方法

### 1. 交易量和 VIP 等级建设

对高流动性交易对使用较小间隔和可控订单金额。目标是高换手率和可预期执行。费率在这里很重要，因此应尽量使用低费率交易对或手续费折扣方案。

### 2. 市场回调后的做多网格

在市场出现明显回调后启动，而不是追逐急涨行情。机器人会分层买入，并在反弹中逐步卖出。这种风格需要足够保证金来承受更深回调。

### 3. 币安现货网格

当你希望机器人买卖真实现货资产，而不是开杠杆合约仓位时，请使用现货模式。现货模式仅做多：机器人会先在低位买入，再在反弹中卖出库存。它比合约简单，但仍需要足够的计价币余额，并提前规划长期下跌的情况。

### 4. 库存退出

如果您已经持有仓位，机器人可以随着价格上涨帮助逐渐卖出。当仓位完全减少时，您可以停止机器人。

### 5. 中性网格

当您需要长边和短边网格行为时，请使用中性模式。从较小的尺寸开始，观察交易所在缩放之前如何处理头寸模式。

### 6. 经典网格

经典网格是仅支持合约的中性模式。它不设置上下区间，而是实时维持当前网格价下方 50 个买单、上方 50 个卖单，总计目标 100 个活跃挂单。成交后会自动补单，让买一和卖一尽量保持设定间隔。Hyperliquid 合约目前不支持该模式，因为它不提供所需的中性双向持仓行为。


## 参数指南

| 设置 | 含义 | 实用建议 |
| --- | --- | --- |
| `symbol` | 交易对 | 从 BTC 或 ETH 等高流动性交易对开始。 |
| `app.market_type` | `futures` 或 `spot` | 默认为 `futures`。现货实盘交易通过专用适配器支持 Binance、Bitget、Bybit、OKX、Gate 和 Hyperliquid。 |
| `mode` | `normal`、`aggressive` 或 `classic` | `classic` 为固定 50 买 / 50 卖经典网格，会强制使用合约 + 中性方向，并以 100 个活跃挂单为目标。 |
| `direction` | `long`、`short` 或 `neutral` | 做多网格需要为回撤预留保证金。启动时已有交易所仓位会恢复为机器人库存。 |
| `price_interval` | 网格层之间的价格距离 | 间隔越小，交易越多，手续费也越多。 |
| `order_quantity` | 每笔订单使用的金额 | 金额越大，成交量和回撤都会放大。确认界面在你的交易所和市场类型下显示的是报价币金额还是基础币数量。 |
| `min_order_value` | 最小订单名义价值 | 必须满足交易所最小下单要求。 |
| `risk_control.enabled` | 市场异常保护 | 除非你非常清楚原因，否则保持开启。 |


## 网页控制台

控制台支持 11 种语言：

英语、简体中文、俄语、韩语、日语、西班牙语、越南语、印地语、葡萄牙语、阿拉伯语和繁体中文。

Web 控制台模式显示：

- API管理
- 机器人创建和编辑
- 交易所标识
- 实时余额
- 今日和累计已实现盈亏
- 今日及总交易量
- 运行、暂停和停止机器人状态


## 手动安装

```bash
git clone https://github.com/haohaoi34/nexus-trade-bot.git
cd nexus-trade-bot
go mod download
go build -o nexus-trade-bot .
```

启动 Web 控制台：

```bash
./nexus-trade-bot
```

默认本地 URL：

```text
http://127.0.0.1:8080
```

在服务器上公开：

```bash
NEXUS_TRADE_BOT_ADDR=0.0.0.0:8080 ./nexus-trade-bot
```

在源码目录中使用一键服务器脚本：

```bash
chmod +x scripts/nexus-trade-bot.sh
scripts/nexus-trade-bot.sh install
scripts/nexus-trade-bot.sh start
scripts/nexus-trade-bot.sh status
scripts/nexus-trade-bot.sh logs
scripts/nexus-trade-bot.sh stop
```

该运行程序可以从源代码签出和发布包中运行。在源模式下，它构建 `./nexus-trade-bot`；在发布模式下，它直接使用捆绑的二进制文件。

运行 CLI 工作模式：

```bash
./nexus-trade-bot worker config.yaml
```


## 实时交易之前

首先检查这些：

- API key有交易权限，但没有提现权限。
- 保证金模式正是您所期望的。
- 杠杆不要太激进。
- 交易对具有足够的流动性。
- 订单大小符合交易所最低要求。
- 您了解网格可以累积多少位置。
- 您有一个单向市场计划。
- 您的服务器防火墙仅在需要时才会公开 Web 端口。


## 免责声明

期货交易可能会造成重大损失。网格策略可以在区间波动或复苏的市场中表现良好，但它们也可以在强劲的单向趋势中积累大量头寸。 Nexus-trade-bot 是执行软件；您负责策略设置、交易所配置、账户风险以及通过 API 密钥进行的每笔交易。
