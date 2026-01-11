package safety

import "nexus-trade-bot/logger"

func recoverWorker(name string) {
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
