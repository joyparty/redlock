// Package redlock 是根据 https://redis.io/topics/distlock 实现的分布式锁
package redlock

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"time"

	"github.com/go-redis/redis/v8"
)

var (
	// ErrLockConflict 锁定冲突
	ErrLockConflict = fmt.Errorf("lock conflict")
	// ErrLockExpired 锁已过期不存在
	ErrLockExpired = fmt.Errorf("lock not exist or expired")

	defaultClient redis.Cmdable

	unlock = redis.NewScript(`
if redis.call("get", KEYS[1]) == ARGV[1] then
	return redis.call("del", KEYS[1])
else
	return 0
end`)

	extend = redis.NewScript(`
if redis.call("get", KEYS[1]) == ARGV[1] then
	return redis.call("pexpire", KEYS[1], ARGV[2])
else
	return 0
end`)
)

// Result Mutext.Do()返回值类型
type Result struct {
	LockErr error
	TaskErr error
}

// Err 获取错误
func (r Result) Err() error {
	if err := r.TaskErr; err != nil {
		return err
	}
	return r.LockErr
}

// Mutex 锁
type Mutex struct {
	ttl   time.Duration // 锁记录的过期时长
	rc    redis.Cmdable
	name  string
	value []byte // 随机值
}

// NewMutex 新建锁对象
func NewMutex(name string, ttl time.Duration) (*Mutex, error) {
	if defaultClient == nil {
		return nil, errors.New("call SetDefaultClient() set redis client")
	}

	return NewMutexFromClient(name, ttl, defaultClient)
}

// NewMutexFromClient 使用redis客户端创建锁
func NewMutexFromClient(name string, ttl time.Duration, c redis.Cmdable) (*Mutex, error) {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		return nil, fmt.Errorf("generate random value, %w", err)
	}

	return &Mutex{
		ttl:   ttl,
		rc:    c,
		name:  name,
		value: value,
	}, nil
}

// Lock 锁定，失败不会重试
func (mux *Mutex) Lock(ctx context.Context) error {
	ok, err := mux.rc.SetNX(ctx, mux.name, mux.value, mux.ttl).Result()
	if err != nil {
		return err
	} else if !ok {
		return ErrLockConflict
	}
	return nil
}

// Unlock 解除锁定
func (mux *Mutex) Unlock(ctx context.Context) error {
	ok, err := unlock.Run(ctx, mux.rc, []string{mux.name}, mux.value).Bool()
	if err != nil {
		return err
	} else if !ok {
		return ErrLockExpired
	}
	return nil
}

// Extend 延长锁过期时间，继续持有
func (mux *Mutex) Extend(ctx context.Context) error {
	ok, err := extend.Run(ctx, mux.rc, []string{mux.name}, mux.value, mux.ttl.Milliseconds()).Bool()
	if err != nil {
		return err
	} else if !ok {
		return ErrLockExpired
	}
	return nil
}

// Do 锁定后执行
func (mux *Mutex) Do(ctx context.Context, task func(ctx context.Context) error) (result Result) {
	result = Result{}
	if err := mux.Lock(ctx); err != nil {
		result.LockErr = err
		return
	}

	defer func() {
		if result.LockErr == nil {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()

			if err := mux.Unlock(ctx); err != nil {
				result.LockErr = fmt.Errorf("unlock, %w", err)
			}
		}
	}()

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// 按照锁过期时间的一半，定时延长锁过期时间
	go func() {
		tk := time.NewTicker(mux.ttl / 2)
		defer tk.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-tk.C:
				if err := mux.Extend(ctx); err != nil {
					result.LockErr = fmt.Errorf("extend, %w", err)
					cancel()
					return
				}
			}
		}
	}()

	if err := task(ctx); err != nil {
		result.TaskErr = err
		cancel()
		return
	}

	return
}

// SetDefaultClient 设置默认redis客户端
func SetDefaultClient(c redis.Cmdable) {
	if c != nil {
		defaultClient = c
	}
}
