package main

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"nexus-trade-bot/config"
	"nexus-trade-bot/exchange"
	"nexus-trade-bot/tradestats"
)

func TestPublicAccountViewRedactsSecrets(t *testing.T) {
	account := &accountProfile{
		ID:       "acc-1",
		Name:     "main",
		Exchange: "bitget",
		Config: config.ExchangeConfig{
			APIKey:     "api",
			SecretKey:  "secret",
			Passphrase: "pass",
			FeeRate:    0.0002,
		},
	}

	view := publicAccountView(account)

	if view.Config.APIKey != secretMask || view.Config.SecretKey != secretMask || view.Config.Passphrase != secretMask {
		t.Fatalf("expected secrets to be masked, got %#v", view.Config)
	}
	if account.Config.APIKey != "api" || account.Config.SecretKey != "secret" || account.Config.Passphrase != "pass" {
		t.Fatalf("public view mutated stored account config: %#v", account.Config)
	}
	if view.Config.FeeRate != account.Config.FeeRate {
		t.Fatalf("fee rate should remain visible")
	}
}

func TestMergeExistingSecretsPreservesMaskedValues(t *testing.T) {
	existing := config.ExchangeConfig{
		APIKey:     "old-api",
		SecretKey:  "old-secret",
		Passphrase: "old-pass",
		FeeRate:    0.0002,
	}
	next := config.ExchangeConfig{
		APIKey:     secretMask,
		SecretKey:  "",
		Passphrase: secretMask,
		FeeRate:    0.0004,
	}

	merged := mergeExistingSecrets(next, existing, true)

	if merged.APIKey != existing.APIKey || merged.SecretKey != existing.SecretKey || merged.Passphrase != existing.Passphrase {
		t.Fatalf("expected masked/blank values to preserve existing secrets, got %#v", merged)
	}
	if merged.FeeRate != next.FeeRate {
		t.Fatalf("non-secret fields should come from payload")
	}
}

func TestNormalizeAccountExchangeConfigDefaultsFeeRate(t *testing.T) {
	cfg := normalizeAccountExchangeConfig(config.ExchangeConfig{
		APIKey:    " api ",
		SecretKey: " secret ",
	})

	if cfg.APIKey != "api" || cfg.SecretKey != "secret" {
		t.Fatalf("expected secrets to be trimmed, got %#v", cfg)
	}
	if cfg.FeeRate != config.DefaultFeeRate {
		t.Fatalf("expected default fee rate %.8f, got %.8f", config.DefaultFeeRate, cfg.FeeRate)
	}
}

func TestApplyAccountConfigNormalizesExchangeName(t *testing.T) {
	cfg := &config.Config{}
	account := &accountProfile{
		ID:       "acc-1",
		Name:     "main",
		Exchange: " Gate.io ",
		Config: config.ExchangeConfig{
			APIKey:    "api",
			SecretKey: "secret",
			FeeRate:   0.0002,
		},
	}

	if err := applyAccountConfigToConfig(cfg, account); err != nil {
		t.Fatalf("applyAccountConfigToConfig() error = %v", err)
	}
	if cfg.App.CurrentExchange != "gate" {
		t.Fatalf("expected exchange gate, got %q", cfg.App.CurrentExchange)
	}
	if _, ok := cfg.Exchanges["gate"]; !ok {
		t.Fatalf("expected normalized gate exchange config, got %#v", cfg.Exchanges)
	}
}

func TestApplyHardcodedRobotDefaultsUsesClassicGridWindow(t *testing.T) {
	cfg := &config.Config{}
	cfg.Trading.Mode = "classic"
	cfg.App.MarketType = "spot"
	cfg.Trading.Direction = "long"
	cfg.Trading.BuyWindowSize = 1
	cfg.Trading.SellWindowSize = 1

	if err := applyHardcodedRobotDefaults(cfg); err != nil {
		t.Fatalf("applyHardcodedRobotDefaults() error = %v", err)
	}

	if cfg.Trading.Mode != "classic" {
		t.Fatalf("expected mode classic, got %q", cfg.Trading.Mode)
	}
	if cfg.App.MarketType != "futures" || cfg.Trading.Direction != "neutral" {
		t.Fatalf("expected classic grid to force futures/neutral, got market=%q direction=%q", cfg.App.MarketType, cfg.Trading.Direction)
	}
	if cfg.Trading.BuyWindowSize != config.ClassicGridWindowSize || cfg.Trading.SellWindowSize != config.ClassicGridWindowSize {
		t.Fatalf("expected classic grid to force 50/50 windows, got buy=%d sell=%d", cfg.Trading.BuyWindowSize, cfg.Trading.SellWindowSize)
	}
}

