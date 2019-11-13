// Package redlock 是根据 https://redis.io/topics/distlock 实现的分布式锁
package redlock

import (
	"context"
	"crypto/rand"
	"time"

	"github.com/go-redis/redis"
	"github.com/pkg/errors"
)

var (
	// DefaultRetryDelay 多次重试直接的默认间隔时间
	DefaultRetryDelay = 100 * time.Millisecond

	defaultClient redis.Cmdable

	unlock = redis.NewScript(`
if redis.call("get", KEYS[1]) == ARGV[1] then
	return redis.call("del", KEYS[1])
else
	return 0
end`)

	extend = redis.NewScript(`
if redis.call("get", KEYS[1]) == ARGV[1] then
	return redis.call("pexpire", KEYS[1], ARGV[1])
else
	return 0
end
`)
)

// Mutex 锁
type Mutex struct {
	RetryDelay time.Duration // 多次重试之间的间隔时间

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
		RetryDelay: DefaultRetryDelay,

		ttl:   ttl,
		rc:    c,
		name:  name,
		value: value,
	}, nil
}

// Lock 锁定，失败不会重试
func (mux Mutex) Lock() (bool, error) {
	ok, err := mux.rc.SetNX(mux.name, mux.value, mux.ttl).Result()
	return ok, errors.Wrapf(err, "lock %q", mux.name)
}

// TryLock 尝试锁定，会反复多次重试，直到超时
func (mux Mutex) TryLock(ctx context.Context) (bool, error) {
	for {
		select {
		case <-ctx.Done():
			return false, errors.WithStack(ctx.Err())
		default:
			ok, err := mux.Lock()
			if err != nil {
				return false, err
			} else if ok {
				return true, nil
			}

			time.Sleep(mux.RetryDelay)
		}
	}
}

// Unlock 解除锁定
func (mux Mutex) Unlock() (bool, error) {
	ok, err := unlock.Run(mux.rc, []string{mux.name}, mux.value).Bool()
	return ok, errors.Wrapf(err, "unlock %q", mux.name)
}

// Extend 延长锁过期时间，继续持有
func (mux Mutex) Extend() (bool, error) {
	ok, err := extend.Run(mux.rc, []string{mux.name}, mux.ttl.Milliseconds()).Bool()
	return ok, errors.Wrapf(err, "extend %q", mux.name)
}

// SetDefaultClient 设置默认redis客户端
func SetDefaultClient(c redis.Cmdable) {
	if c != nil {
		defaultClient = c
	}
}
