package main

import (
	"os"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"

	"nexus-trade-bot/logger"
)

const defaultMemoryLimitMiB = 1536

func configureRuntimeGuard() {
	maxProcs := runtime.NumCPU()
	if configured := strings.TrimSpace(os.Getenv("NEXUS_GOMAXPROCS")); configured != "" {
		if parsed, err := strconv.Atoi(configured); err == nil && parsed > 0 {
			maxProcs = parsed
		}
	}
	if maxProcs > 0 {
		runtime.GOMAXPROCS(maxProcs)
	}

	if strings.TrimSpace(os.Getenv("GOMEMLIMIT")) == "" {
		limitMiB := defaultMemoryLimitMiB
		if configured := strings.TrimSpace(os.Getenv("NEXUS_GOMEMLIMIT_MB")); configured != "" {
			if parsed, err := strconv.Atoi(configured); err == nil && parsed >= 256 {
				limitMiB = parsed
			}
		}
		debug.SetMemoryLimit(int64(limitMiB) << 20)
		logger.Info("🧠 运行时内存软上限: %d MiB", limitMiB)
	}
	logger.Info("⚙️ 运行时并发核心: %d", runtime.GOMAXPROCS(0))
}

func recoverAndLog(name string) {
	if r := recover(); r != nil {
		logger.Error("🛡️ [%s] 捕获异常，协程已安全退出: %v", name, r)
	}
}

func runProtected(name string, fn func()) {
	defer func() {
		if r := recover(); r != nil {
			logger.Error("🛡️ [%s] 捕获异常，本轮任务已跳过: %v", name, r)
		}
	}()
	fn()
}