func TestApplyHardcodedRobotDefaultsRejectsHyperliquidClassic(t *testing.T) {
	cfg := &config.Config{}
	cfg.App.CurrentExchange = "hyperliquid"
	cfg.Trading.Mode = "classic"

	err := applyHardcodedRobotDefaults(cfg)
	if err == nil || !strings.Contains(err.Error(), "Hyperliquid") {
		t.Fatalf("expected Hyperliquid classic grid validation error, got %v", err)
	}
}

func TestMergeStatsMetricKeepsLocalPNLAndPrice(t *testing.T) {
	base := robotMetric{
		CurrentPrice:     1.23,
		UnrealizedPNL:    -0.45,
		TodayRealizedPNL: 1,
		TotalRealizedPNL: 2,
		TodayVolume:      3,
		TotalVolume:      4,
	}
	next := robotMetric{
		CurrentPrice:     1.25,
		UnrealizedPNL:    -0.50,
		TodayRealizedPNL: 5,
		TotalRealizedPNL: 6,
		TodayVolume:      7,
		TotalVolume:      8,
	}

	merged := mergeStatsMetric(base, next)

	if merged.CurrentPrice != next.CurrentPrice || merged.UnrealizedPNL != next.UnrealizedPNL {
		t.Fatalf("expected local price/pnl to refresh from stats snapshot, got %#v", merged)
	}
	if merged.TodayRealizedPNL != next.TodayRealizedPNL || merged.TotalRealizedPNL != next.TotalRealizedPNL {
		t.Fatalf("expected realized pnl to refresh from stats snapshot, got %#v", merged)
	}
	if merged.TodayVolume != next.TodayVolume || merged.TotalVolume != next.TotalVolume {
		t.Fatalf("expected volume to refresh from stats snapshot, got %#v", merged)
	}
}

func TestMergeStatsMetricPreservesExchangePNL(t *testing.T) {
	base := robotMetric{
		UnrealizedPNL:               11,
		TodayRealizedPNL:            12,
		TotalRealizedPNL:            13,
		HasExchangeUnrealizedPNL:    true,
		HasExchangeTodayRealizedPNL: true,
		HasExchangeTotalRealizedPNL: true,
		TodayVolume:                 1,
		TotalVolume:                 2,
	}
	next := robotMetric{
		CurrentPrice:     99,
		UnrealizedPNL:    -1,
		TodayRealizedPNL: -2,
		TotalRealizedPNL: -3,
		TodayVolume:      4,
		TotalVolume:      5,
	}

	merged := mergeStatsMetric(base, next)

	if merged.UnrealizedPNL != 11 || merged.TodayRealizedPNL != 12 || merged.TotalRealizedPNL != 13 {
		t.Fatalf("expected exchange pnl values to survive local stats merge, got %#v", merged)
	}
	if merged.CurrentPrice != 99 || merged.TodayVolume != 4 || merged.TotalVolume != 5 {
		t.Fatalf("expected local price/volume to refresh, got %#v", merged)
	}
}

func TestCachedRobotMetricMergesFreshLocalStats(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "bot.yaml")
	recorder := tradestats.NewRecorder(tradestats.PathForConfig(configPath), 2, 1, 0)
	if err := recorder.RecordTotals(1, 2, 10, 3, -4); err != nil {
		t.Fatalf("record totals: %v", err)
	}
	robot := &robotDefinition{
		ID:         "bot-1",
		AccountID:  "acc-1",
		ConfigPath: configPath,
		UpdatedAt:  time.Now(),
	}
	server := &consoleServer{
		metricCache: map[string]robotMetricCache{
			"bot-1": {
				Value:          robotMetric{Balance: 99, TotalRealizedPNL: 1, TotalVolume: 1},
				RobotUpdatedAt: robot.UpdatedAt,
				ConfigPath:     configPath,
				ExpiresAt:      time.Now().Add(time.Minute),
			},
		},
	}

	got := server.fetchRobotMetric(robot)

	if got.Balance != 99 {
		t.Fatalf("expected cached remote balance to remain, got %#v", got)
	}
	if got.TotalRealizedPNL != 3 || got.UnrealizedPNL != -4 || got.TotalVolume != 30 {
		t.Fatalf("expected fresh local stats to override cached stats, got %#v", got)
	}
}

