# nexus-trade-bot

<p align="center">
  <img src="logo/logo.png" alt="Nexus Trade Bot" width="720">
</p>

**A grid bot control center built for traders who want volume, automation, and risk visibility without babysitting every order. Futures is the default mode; spot grids are supported on major centralized exchanges.**

[![Go](https://img.shields.io/badge/Go-1.26%2B-00ADD8?logo=go&logoColor=white)](https://go.dev/)
[![License](https://img.shields.io/badge/license-MIT-green)](LICENSE)
[![One Command](https://img.shields.io/badge/install-one%20command-blue)](#one-command-install)
[![Languages](https://img.shields.io/badge/languages-11-orange)](#languages)

## Join the User Community

Questions, deployment notes, exchange-specific fixes, and live trading lessons are easier when users are in one place. Join the Nexus Trade Bot user group: [https://t.me/nexustradebot8](https://t.me/nexustradebot8).

## Languages

English | [简体中文](docs/readme/README.zh-CN.md) | [Русский](docs/readme/README.ru.md) | [한국어](docs/readme/README.ko.md) | [日本語](docs/readme/README.ja.md) | [Español](docs/readme/README.es.md) | [Tiếng Việt](docs/readme/README.vi.md) | [हिन्दी](docs/readme/README.hi.md) | [Português](docs/readme/README.pt.md) | [العربية](docs/readme/README.ar.md) | [繁體中文](docs/readme/README.zh-TW.md)


## One-Command Install

Run this on a fresh Ubuntu server:

```bash
wget -O nexus-trade-bot.sh https://raw.githubusercontent.com/haohaoi34/nexus-trade-bot/main/scripts/nexus-trade-bot.sh && chmod +x nexus-trade-bot.sh && ./nexus-trade-bot.sh install && ./nexus-trade-bot.sh start
```

The server runner automatically:

- Installs missing Ubuntu dependencies.
- Installs Go if the server does not have a compatible version.
- Clones `https://github.com/haohaoi34/nexus-trade-bot.git` when it is not already running inside a source checkout.
- Builds the bot from source, or uses the bundled binary in a release package.
- Creates `config.yaml` from `config.example.yaml` if needed and keeps it local.
- Starts the web console in the background and writes logs to `logs/`.
- Detects the server public IP automatically and prints a clear access block with the local URL, server URL, PID file, and log path.

Useful server commands:

```bash
./nexus-trade-bot.sh install
./nexus-trade-bot.sh start
./nexus-trade-bot.sh status
./nexus-trade-bot.sh logs
./nexus-trade-bot.sh restart
./nexus-trade-bot.sh stop
./nexus-trade-bot.sh update
```

Default web login:

```text
username: admin
password: admin
```

Change the default password immediately after your first login.


## Supported Exchanges

| Exchange | Support |
| --- | --- |
| Binance | Futures: stable. Spot: stable. Best for high-liquidity USDT/USDC perpetual and spot grids. |
| Bitget | Futures: stable. Spot: stable. Best for grid trading and fee-rebate volume strategies. |
| Gate.io | Futures: stable. Spot: stable. Useful for multi-exchange diversification. |
| Bybit | Futures: beta. Spot: stable. Test with smaller size first. |
| OKX | Futures: beta. Spot: stable. Requires API Key, Secret Key, and Passphrase. |
| Hyperliquid | Futures: beta. Spot: beta. Uses wallet-based API setup and USDC spot pairs. |

Bitget rebate link: [up to 70% fee rebate, invite code `4n9z`](https://partner.hdmune.cn/bg/3DLRKF).


## What It Does

nexus-trade-bot helps you run grid strategies from a clean web console:

- Add exchange APIs once and verify them before use.
- Create multiple bots for different symbols, accounts, and directions.
- Choose futures or spot. Futures is selected by default.
- Use long, short, or neutral mode on futures; use long mode on spot.
- Load Binance, Bitget, Bybit, OKX, Gate, and Hyperliquid spot symbols automatically.
- Watch balances, trading volume, bot status, and PnL in real time.
- Pause a bot, change parameters, and restart it with the latest settings.
- Let the risk monitor stop trading during abnormal market moves.

It is designed for traders who care about execution, turnover, and control, not for people who want to keep editing config files all day.

## The Core Idea

A grid bot places buy and sell orders at fixed price intervals. Instead of trying to predict the exact top or bottom, it keeps working around a price range:

- When price drops, the bot gradually buys according to your grid settings.
- When price rebounds, the bot sells higher levels step by step.
- In a sideways or upward-recovering market, this can turn volatility into repeated realized trades.
- In a one-way downtrend, the bot accumulates position and needs enough margin, risk limits, and patience.

The goal is not magic profit. The goal is disciplined execution: consistent order spacing, controlled order size, visible risk, and automatic reaction when the market becomes abnormal.


## Example Strategy: ETH Grid With High Turnover

Here is a practical example to understand how traders use this type of bot.

Assume ETH is trading near `3000`, and you configure:

| Parameter | Example |
| --- | --- |
| Symbol | `ETHUSDT` or `ETHUSDC` |
| Direction | Long grid |
| Price interval | `1 USDT` |
| Order amount | `300 USDT` per grid order |
| Market style | Sideways or upward-recovering market |

With a tight `1 USDT` interval and active ETH liquidity, the bot may generate very high turnover. In a busy market, this kind of configuration can reach millions of dollars in daily trading volume, and tens of millions in monthly volume, depending on volatility, fees, liquidity, and account size.

This is why many traders use grid systems for two purposes:

- **Volume building**: increasing futures trading volume for exchange VIP tiers or campaigns.
- **Volatility harvesting**: repeatedly buying lower and selling higher inside a range.


## Example Drawdown Logic

Grid trading must be planned around drawdown.

Suppose ETH starts near `3000` and falls to `2700`. A long grid will usually hold a floating loss because it has bought along the way down. But it has also accumulated lower entries. If price later rebounds from `2700` toward `2850`, the average cost may be pulled down enough that the account approaches breakeven earlier than a single entry at `3000`.

If ETH returns close to the original `3000` area, the strategy may benefit from both:

- inventory recovery from the rebound;
- realized grid spreads collected during the movement.

Some traders reserve a larger margin buffer, for example around `30,000 USDT`, to design a grid that can tolerate a much deeper move such as a `1000 USDT` ETH drawdown. Whether that is enough depends on leverage, margin mode, position size, fees, exchange maintenance margin rules, and how aggressive your grid is.

The important point: grid profit comes from preparation, not optimism. Before running size, calculate how far the market can move against you, how much position the bot can accumulate, and what happens if the market does not rebound quickly.


## Built-In Risk Protection

Fast one-way drops are the worst environment for an aggressive long grid. nexus-trade-bot includes a market risk monitor designed to reduce this problem:

- watches major symbols such as BTC, ETH, SOL, XRP, and DOGE;
- detects abnormal price and volume behavior;
- pauses trading when market conditions become dangerous;
- allows trading again only after enough monitored symbols recover.

This does not remove risk, but it gives the bot a chance to stop adding exposure during sudden liquidation-style moves.


## Common Ways To Use It

### 1. Volume and VIP Tier Building

Use tight intervals and controlled order size on deep-liquidity symbols. The goal is high turnover with predictable execution. Fee rates matter a lot here, so use low-fee pairs or rebate programs where possible.

### 2. Long Grid After a Market Pullback

Start after a meaningful drop instead of chasing a vertical pump. The bot buys in layers and sells into rebounds. This style needs enough margin to survive deeper pullbacks.

### 3. Binance Spot Grid

Use spot mode when you want the bot to buy and sell actual coins instead of opening leveraged futures positions. Spot mode is long-only: the bot buys lower levels first and sells inventory into rebounds. It is simpler than futures, but it still needs enough quote balance and a plan for prolonged downtrends.

### 4. Inventory Exit

If you already hold a position, the bot can help sell it out gradually as price rises. When the position is fully reduced, you can stop the bot.

### 5. Neutral Grid

Use neutral mode when you want both long-side and short-side grid behavior. Start with smaller size and watch how the exchange handles position mode before scaling.


## Parameter Guide

| Setting | What It Means | Practical Tip |
| --- | --- | --- |
| `symbol` | Trading pair | Start with liquid pairs such as BTC or ETH. |
| `app.market_type` | `futures` or `spot` | Defaults to `futures`. Spot live trading supports Binance, Bitget, Bybit, OKX, Gate, and Hyperliquid through dedicated adapters. |
| `direction` | `long`, `short`, or `neutral` | Long grids need margin for drawdowns. Existing exchange positions are restored as bot inventory at startup. |
| `price_interval` | Distance between grid levels | Smaller interval means more trades and more fees. |
| `order_quantity` | Amount used per order | Larger amount increases turnover and drawdown. Confirm whether the UI is showing quote value or base quantity for your exchange and market type. |
| `min_order_value` | Minimum order notional | Must satisfy exchange minimums. |
| `risk_control.enabled` | Market abnormality protection | Keep it enabled unless you know exactly why not. |


## Web Console

The console supports 11 languages:

English, Simplified Chinese, Russian, Korean, Japanese, Spanish, Vietnamese, Hindi, Portuguese, Arabic, and Traditional Chinese.

Web Console mode shows:

- API management
- bot creation and editing
- exchange logos
- real-time balances
- today and total realized PnL
- today and total trading volume
- running, paused, and stopped bot states


## Manual Installation

```bash
git clone https://github.com/haohaoi34/nexus-trade-bot.git
cd nexus-trade-bot
go mod download
go build -o nexus-trade-bot .
```

Start Web Console:

```bash
./nexus-trade-bot
```

Default local URL:

```text
http://127.0.0.1:8080
```

Expose on a server:

```bash
NEXUS_TRADE_BOT_ADDR=0.0.0.0:8080 ./nexus-trade-bot
```

One-command server runner from a source checkout:

```bash
chmod +x scripts/nexus-trade-bot.sh
scripts/nexus-trade-bot.sh install
scripts/nexus-trade-bot.sh start
scripts/nexus-trade-bot.sh status
scripts/nexus-trade-bot.sh logs
scripts/nexus-trade-bot.sh stop
```

The runner works from both a source checkout and a release package. In source mode it builds `./nexus-trade-bot`; in release mode it uses the bundled binary directly.

Run CLI worker mode:

```bash
./nexus-trade-bot worker config.yaml
```


## Before You Trade Live

Check these first:

- API key has trading permission but no withdrawal permission.
- Margin mode is what you expect.
- Leverage is not too aggressive.
- The symbol has enough liquidity.
- Order size meets exchange minimums.
- You understand how much position the grid can accumulate.
- You have a plan for one-way markets.
- Your server firewall exposes the web port only when intended.


## Disclaimer

Futures trading can cause significant losses. Grid strategies can perform well in range-bound or recovering markets, but they can also accumulate large positions during strong one-way trends. nexus-trade-bot is execution software; you are responsible for strategy settings, exchange configuration, account risk, and every trade placed through your API keys.
