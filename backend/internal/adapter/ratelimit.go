package adapter

import (
	"errors"
	"sync"
	"time"
)

// ErrRateLimitTimeout 限流器获取令牌超时错误。
var ErrRateLimitTimeout = errors.New("rate limiter: acquire timeout")

// RateLimiter 令牌桶限流器，保护 BMC 不被大量并发请求打挂。
type RateLimiter struct {
	mu        sync.Mutex
	tokens    int           // 当前可用令牌数
	maxTokens int           // 最大令牌数（最大并发）
	interval  time.Duration // 令牌补充间隔
	lastFill  time.Time     // 上次补充时间
}

// NewRateLimiter 创建限流器。
// maxConcurrent: 最大并发数
// refillInterval: 令牌补充间隔
func NewRateLimiter(maxConcurrent int, refillInterval time.Duration) *RateLimiter {
	return &RateLimiter{
		tokens:    maxConcurrent,
		maxTokens: maxConcurrent,
		interval:  refillInterval,
		lastFill:  time.Now(),
	}
}

// refill 补充令牌（内部方法，调用前须持有锁）。
func (r *RateLimiter) refill() {
	now := time.Now()
	elapsed := now.Sub(r.lastFill)
	if elapsed >= r.interval {
		newTokens := int(elapsed / r.interval)
		r.tokens += newTokens
		if r.tokens > r.maxTokens {
			r.tokens = r.maxTokens
		}
		r.lastFill = now
	}
}

// Acquire 获取令牌，如果无可用令牌则阻塞等待。
// 返回 true 表示获取成功，false 表示超时放弃。
func (r *RateLimiter) Acquire() bool {
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		r.mu.Lock()
		r.refill()
		if r.tokens > 0 {
			r.tokens--
			r.mu.Unlock()
			return true
		}
		r.mu.Unlock()
		time.Sleep(50 * time.Millisecond)
	}
	return false
}

// Release 释放令牌，归还到令牌桶中。
func (r *RateLimiter) Release() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.tokens < r.maxTokens {
		r.tokens++
	}
}

// Execute 在限流保护下执行函数，自动获取和释放令牌。
func (r *RateLimiter) Execute(fn func() error) error {
	if !r.Acquire() {
		return ErrRateLimitTimeout
	}
	defer r.Release()
	return fn()
}

// defaultLimiter 包级别默认限流器，最大并发 10，令牌补充间隔 100ms。
var defaultLimiter = NewRateLimiter(10, 100*time.Millisecond)

// GetDefaultLimiter 获取默认限流器实例。
func GetDefaultLimiter() *RateLimiter {
	return defaultLimiter
}
