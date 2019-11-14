// Package redlock 是根据 https://redis.io/topics/distlock 实现的分布式锁
package redlock

import (
	"crypto/rand"
	"fmt"
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
	return redis.call("pexpire", KEYS[1], ARGV[1])
else
	return 0
end`)
)

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
	ok, err := extend.Run(mux.rc, []string{mux.name}, mux.ttl.Milliseconds()).Bool()
	if err != nil {
		return errors.WithStack(err)
	} else if !ok {
		return errors.WithStack(ErrLockExpired)
	}
	return nil
}

// SetDefaultClient 设置默认redis客户端
func SetDefaultClient(c redis.Cmdable) {
	if c != nil {
		defaultClient = c
	}
}
