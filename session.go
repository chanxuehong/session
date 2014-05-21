package session

import (
	"errors"
	"fmt"
	"github.com/chanxuehong/session/list" //这个 list 可以复用 Element 本身, 而不仅仅是 Element.Value
	"sync"
	"time"
)

var (
	ErrNotFound  = errors.New("session: item not found")
	ErrNotStored = errors.New("session: item not stored")
)

const (
	// 设置下面的最大限制主要是为了防止 int64 溢出.
	// Expiration = time.Now().Unix() + maxMaxAge
	// 这样除非是恶意更改系统时间, 否则基本都能正常工作
	maxMaxAge int = 60 * 60 * 24 * 365.2425 * 60 // seconds, 60 years
)

//
//                           front                          back
//                          +------+       +------+       +------+
// freeList:list.List       |empty |------>|empty |------>|empty |
//                          |      |<------|      |<------|      |
//                          +------+       +------+       +------+
//
//                           front                          back
//                          +------+       +------+       +------+
// lruList:list.List        | key  |------>| key  |------>| key  |
//                          | value|<------| value|<------| value|
//                          +------+       +------+       +------+
//                              ^              ^              ^
// cache:                       |              |              |
// map[string]*list.Element     |              |              |
//      +------+------+         |              |              |
//      | key  |value-+---------+              |              |
//      +------+------+                        |              |
//      | key  |value-+------------------------+              |
//      +------+------+                                       |
//      | key  |value-+---------------------------------------+
//      +------+------+
//
// 对于 cache 来说: key == cache[key].key
//

type Storage struct {
	mu sync.Mutex

	freeList *list.List               // 利旧的 list, 在这个链表上的元素都是 cache 里没有的
	lruList  *list.List               // 在用的 list, lruList.Len() == len(cache)
	cache    map[string]*list.Element // key == cache[key].key

	maxAge int64 // seconds; Storage 里的 item 的生命期.

	// 对于过期的 list.Element:
	// 如果 lruList.Len()+freeList.Len() <= capacity, 则不物理删除, 只是移动到 freeList 链表上;
	// 如果 lruList.Len()+freeList.Len() > capacity, 物理删除超过部分的.
	capacity int
}

// 创建并初始化一个 Storage
//  maxAge:     seconds; Storage 里的 item 的生命期, 最大为60年, 不能调整.
//  gcInterval: 垃圾收集的时间间隔, 不能小于 1 分钟, 不能调整.
//  capacity:   容量, 在这个 capacity 内的 item 不会被 gc() 物理清除, 内存复用考虑.
func New(maxAge int, gcInterval time.Duration, capacity int) *Storage {
	if maxAge <= 0 {
		panic(fmt.Sprintf("session.New: maxAge must be > 0 and now == %d", maxAge))
	} else if maxAge > maxMaxAge {
		panic(fmt.Sprintf("session.New: maxAge must be <= %d and now == %d", maxMaxAge, maxAge))
	}
	if gcInterval < time.Minute {
		panic(fmt.Sprintf("session.New: gcInterval must be >= %d(time.Minute) and now == %d", time.Minute, gcInterval))
	}
	if capacity <= 0 {
		panic(fmt.Sprintf("session.New: capacity must be > 0 and now == %d", capacity))
	}

	s := &Storage{
		lruList:  list.New(),
		freeList: list.New(),
		cache:    make(map[string]*list.Element, capacity),
		maxAge:   int64(maxAge),
		capacity: capacity,
	}
	for i := 0; i < capacity; i++ {
		s.freeList.PushFront(&list.Element{})
	}

	// 启用一个 goroutine 来做 gc
	go func() {
		gcTick := time.Tick(gcInterval)
		for {
			select {
			case <-gcTick:
				s.gc()
			}
		}
	}()

	return s
}

// 获取缓存的 item 个数
func (s *Storage) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lruList.Len()
}

// 获取缓存中 item 的物理个数
func (s *Storage) PhysicsLen() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lruList.Len() + s.freeList.Len() // 不会溢出, 详见 Storage.add
}

// 获取缓存的当前 capacity 参数
func (s *Storage) Capacity() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.capacity
}

// 设置新的 capacity, 如果 capacity <= 0 则不做操作
func (s *Storage) SetCapacity(capacity int) {
	if capacity > 0 {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.capacity = capacity
	}
}

// 增加 key-value 到缓存中.
//
// 调用者保证 cache 中没有 "同样" 的 key, 所以可以直接增加.
//
// 优先复用 lruList 上过期的, 如果 lruList 上没有过期则复用 freeList 上的,
// 如果 freeList 为空则新增.
func (s *Storage) add(key string, value interface{}, timenow int64) error {
	var e *list.Element

	if e = s.lruList.Back(); e != nil && timenow > e.Expiration {
		delete(s.cache, e.Key)

		e.Key = key
		e.Value = value
		e.Expiration = timenow + s.maxAge

		s.cache[key] = s.lruList.MoveToFront(e)

		return nil
	}

	if s.freeList.Len() > 0 {
		e = s.freeList.RemoveFront()

		e.Key = key
		e.Value = value
		e.Expiration = timenow + s.maxAge

		s.cache[key] = s.lruList.PushFront(e)

		return nil
	}

	// now s.freeList.Len() == 0
	// 如果当前 s.lruList.Len() 达到了 int 类型的最大值, 则不增加, 返回 ErrNotStored
	// 主要是为了 s.lruList.Len() 和 s.lruList.Len()+s.freeList.Len() 不溢出.
	if s.lruList.Len()<<1 == -2 {
		return ErrNotStored
	}

	e = &list.Element{
		Key:        key,
		Value:      value,
		Expiration: timenow + s.maxAge,
	}
	s.cache[key] = s.lruList.PushFront(e)

	return nil
}