func TestStoppedRobotMetricUsesLocalStatsWithoutRemoteFetch(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "bot.yaml")
	recorder := tradestats.NewRecorder(tradestats.PathForConfig(configPath), 2, 1, 0)
	if err := recorder.RecordTotals(1, 2, 10, 3, -4); err != nil {
		t.Fatalf("record totals: %v", err)
	}
	robot := &robotDefinition{ID: "bot-1", ConfigPath: configPath, UpdatedAt: time.Now()}
	server := &consoleServer{
		robotsDir:     dir,
		processes:     map[string]*robotProcess{"bot-1": {Status: "stopped", DesiredStatus: "stopped"}},
		metricCache:   make(map[string]robotMetricCache),
		procInspector: func(int) bool { return false },
	}

	got := server.fetchRobotMetric(robot)

	if got.TotalRealizedPNL != 3 || got.UnrealizedPNL != -4 || got.TotalVolume != 30 {
		t.Fatalf("expected stopped robot metric to come from local stats only, got %#v", got)
	}
	if got.Error != "" {
		t.Fatalf("stopped robot metric should not surface remote exchange errors, got %q", got.Error)
	}
}

func TestApplyExchangePositionsMetricPrefersExchangeUnrealizedPNL(t *testing.T) {
	base := robotMetric{UnrealizedPNL: 9, TotalRealizedPNL: 3}
	positions := []*exchange.Position{
		{Symbol: "ETHUSDT", Size: -2, EntryPrice: 100, MarkPrice: 101, UnrealizedPNL: -2.5, HasUnrealizedPNL: true},
		{Symbol: "ETHUSDT", Size: 1, EntryPrice: 90, MarkPrice: 101, UnrealizedPNL: 11, HasUnrealizedPNL: true},
	}

	got := applyExchangePositionsMetric(base, positions, 105)

	if got.ShortPosition != 2 || got.LongPosition != 1 || got.NetPosition != -1 || got.PositionCount != 2 {
		t.Fatalf("unexpected position metrics: %#v", got)
	}
	if got.UnrealizedPNL != 8.5 {
		t.Fatalf("expected exchange unrealized pnl 8.5, got %.8f", got.UnrealizedPNL)
	}
	if got.TotalRealizedPNL != 3 {
		t.Fatalf("expected local realized pnl to remain without exchange realized data, got %.8f", got.TotalRealizedPNL)
	}
}

func TestApplyExchangePositionsMetricDoesNotTreatPositionRealizedAsSummary(t *testing.T) {
	base := robotMetric{TodayRealizedPNL: 7, TotalRealizedPNL: 13}
	positions := []*exchange.Position{
		{Symbol: "ETHUSDT", Size: -2, EntryPrice: 100, RealizedPNL: -1.5, HasRealizedPNL: true},
	}

	got := applyExchangePositionsMetric(base, positions, 99)

	if got.TotalRealizedPNL != 13 || got.TodayRealizedPNL != 7 {
		t.Fatalf("position-level realized pnl should not override robot summary pnl: %#v", got)
	}
	if got.HasExchangeTotalRealizedPNL {
		t.Fatalf("position-level realized pnl must not be marked as exchange summary pnl: %#v", got)
	}
}

func TestApplyExchangePositionsMetricFallsBackToLatestPriceFormula(t *testing.T) {
	base := robotMetric{UnrealizedPNL: 9}
	positions := []*exchange.Position{
		{Symbol: "SOLUSDC", Size: -3, EntryPrice: 86.5},
	}

	got := applyExchangePositionsMetric(base, positions, 86)

	if got.UnrealizedPNL != 1.5 {
		t.Fatalf("expected latest-price fallback pnl 1.5, got %.8f", got.UnrealizedPNL)
	}
}

func TestApplyExchangePNLSummaryOverridesRealizedPNLWhenAvailable(t *testing.T) {
	base := robotMetric{TodayRealizedPNL: 1, TotalRealizedPNL: 2}
	got := applyExchangePNLSummary(base, exchange.PNLSummary{
		TodayRealizedPNL:    3,
		TotalRealizedPNL:    4,
		HasTodayRealizedPNL: true,
		HasTotalRealizedPNL: true,
	})

	if got.TodayRealizedPNL != 3 || got.TotalRealizedPNL != 4 {
		t.Fatalf("expected exchange realized pnl to override local stats, got %#v", got)
	}
}

func TestExchangePNLCacheKeyIncludesMarketType(t *testing.T) {
	futures := &robotDefinition{AccountID: "acc-1", Config: testRobotConfig("ETHUSDT", "futures")}
	spot := &robotDefinition{AccountID: "acc-1", Config: testRobotConfig("ETHUSDT", "spot")}

	futuresKey := exchangePNLCacheKey(futures, "ETHUSDT", "2026-05-19")
	spotKey := exchangePNLCacheKey(spot, "ETHUSDT", "2026-05-19")

	if futuresKey == spotKey {
		t.Fatalf("expected futures and spot pnl cache keys to differ, both were %q", futuresKey)
	}
	if !strings.Contains(futuresKey, "|futures|") || !strings.Contains(spotKey, "|spot|") {
		t.Fatalf("expected cache keys to include market type, got futures=%q spot=%q", futuresKey, spotKey)
	}
}

