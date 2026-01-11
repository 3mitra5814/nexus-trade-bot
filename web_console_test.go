package main

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"nexus-trade-bot/config"
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
