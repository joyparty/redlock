// Package redlock 是根据 https://redis.io/topics/distlock 实现的分布式锁
package redlock

import (
	"context"
	"crypto/rand"
	"fmt"
	"sync"
	"time"

	"github.com/go-redis/redis"
	"github.com/pkg/errors"
)

var (
	// ErrLockConflict 锁定冲突
	ErrLockConflict = fmt.Errorf("lock conflict")
	// ErrLockExpired 锁已过期不存在
	ErrLockExpired = fmt.Errorf("lock expired")

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

// Success 任务是否完全成功
func (r Result) Success() bool {
	return r.LockErr == nil && r.TaskErr == nil
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
		return nil, errors.New("use SetDefaultClient() set redis client")
	}

	return NewMutexFromClient(name, ttl, defaultClient)
}

// NewMutexFromClient 使用redis客户端创建锁
func NewMutexFromClient(name string, ttl time.Duration, c redis.Cmdable) (*Mutex, error) {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		return nil, errors.WithStack(err)
	}

	return &Mutex{
		ttl:   ttl,
		rc:    c,
		name:  name,
		value: value,
	}, nil
}

// Lock 锁定，失败不会重试
func (mux *Mutex) Lock() error {
	ok, err := mux.rc.SetNX(mux.name, mux.value, mux.ttl).Result()
	if err != nil {
		return errors.WithStack(err)
	} else if !ok {
		return errors.WithStack(ErrLockConflict)
	}
	return nil
}

// Unlock 解除锁定
func (mux *Mutex) Unlock() error {
	ok, err := unlock.Run(mux.rc, []string{mux.name}, mux.value).Bool()
	if err != nil {
		return errors.WithStack(err)
	} else if !ok {
		return errors.WithStack(ErrLockExpired)
	}
	return nil
}

// Extend 延长锁过期时间，继续持有
func (mux *Mutex) Extend() error {
	ok, err := extend.Run(mux.rc, []string{mux.name}, mux.value, mux.ttl.Milliseconds()).Bool()
	if err != nil {
		return errors.WithStack(err)
	} else if !ok {
		return errors.WithStack(ErrLockExpired)
	}
	return nil
}

// Do 锁定后执行
func (mux *Mutex) Do(ctx context.Context, task func(ctx context.Context) error) (result Result) {
	result = Result{}
	if err := mux.Lock(); err != nil {
		result.LockErr = err
		return
	}

	defer func() {
		if result.LockErr == nil {
			result.LockErr = mux.Unlock()
		}
	}()

	var cancelOnce sync.Once
	ctx, cancel := context.WithCancel(ctx)
	defer cancelOnce.Do(cancel)

	// 按照锁过期时间的一半，定时延长锁过期时间
	go func() {
		tk := time.NewTicker(mux.ttl / 2)
		defer tk.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-tk.C:
				if err := mux.Extend(); err != nil {
					result.LockErr = err
					cancelOnce.Do(cancel)
					return
				}
			}
		}
	}()

	if err := task(ctx); err != nil {
		result.TaskErr = err
		cancelOnce.Do(cancel)
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