func TestFriendlyErrorMessageForRestrictedLocation(t *testing.T) {
	got := friendlyErrorMessage(testError("status=451 body=Service unavailable from a restricted location"))
	want := "服务器所在地区被交易所限制访问。请更换服务器地区、配置代理，或改用当前地区可访问的交易所。"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestIsIgnorableCancelErrorForDelete(t *testing.T) {
	cases := []string{
		"清空交易对挂单失败: 查询挂单失败: <APIError> code=-2015, msg=Invalid API-key, IP, or permissions for action",
		"unauthorized",
		"forbidden",
		"permission denied",
		"status=401",
	}
	for _, msg := range cases {
		if !isIgnorableCancelErrorForDelete(testError(msg)) {
			t.Fatalf("expected delete to ignore cancel error: %q", msg)
		}
	}
	if isIgnorableCancelErrorForDelete(testError("network timeout")) {
		t.Fatalf("did not expect random network error to be ignored")
	}
}

func TestCollectArchivedRobotMetricsSkipsActiveRobots(t *testing.T) {
	dir := t.TempDir()
	activeConfigPath := filepath.Join(dir, "active.yaml")
	archivedConfigPath := filepath.Join(dir, "deleted.yaml")

	activeRecorder := tradestats.NewRecorder(tradestats.PathForConfig(activeConfigPath), 2, 1, 0)
	if err := activeRecorder.RecordTotals(10, 10, 2, 99, 0); err != nil {
		t.Fatalf("record active totals: %v", err)
	}
	archivedRecorder := tradestats.NewRecorder(tradestats.PathForConfig(archivedConfigPath), 2, 1, 0)
	if err := archivedRecorder.RecordTotals(1, 2, 10, 3, 0); err != nil {
		t.Fatalf("record archived totals: %v", err)
	}

	server := &consoleServer{
		robotsDir: dir,
		robots: map[string]*robotDefinition{
			"active": {ID: "active", ConfigPath: activeConfigPath},
		},
	}

	metrics := server.collectArchivedRobotMetrics(server.collectRobotConfigPaths())

	if len(metrics) != 1 {
		t.Fatalf("expected one archived metric, got %d: %#v", len(metrics), metrics)
	}
	if metrics[0].TotalRealizedPNL != 3 || metrics[0].TodayRealizedPNL != 3 {
		t.Fatalf("expected archived realized pnl only, got %#v", metrics[0])
	}
	if metrics[0].TotalVolume != 30 || metrics[0].TodayVolume != 30 {
		t.Fatalf("expected archived volume only, got %#v", metrics[0])
	}
}

func TestAutoRobotNameUsesSymbolMarketAndDirection(t *testing.T) {
	cfg := &config.Config{}
	cfg.App.MarketType = "futures"
	cfg.Trading.Symbol = "ETHUSDT"
	cfg.Trading.Direction = "short"

	got := autoRobotName(cfg)
	want := "ETH/USDT · 合约 · 做空"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestStopRobotCancelsPendingAutoRestart(t *testing.T) {
	server := &consoleServer{
		processes: map[string]*robotProcess{
			"bot-1": {
				Status:        "stopped",
				DesiredStatus: "running",
				ExitReason:    "进程异常退出，5s 后自动重启",
			},
		},
	}

	if err := server.stopRobot("bot-1"); err != nil {
		t.Fatalf("stopRobot() error = %v", err)
	}

	proc := server.processes["bot-1"]
	if proc.DesiredStatus != "stopped" || proc.Status != "stopped" {
		t.Fatalf("expected pending restart to be stopped, got status=%q desired=%q", proc.Status, proc.DesiredStatus)
	}
	if proc.StoppedAt.IsZero() || time.Since(proc.StoppedAt) > time.Minute {
		t.Fatalf("expected stopped timestamp to be set, got %v", proc.StoppedAt)
	}
}

func TestStopRobotIgnoresStalePidfileForNonWorkerProcess(t *testing.T) {
	if _, err := exec.LookPath("sleep"); err != nil {
		t.Skip("sleep command not available")
	}
	cmd := exec.Command("sleep", "60")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start sleep: %v", err)
	}
	waited := false
	defer func() {
		if !waited {
			_ = signalProcessGroup(cmd.Process.Pid, syscall.SIGKILL)
			_, _ = cmd.Process.Wait()
		}
	}()

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "bot.yaml")
	server := &consoleServer{
		robotsDir: dir,
		processes: make(map[string]*robotProcess),
		robots: map[string]*robotDefinition{
			"bot-1": {ID: "bot-1", ConfigPath: cfgPath, Config: &config.Config{}},
		},
	}
	if err := os.WriteFile(server.robotPIDPath("bot-1"), []byte(strconv.Itoa(cmd.Process.Pid)+"\n"), 0600); err != nil {
		t.Fatalf("write pidfile: %v", err)
	}

	if err := server.stopRobotWithStatus("bot-1", "paused"); err == nil {
		t.Fatalf("expected stale non-worker pidfile to be ignored")
	}
	if !processAlive(cmd.Process.Pid) {
		t.Fatalf("non-worker process should not be killed")
	}
	_ = signalProcessGroup(cmd.Process.Pid, syscall.SIGKILL)
	_, _ = cmd.Process.Wait()
	waited = true
	if _, ok := server.readRobotPID("bot-1"); ok {
		t.Fatalf("expected stale pidfile to be removed")
	}
}

