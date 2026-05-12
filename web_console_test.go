package main

import (
	"context"
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

func TestFriendlyErrorMessageForRestrictedLocation(t *testing.T) {
	got := friendlyErrorMessage(testError("status=451 body=Service unavailable from a restricted location"))
	want := "服务器所在地区被交易所限制访问。请更换服务器地区、配置代理，或改用当前地区可访问的交易所。"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
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

func TestStopRobotKillsPidfileWorker(t *testing.T) {
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
	server := &consoleServer{
		robotsDir: dir,
		processes: make(map[string]*robotProcess),
	}
	if err := os.WriteFile(server.robotPIDPath("bot-1"), []byte(strconv.Itoa(cmd.Process.Pid)+"\n"), 0600); err != nil {
		t.Fatalf("write pidfile: %v", err)
	}

	if err := server.stopRobotWithStatus("bot-1", "paused"); err != nil {
		t.Fatalf("stopRobotWithStatus() error = %v", err)
	}
	proc := server.processes["bot-1"]
	if proc == nil || proc.Status != "paused" || proc.DesiredStatus != "paused" {
		t.Fatalf("expected adopted pidfile worker to be paused, got %#v", proc)
	}
	if err := cmd.Wait(); err == nil {
		t.Fatalf("expected pidfile worker to be killed, got clean exit")
	}
	waited = true
	if _, ok := server.readRobotPID("bot-1"); ok {
		t.Fatalf("expected pidfile to be removed")
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

type testError string

func (e testError) Error() string {
	return string(e)
}
