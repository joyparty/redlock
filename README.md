# RedLock

根据 https://redis.io/topics/distlock 实现的分布式锁

没有实现多redis节点的增强版本，仅仅实现了单redis节点的方式

## API

创建锁
```golang
import (
	"github.com/go-redis/redis"
	"github.com/joyparty/redlock"
)

name := "..." // 锁名字，每把锁应该有唯一的名字，名字相同的锁会互斥
ttl := 10 * time.Second	// 锁记录过期时间

var client redis.Cmdable
// 模块配置，指定默认redis客户端
redlock.SetDefaultClient(client)
// 使用默认redis客户端创建锁
mux, err := redlock.NewMutex(name, ttl)

// 使用自己指定的redis客户端创建锁
mux, err := redlock.NewMutexFromClient(name, ttl, client)
```

锁定
```golang
if err := mux.Lock(); errors.Is(err, redlock.ErrLockConflict) {
	fmt.Println("已经被锁定了")
}
```

解锁
```golang
if err := mux.Unlock(); errors.Is(err, redlock.ErrLockExpired) {
	fmt.Println("锁记录过期或不存在")
}
```

延长锁的过期时间，延长的时间使用创建锁的ttl参数值
```golang
if err := mux.Extend(); errors.Is(err, redlock.ErrLockExpired) {
	fmt.Println("锁记录过期或不存在")
}
```

锁定并执行
```golang
var task func(ctx context.Context) error

if err := mux.Do(context.TODO(), task).Err(); err != nil {
	if errors.Is(err, redlock.ErrLockConflict) {
		fmt.Println("锁定失败")
	} else if errors.Is(err, redlock.ErrLockExpired) {
		fmt.Println("锁记录过期或不存在")
	} else if errors.Is(err, context.Canceled) {
		fmt.Println("context canceled")
	} else {
		fmt.Printf("其它错误 %s\n", err)
	}
}
```

## 使用说明

### TTL参数

TTL参数设置短了，有可能任务还没有执行完毕，锁记录就过期了。但是设置过长的值，任务异常退出没有正确的释放锁，导致其它的实例也被锁定在外面。

所以，TTL的取值会基于两方面的考虑，任务正常情况下执行的最长时间，假设锁没有得到正常的释放，希望在多短的时间内其它的实例能够接替继续执行，TTL应该在这两个时间之间取值。

### 延时

实际使用中，有时候很难确定一个任务会跑多长时间，预期是一回事，实际是另外一回事。这就导致预估的TTL值实际上不好用。

应对的方式就是在任务执行过程中定期给锁记录延长过期时间，只要任务还在执行就会一直延长，直到任务执行完毕后释放锁。

### 锁定并执行

任务边执行边延长锁记录是一个看起来简单但烦琐的事情，需要处理以下情况：

- 任务执行过程中，周期性的延时
- 任务执行完毕后，中断延时并释放锁
- 延时失败时，中断任务执行
- 外部传递进来的context cancel时，中断延时和任务

因此`redlock`提供了`Do`方法封装了这部分复杂性，调用方只需要编写自己的业务逻辑即可。

`Do`的延时周期是`ttl`参数的一半，假设设置的`ttl`为10秒，每5秒就会给锁记录延时10秒。

任务的中断是由[context](https://golang.org/pkg/context)包来实现的，调用方有责任处理传递进来的`ctx` cancel行为，否则有可能导致锁已经异常过期了，任务还在继续执行的情况。