func TestPidCommandUsesWorkerConfig(t *testing.T) {
	if _, err := exec.LookPath("sleep"); err != nil {
		t.Skip("sleep command not available")
	}
	cmd := exec.Command("sleep", "60")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start sleep: %v", err)
	}
	defer func() {
		if processAlive(cmd.Process.Pid) {
			_ = cmd.Process.Kill()
		}
		_, _ = cmd.Process.Wait()
	}()

	if pidCommandUsesWorkerConfig(cmd.Process.Pid, filepath.Join(t.TempDir(), "bot.yaml")) {
		t.Fatalf("sleep process must not match nexus worker config")
	}
}

func TestWorkerCommandUsesConfigOnlyMatchesDirectWorker(t *testing.T) {
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("get cwd: %v", err)
	}
	relConfig := filepath.Join("web_console_robots", "solusdc-----23cdab46.yaml")
	targetConfig := filepath.Join(cwd, relConfig)

	tests := []struct {
		name string
		args []string
		want bool
	}{
		{
			name: "direct worker executable",
			args: []string{"nexus-trade-bot", "worker", relConfig},
			want: true,
		},
		{
			name: "direct worker executable path",
			args: []string{"./nexus-trade-bot", "worker", relConfig},
			want: true,
		},
		{
			name: "screen wrapper is not the worker process",
			args: []string{"SCREEN", "-dmS", "nexus-solusdc", "./nexus-trade-bot", "worker", relConfig},
			want: false,
		},
		{
			name: "login wrapper is not the worker process",
			args: []string{"login", "-pflq", "hu", "./nexus-trade-bot", "worker", relConfig},
			want: false,
		},
		{
			name: "wrong config",
			args: []string{"nexus-trade-bot", "worker", filepath.Join("web_console_robots", "other.yaml")},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := workerCommandUsesConfig(tt.args, targetConfig); got != tt.want {
				t.Fatalf("workerCommandUsesConfig(%q) = %v, want %v", strings.Join(tt.args, " "), got, tt.want)
			}
		})
	}
}

type clearOpenOrdersExchange struct {
	getCalls    int
	cancelIDs   []int64
	openByRound [][]*exchange.Order
}

func (e *clearOpenOrdersExchange) GetName() string { return "test" }
func (e *clearOpenOrdersExchange) GetOpenOrders(context.Context, string) ([]*exchange.Order, error) {
	if e.getCalls >= len(e.openByRound) {
		e.getCalls++
		return nil, nil
	}
	orders := e.openByRound[e.getCalls]
	e.getCalls++
	return orders, nil
}
func (e *clearOpenOrdersExchange) BatchCancelOrders(_ context.Context, _ string, orderIDs []int64) error {
	e.cancelIDs = append(e.cancelIDs, orderIDs...)
	return nil
}
func (e *clearOpenOrdersExchange) PlaceOrder(context.Context, *exchange.OrderRequest) (*exchange.Order, error) {
	return nil, nil
}
func (e *clearOpenOrdersExchange) BatchPlaceOrders(context.Context, []*exchange.OrderRequest) ([]*exchange.Order, bool) {
	return nil, false
}
func (e *clearOpenOrdersExchange) CancelOrder(context.Context, string, int64) error { return nil }
func (e *clearOpenOrdersExchange) CancelAllOrders(context.Context, string) error    { return nil }
func (e *clearOpenOrdersExchange) GetOrder(context.Context, string, int64) (*exchange.Order, error) {
	return nil, nil
}
func (e *clearOpenOrdersExchange) GetAccount(context.Context) (*exchange.Account, error) {
	return nil, nil
}
func (e *clearOpenOrdersExchange) GetPositions(context.Context, string) ([]*exchange.Position, error) {
	return nil, nil
}
func (e *clearOpenOrdersExchange) GetBalance(context.Context, string) (float64, error) { return 0, nil }
func (e *clearOpenOrdersExchange) StartOrderStream(context.Context, func(interface{})) error {
	return nil
}
func (e *clearOpenOrdersExchange) StopOrderStream() error { return nil }
func (e *clearOpenOrdersExchange) GetLatestPrice(context.Context, string) (float64, error) {
	return 0, nil
}
func (e *clearOpenOrdersExchange) StartPriceStream(context.Context, string, func(price float64)) error {
	return nil
}
func (e *clearOpenOrdersExchange) StartKlineStream(context.Context, []string, string, exchange.CandleUpdateCallback) error {
	return nil
}
func (e *clearOpenOrdersExchange) StopKlineStream() error { return nil }
func (e *clearOpenOrdersExchange) GetHistoricalKlines(context.Context, string, string, int) ([]*exchange.Candle, error) {
	return nil, nil
}
func (e *clearOpenOrdersExchange) GetPriceDecimals() int    { return 2 }
func (e *clearOpenOrdersExchange) GetQuantityDecimals() int { return 4 }
func (e *clearOpenOrdersExchange) GetBaseAsset() string     { return "ETH" }
func (e *clearOpenOrdersExchange) GetQuoteAsset() string    { return "USDT" }