// 从 s.lruList 上移除 e.
//
// 调用者保证 e != nil 并且 e 是 lruList 上的元素.
//
// 如果 s.lruList.Len()+s.freeList.Len() > s.capacity 并且
// s.lruList.Len() < s.capacity*0.8 则物理移除, 否则移至 s.freeList
func (s *Storage) remove(e *list.Element) {
	delete(s.cache, e.Key)
	// e 关联的资源提前交给 runtime GC
	e.Key = ""
	e.Value = nil

	totalLen := s.lruList.Len() + s.freeList.Len() // 不会溢出, 详见 Storage.add
	// s.lruList.Len() < s.capacity*0.8, 用下面的实现不会溢出, 精度相对较好, 最多丢失2个数字精度;
	// 如果转换成 float64 则有可能丢失 800 个数字的精度, 因为 float64 只有 53 位整数.
	if totalLen > s.capacity && s.lruList.Len() < (s.capacity-s.capacity/5) {
		s.lruList.Remove(e)
	} else {
		s.freeList.PushFront(s.lruList.Remove(e))
	}
}

// 增加 key-value 到缓存中.
// 如果已经存在相同的 key 元素则返回 ErrNotStored, 否则返回 nil.
func (s *Storage) Add(key string, value interface{}) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	timenow := time.Now().Unix()

	if e, hit := s.cache[key]; hit {
		if timenow > e.Expiration { // 过期利旧
			//e.Key = key
			e.Value = value
			e.Expiration = timenow + s.maxAge

			s.lruList.MoveToFront(e)
			return nil
		} else {
			return ErrNotStored
		}
	} else {
		return s.add(key, value, timenow)
	}
}

// 设置 key-value 到缓存, 无条件的.
func (s *Storage) Set(key string, value interface{}) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	timenow := time.Now().Unix()

	if e, hit := s.cache[key]; hit {
		//e.Key = key
		e.Value = value
		e.Expiration = timenow + s.maxAge

		s.lruList.MoveToFront(e)
		return nil
	} else {
		return s.add(key, value, timenow)
	}
}

// 读取 key 对应的 value, 如果不存在这样的 key 则返回 ErrNotFound
func (s *Storage) Get(key string) (interface{}, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if e, hit := s.cache[key]; hit {
		if timenow := time.Now().Unix(); timenow > e.Expiration { // 过期了
			s.remove(e) // e != nil
			return nil, ErrNotFound
		} else {
			e.Expiration = timenow + s.maxAge
			s.lruList.MoveToFront(e)
			return e.Value, nil
		}
	} else {
		return nil, ErrNotFound
	}
}

// 删除 key-value, 如果不存在这样的 key 则返回 ErrNotFound
func (s *Storage) Delete(key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if e, hit := s.cache[key]; hit {
		s.remove(e) // e != nil
		return nil
	} else {
		return ErrNotFound
	}
}

// gc 清理过期的 item.
func (s *Storage) gc() {
	s.mu.Lock()
	defer s.mu.Unlock()

	timenow := time.Now().Unix()

	// 扫描 s.lruList, 把过期的 Element 移至 s.freeList
	var e *list.Element
	for e = s.lruList.Back(); e != nil; e = s.lruList.Back() {
		if timenow > e.Expiration {
			delete(s.cache, e.Key)
			// e 关联的资源提前交给 runtime GC
			e.Key = ""
			e.Value = nil
			s.freeList.PushFront(s.lruList.Remove(e))
		} else {
			break
		}
	}

	// 如果总的容量超过了 capacity, 并且 lruList.Len() 在 capacity 的 80% 以内,
	// 则把总容量截短到 capacity. 设计 80% 以内才截短是为了缓冲考虑.
	totalLen := s.lruList.Len() + s.freeList.Len() // 不会溢出, 详见 Storage.add
	// s.lruList.Len() < s.capacity*0.8, 用下面的实现不会溢出, 精度相对较好, 最多丢失2个数字精度;
	// 如果转换成 float64 则有可能丢失 800 个数字的精度, 因为 float64 只有 53 位整数.
	if totalLen > s.capacity && s.lruList.Len() < (s.capacity-s.capacity/5) {
		// s.freeList.Len() > shouldRemoveNum, why?
		for shouldRemoveNum := totalLen - s.capacity; shouldRemoveNum > 0; shouldRemoveNum-- {
			s.freeList.RemoveFront()
		}
	}
}
