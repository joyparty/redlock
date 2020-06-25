package redlock

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-redis/redis"
	"github.com/stretchr/testify/suite"
)

type LockSuite struct {
	suite.Suite
	client redis.Cmdable
}

func (s *LockSuite) SetupSuite() {
	c := redis.NewClient(&redis.Options{
		Addr:        "127.0.0.1:6379",
		DialTimeout: 2 * time.Second,
	})

	if err := c.Ping().Err(); err != nil {
		s.Fail("redis connect failed")
	}
	s.client = c
}

func (s *LockSuite) TearDownSuite() {
	s.client = nil
}

func (s *LockSuite) TestConflict() {
	key := "test:redlock:conflict"
	s.client.Del(key) // 先清除一次

	mux1, err := NewMutexFromClient(key, 3*time.Second, s.client)
	s.Require().NoError(err)

	s.Require().NoError(mux1.Lock(), "mux1.Lock()")

	mux2, err := NewMutexFromClient(key, 3*time.Second, s.client)
	s.Require().NoError(err)

	err = mux2.Lock()
	s.Require().EqualError(err, ErrLockConflict.Error(), "mux2.Lock()")

	s.Require().NoError(mux1.Unlock(), "mux1.Unlock()")

	s.Require().NoError(mux2.Lock(), "mux2.Lock()")
	s.Require().NoError(mux2.Unlock(), "mux2.Unlock()")
}

func (s *LockSuite) TestExpiration() {
	key := "test:redlock:expiration"
	s.client.Del(key) // 先清除一次

	mux, err := NewMutexFromClient(key, 200*time.Millisecond, s.client)
	s.Require().NoError(err)

	s.Require().NoError(mux.Lock(), "mux.Lock()")

	time.Sleep(100 * time.Millisecond)
	s.Require().NoError(mux.Extend(), "mux.Extend()")

	time.Sleep(300 * time.Millisecond)
	s.Require().EqualError(mux.Extend(), ErrLockExpired.Error(), "mux.Extend()")

	s.Require().EqualError(mux.Unlock(), ErrLockExpired.Error(), "mux.Unlock()")
}

func (s *LockSuite) TestDoOnce() {
	key := "test:redlock:do:once"
	s.client.Del(key) // 先清除一次

	var n int64
	task := func(ctx context.Context) error {
		time.Sleep(1 * time.Second)
		atomic.AddInt64(&n, 1)
		return nil
	}

	go func() {
		mux2, _ := NewMutexFromClient(key, 3*time.Second, s.client)
		time.Sleep(100 * time.Millisecond)
		r2 := mux2.Do(context.Background(), task)
		s.Require().EqualError(r2.LockErr, ErrLockConflict.Error(), "mux2.Do")
	}()

	go func() {
		mux3, _ := NewMutexFromClient(key, 3*time.Second, s.client)
		time.Sleep(200 * time.Millisecond)
		r3 := mux3.Do(context.Background(), task)
		s.Require().EqualError(r3.LockErr, ErrLockConflict.Error(), "mux2.Do")
	}()

	mux1, _ := NewMutexFromClient(key, 3*time.Second, s.client)
	r1 := mux1.Do(context.Background(), task)

	s.Require().Nil(r1.Err())
	s.Require().Equal(int64(1), n, "task run more than once")
}

func (s *LockSuite) TestDoCancel() {
	key := "test:redlock:do:cancel"
	s.client.Del(key) // 先清除一次

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()

	var n int
	mux, _ := NewMutexFromClient(key, 3*time.Second, s.client)
	r := mux.Do(ctx, func(ctx context.Context) error {
		n = 1
		<-ctx.Done()
		return ctx.Err()
	})

	s.Require().Equal(1, n)
	s.Require().EqualError(r.TaskErr, context.Canceled.Error())
	s.Require().NoError(r.LockErr)
}

func (s *LockSuite) TestDoExpired() {
	key := "test:redlock:do:expired"
	s.client.Del(key) // 先清除一次

	go func() {
		time.Sleep(100 * time.Millisecond)
		s.client.Del(key) // del lock
	}()

	mux, _ := NewMutexFromClient(key, 1*time.Second, s.client)
	r := mux.Do(context.Background(), func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	})

	s.Require().EqualError(r.LockErr, ErrLockExpired.Error())
	s.Require().EqualError(r.TaskErr, context.Canceled.Error())
}

func (s *LockSuite) TestDoError() {
	key := "test:redlock:do:error"
	s.client.Del(key) // 先清除一次

	myErr := fmt.Errorf("test do error")
	mux, _ := NewMutexFromClient(key, 1*time.Second, s.client)
	r := mux.Do(context.Background(), func(ctx context.Context) error {
		return myErr
	})

	s.Require().NoError(r.LockErr)
	s.Require().EqualError(r.TaskErr, myErr.Error())
}

func TestLockSuite(t *testing.T) {
	suite.Run(t, &LockSuite{})
}