func TestCancelSymbolOpenOrdersUntilClearCancelsEveryOrderRegardlessOfTag(t *testing.T) {
	ex := &clearOpenOrdersExchange{openByRound: [][]*exchange.Order{
		{
			{OrderID: 1, ClientOrderID: "bot_a"},
			{OrderID: 2, ClientOrderID: "manual_order"},
		},
		{
			{OrderID: 3, ClientOrderID: ""},
		},
		nil,
	}}

	if err := cancelSymbolOpenOrdersUntilClear(context.Background(), ex, "ETHUSDT"); err != nil {
		t.Fatalf("cancelSymbolOpenOrdersUntilClear() error = %v", err)
	}
	want := []int64{1, 2, 3}
	if !reflect.DeepEqual(ex.cancelIDs, want) {
		t.Fatalf("expected all order IDs to be canceled, got %v want %v", ex.cancelIDs, want)
	}
	if ex.getCalls != 3 {
		t.Fatalf("expected repeated verification until clear, got %d GetOpenOrders calls", ex.getCalls)
	}
}

func TestApplyAccountConfigToRobotRefreshesSecrets(t *testing.T) {
	robot := &robotDefinition{Config: &config.Config{}}
	robot.Config.Exchanges = map[string]config.ExchangeConfig{
		"bitget": {APIKey: "old", SecretKey: "old-secret", Passphrase: "old-pass"},
	}
	account := &accountProfile{
		Exchange: "okx",
		Config:   config.ExchangeConfig{APIKey: "new", SecretKey: "new-secret", Passphrase: "new-pass", FeeRate: 0.0004},
	}

	if err := applyAccountConfigToRobot(robot, account); err != nil {
		t.Fatalf("applyAccountConfigToRobot() error = %v", err)
	}

	if robot.Config.App.CurrentExchange != "okx" {
		t.Fatalf("expected exchange okx, got %q", robot.Config.App.CurrentExchange)
	}
	got := robot.Config.Exchanges["okx"]
	if got.APIKey != account.Config.APIKey || got.SecretKey != account.Config.SecretKey || got.Passphrase != account.Config.Passphrase {
		t.Fatalf("expected robot config to use latest account secrets, got %#v", got)
	}
	if got.FeeRate != account.Config.FeeRate {
		t.Fatalf("expected robot config to use account fee rate, got %.8f want %.8f", got.FeeRate, account.Config.FeeRate)
	}
}

func TestCreateRobotRejectsSameAccountExchangeMarketSymbolConflict(t *testing.T) {
	server := newTestConsoleServer(t)

	if _, err := server.createRobot(robotPayload{Name: "alpha", AccountID: "acc-1", Config: testRobotConfig("M/USDT", "futures")}); err != nil {
		t.Fatalf("create first robot: %v", err)
	}
	if _, err := server.createRobot(robotPayload{Name: "beta", AccountID: "acc-1", Config: testRobotConfig("MUSDT", "futures")}); err == nil {
		t.Fatalf("expected second robot with same account/exchange/market/symbol to be rejected")
	} else if !strings.Contains(err.Error(), "同一账户/交易所/市场/交易对只能保留一个机器人") {
		t.Fatalf("unexpected conflict error: %v", err)
	}
}

