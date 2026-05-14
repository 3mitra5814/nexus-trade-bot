package ratelimit

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type Profile struct {
	Exchange    string
	Bucket      string
	DefaultQPS  int
	EnvOverride string
}

func Wait(ctx context.Context, profile Profile) error {
	qps := qpsForProfile(profile)
	if qps <= 0 {
		return nil
	}
	interval := time.Second / time.Duration(qps)
	lockPath := filepath.Join(os.TempDir(), fmt.Sprintf("nexus-trade-bot-%s-%s-rest.lock", sanitize(profile.Exchange), sanitize(profile.Bucket)))
	file, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return fmt.Errorf("创建 %s REST 限速锁失败: %w", profile.Exchange, err)
	}
	defer file.Close()

	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX); err != nil {
		return fmt.Errorf("获取 %s REST 限速锁失败: %w", profile.Exchange, err)
	}
	defer syscall.Flock(int(file.Fd()), syscall.LOCK_UN)

	data, _ := io.ReadAll(file)
	lastUnixNano, _ := strconv.ParseInt(strings.TrimSpace(string(data)), 10, 64)
	if lastUnixNano > 0 {
		wait := time.Duration(lastUnixNano) + interval - time.Duration(time.Now().UnixNano())
		if wait > 0 {
			timer := time.NewTimer(wait)
			select {
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			case <-timer.C:
			}
		}
	}

	now := strconv.FormatInt(time.Now().UnixNano(), 10)
	if _, err := file.Seek(0, 0); err != nil {
		return err
	}
	if err := file.Truncate(0); err != nil {
		return err
	}
	if _, err := file.WriteString(now); err != nil {
		return err
	}
	return nil
}

func QPS(profile Profile) int {
	return qpsForProfile(profile)
}

func qpsForProfile(profile Profile) int {
	keys := []string{profile.EnvOverride}
	exchange := strings.ToUpper(sanitize(profile.Exchange))
	bucket := strings.ToUpper(sanitize(profile.Bucket))
	if exchange != "" && bucket != "" {
		keys = append(keys, "NEXUS_"+exchange+"_"+bucket+"_QPS")
	}
	if exchange != "" {
		keys = append(keys, "NEXUS_"+exchange+"_REST_QPS")
	}
	keys = append(keys, "NEXUS_REST_QPS")

	for _, key := range keys {
		if key == "" {
			continue
		}
		if raw := strings.TrimSpace(os.Getenv(key)); raw != "" {
			if parsed, err := strconv.Atoi(raw); err == nil {
				if parsed <= 0 {
					return 0
				}
				if parsed > 200 {
					return 200
				}
				return parsed
			}
		}
	}
	return profile.DefaultQPS
}

func sanitize(value string) string {
	value = strings.ToUpper(strings.TrimSpace(value))
	replacer := strings.NewReplacer("-", "_", " ", "_", "/", "_")
	value = replacer.Replace(value)
	var b strings.Builder
	for _, r := range value {
		if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' {
			b.WriteRune(r)
		}
	}
	return strings.Trim(b.String(), "_")
}