func TestUpdateRobotRejectsSymbolConflict(t *testing.T) {
	server := newTestConsoleServer(t)

	if _, err := server.createRobot(robotPayload{Name: "alpha", AccountID: "acc-1", Config: testRobotConfig("CLUSDT", "futures")}); err != nil {
		t.Fatalf("create first robot: %v", err)
	}
	second, err := server.createRobot(robotPayload{Name: "beta", AccountID: "acc-1", Config: testRobotConfig("MUSDT", "futures")})
	if err != nil {
		t.Fatalf("create second robot: %v", err)
	}

	_, err = server.updateRobot(second.ID, robotPayload{Name: "beta", AccountID: "acc-1", Config: testRobotConfig("CL/USDT", "futures")})
	if err == nil {
		t.Fatalf("expected update into an existing robot scope to be rejected")
	}
	if !strings.Contains(err.Error(), "同一账户/交易所/市场/交易对只能保留一个机器人") {
		t.Fatalf("unexpected conflict error: %v", err)
	}
}

func TestStartRobotRejectsLoadedConflictingRobots(t *testing.T) {
	server := newTestConsoleServer(t)
	server.robots["alpha"] = &robotDefinition{
		ID:         "alpha",
		Name:       "alpha",
		AccountID:  "acc-1",
		ConfigPath: filepath.Join(server.robotsDir, "alpha.yaml"),
		Config:     testRobotConfig("MUSDT", "futures"),
	}
	server.robots["beta"] = &robotDefinition{
		ID:         "beta",
		Name:       "beta",
		AccountID:  "acc-1",
		ConfigPath: filepath.Join(server.robotsDir, "beta.yaml"),
		Config:     testRobotConfig("M/USDT", "futures"),
	}
	server.robots["alpha"].Config.App.CurrentExchange = "bitget"
	server.robots["beta"].Config.App.CurrentExchange = "bitget"

	err := server.startRobot("beta")
	if err == nil {
		t.Fatalf("expected startRobot to reject conflicting loaded robots")
	}
	if !strings.Contains(err.Error(), "同一账户/交易所/市场/交易对只能保留一个机器人") {
		t.Fatalf("unexpected conflict error: %v", err)
	}
}

func TestEnsureRobotOrderTagIsStableAndSafe(t *testing.T) {
	robot := &robotDefinition{ID: "My Robot-123", Config: &config.Config{}}
	ensureRobotOrderTag(robot)
	first := robot.Config.Trading.OrderTag
	ensureRobotOrderTag(robot)
	if first == "" || robot.Config.Trading.OrderTag != first {
		t.Fatalf("expected stable non-empty order tag, got first=%q second=%q", first, robot.Config.Trading.OrderTag)
	}
	if len(first) > 6 || first != strings.ToLower(first) {
		t.Fatalf("expected compact lowercase order tag, got %q", first)
	}
}

func TestPublicConfigDeepCopiesSlices(t *testing.T) {
	cfg := &config.Config{}
	cfg.RiskControl.MonitorSymbols = []string{"BTCUSDT"}
	public := publicConfig(cfg)
	public.RiskControl.MonitorSymbols[0] = "ETHUSDT"
	if cfg.RiskControl.MonitorSymbols[0] != "BTCUSDT" {
		t.Fatalf("public config mutated source monitor symbols: %#v", cfg.RiskControl.MonitorSymbols)
	}
}

func TestCollectAccountsReturnsCopies(t *testing.T) {
	server := &consoleServer{
		accounts: map[string]*accountProfile{
			"acc-1": {
				ID:       "acc-1",
				Name:     "main",
				Exchange: "bitget",
				Config:   config.ExchangeConfig{APIKey: "api", SecretKey: "secret"},
			},
		},
	}

	accounts := server.collectAccounts()
	if len(accounts) != 1 {
		t.Fatalf("expected one account, got %d", len(accounts))
	}
	accounts[0].Name = "mutated"
	accounts[0].Config.APIKey = "mutated"

	stored := server.accounts["acc-1"]
	if stored.Name != "main" || stored.Config.APIKey != "api" {
		t.Fatalf("collectAccounts leaked mutable account pointer: %#v", stored)
	}
}

func TestApplyAccountToRobotConfigUsesAccountSnapshot(t *testing.T) {
	server := &consoleServer{
		accounts: map[string]*accountProfile{
			"acc-1": {
				ID:       "acc-1",
				Name:     "main",
				Exchange: "bitget",
				Config:   config.ExchangeConfig{APIKey: "api", SecretKey: "secret"},
			},
		},
	}
	cfg := &config.Config{}

	account, err := server.applyAccountToRobotConfig("acc-1", cfg)
	if err != nil {
		t.Fatalf("applyAccountToRobotConfig() error = %v", err)
	}
	account.Config.APIKey = "mutated"

	if server.accounts["acc-1"].Config.APIKey != "api" {
		t.Fatalf("applyAccountToRobotConfig leaked mutable account pointer: %#v", server.accounts["acc-1"])
	}
	if cfg.App.CurrentExchange != "bitget" || cfg.Exchanges["bitget"].APIKey != "api" {
		t.Fatalf("expected robot config to receive account credentials, got %#v", cfg)
	}
}

func TestLoadPersistedStoresSkipNilItems(t *testing.T) {
	dir := t.TempDir()
	server := &consoleServer{
		accountsPath: filepath.Join(dir, "accounts.json"),
		robotsPath:   filepath.Join(dir, "robots.json"),
		accounts:     make(map[string]*accountProfile),
		robots:       make(map[string]*robotDefinition),
	}
	accountsRaw, err := json.Marshal([]*accountProfile{
		nil,
		{ID: "", Name: "blank"},
		{ID: "acc-1", Name: "main", Config: config.ExchangeConfig{APIKey: " api ", SecretKey: " secret "}},
	})
	if err != nil {
		t.Fatalf("marshal accounts: %v", err)
	}
	if err := os.WriteFile(server.accountsPath, accountsRaw, 0600); err != nil {
		t.Fatalf("write accounts: %v", err)
	}
	robotsRaw, err := json.Marshal([]*robotDefinition{
		nil,
		{ID: ""},
		{ID: "bot-1", Name: "bot", Config: &config.Config{}},
	})
	if err != nil {
		t.Fatalf("marshal robots: %v", err)
	}
	if err := os.WriteFile(server.robotsPath, robotsRaw, 0600); err != nil {
		t.Fatalf("write robots: %v", err)
	}

	if err := server.loadAccounts(); err != nil {
		t.Fatalf("loadAccounts() error = %v", err)
	}
	if err := server.loadRobots(); err != nil {
		t.Fatalf("loadRobots() error = %v", err)
	}

	if len(server.accounts) != 1 || server.accounts["acc-1"] == nil {
		t.Fatalf("expected only valid account to load, got %#v", server.accounts)
	}
	if len(server.robots) != 1 || server.robots["bot-1"] == nil {
		t.Fatalf("expected only valid robot to load, got %#v", server.robots)
	}
}

func TestCurrentTimezoneLockedCanBeUsedWhileWriteLocked(t *testing.T) {
	server := &consoleServer{settings: consoleSettings{Timezone: "Asia/Shanghai"}}

	server.mu.Lock()
	got := server.currentTimezoneLocked()
	server.mu.Unlock()

	if got != "Asia/Shanghai" {
		t.Fatalf("expected locked timezone lookup to use settings value, got %q", got)
	}
}

func TestBuildRobotViewRefreshesDeadRunningProcess(t *testing.T) {
	server := &consoleServer{
		robotsDir: t.TempDir(),
		accounts:  make(map[string]*accountProfile),
		processes: map[string]*robotProcess{
			"bot-1": {Status: "running", DesiredStatus: "running", PID: 99999999},
		},
	}
	robot := &robotDefinition{ID: "bot-1", Config: &config.Config{}}

	view := server.buildRobotView(robot)

	if view.Status != "stopped" {
		t.Fatalf("expected dead running process to refresh as stopped, got %#v", view)
	}
}

type testError string

func (e testError) Error() string {
	return string(e)
}

func newTestConsoleServer(t *testing.T) *consoleServer {
	t.Helper()
	dir := t.TempDir()
	return &consoleServer{
		robotsDir:    dir,
		robotsPath:   filepath.Join(dir, "robots.json"),
		accountsPath: filepath.Join(dir, "accounts.json"),
		robots:       make(map[string]*robotDefinition),
		processes:    make(map[string]*robotProcess),
		accounts: map[string]*accountProfile{
			"acc-1": {
				ID:       "acc-1",
				Name:     "main",
				Exchange: "bitget",
				Config:   config.ExchangeConfig{APIKey: "api", SecretKey: "secret", Passphrase: "pass", FeeRate: 0.0002},
			},
		},
	}
}

func testRobotConfig(symbol string, marketType string) *config.Config {
	cfg := &config.Config{}
	cfg.App.CurrentExchange = "bitget"
	cfg.App.MarketType = marketType
	cfg.Exchanges = map[string]config.ExchangeConfig{
		"bitget": {APIKey: "api", SecretKey: "secret", Passphrase: "pass", FeeRate: 0.0002},
	}
	cfg.Trading.Direction = "short"
	cfg.Trading.Symbol = symbol
	cfg.Trading.PriceInterval = 0.05
	cfg.Trading.OrderQuantity = 30
	cfg.Trading.BuyWindowSize = 5
	cfg.Trading.SellWindowSize = 5
	return cfg
}
