// version: 1.2.3

// session implements a simple memory-based session container.
//
//  NOTE: Suggestion is the number of cached elements does not exceed 10w,
//  because a large number of elements for runtime.GC () is a burden.
//  More than 10w can consider memcache, redis ...
//
package session

import (
	"errors"
	"fmt"
	"github.com/chanxuehong/session/list" // this list can be reused Element itself, rather than just Element.Value
	"sync"
	"time"
)

var (
	ErrNotFound  = errors.New("session: item not found")
	ErrNotStored = errors.New("session: item not stored")
)

const (
	// Set below the maximum limit is to prevent int64 overflow, for
	// Expiration = time.Now().Unix() + maxAge
	// This can work under normal circumstances,
	// unless deliberately modified the system time to
	// 292277026536-12-05 02:18:07 +0000 UTC
	maxAgeLimit int64 = 60 * 60 * 24 * 365.2425 * 60 // seconds, 60 years
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
// Principle:
//   1. len(cache) == lruList.Len();
//   2. for Element of lruList, we get cache[Element.Key] == Element;
//   3. in the list lruList and freeList, the younger element
//      is always in front of the older elements;
//

// Storage is safe for concurrent use by multiple goroutines.
type Storage struct {
	mu sync.Mutex

	freeList *list.List
	lruList  *list.List
	cache    map[string]*list.Element

	maxAge              int64 // element of lruList effective time
	capacity            int
	gcIntervalResetChan chan time.Duration
}

// New returns an initialized Storage.
//  maxAge:     seconds; the max age of item in Storage, the maximum is 60 years, and can not be adjusted.
//  capacity:   item within the capacity range will not be physically removed by GC , memory reuse considered.
//  gcInterval: GC interval, the minimal is 1 minute.
func New(maxAge, capacity int, gcInterval time.Duration) *Storage {
	_maxAge := int64(maxAge)
	if _maxAge <= 0 {
		panic(fmt.Sprintf("session.New: maxAge must be > 0 and now == %d", _maxAge))
	} else if _maxAge > maxAgeLimit {
		panic(fmt.Sprintf("session.New: maxAge must be <= %d and now == %d", maxAgeLimit, _maxAge))
	}

	if capacity <= 0 {
		panic(fmt.Sprintf("session.New: capacity must be > 0 and now == %d", capacity))
	}

	if gcInterval < time.Minute {
		panic(fmt.Sprintf("session.New: gcInterval must be >= %d(time.Minute) and now == %d", time.Minute, gcInterval))
	}

	s := &Storage{
		lruList:  list.New(),
		freeList: list.New(),
		cache:    make(map[string]*list.Element, capacity),
		maxAge:   _maxAge,
		capacity: capacity,
	}
	for i := 0; i < capacity; i++ {
		s.freeList.PushFront(&list.Element{})
	}

	// new goroutine for gc service
	go func() {
		gc_interval := gcInterval
		var gcTick *time.Ticker

	NEW_GC_INTERVAL:
		for {
			gcTick = time.NewTicker(gc_interval)
			for {
				select {
				case gc_interval = <-s.gcIntervalResetChan:
					gcTick.Stop()
					s.gc() // call gc() immediately
					break NEW_GC_INTERVAL
				case <-gcTick.C:
					s.gc()
				}
			}
		}
	}()

	return s
}

// Get the item cached in the Storage number.
func (s *Storage) Len() (n int) {
	s.mu.Lock()
	n = s.lruList.Len()
	s.mu.Unlock()
	return
}

// Get the capacity of Storage.
func (s *Storage) Capacity() (n int) {
	s.mu.Lock()
	n = s.capacity
	s.mu.Unlock()
	return
}

// Set the capacity of Storage.
//  NOTE: if capacity <= 0, we do nothing.
func (s *Storage) SetCapacity(capacity int) {
	if capacity > 0 {
		s.mu.Lock()
		s.capacity = capacity
		s.mu.Unlock()
	}
}

// Set the gc interval of Storage.
//  NOTE: if gcInterval < time.Minute, we do nothing;
//  and after calling this method we will soon do a GC, please avoid business peak.
func (s *Storage) SetGCInterval(gcInterval time.Duration) {
	if gcInterval >= time.Minute {
		s.gcIntervalResetChan <- gcInterval
	}
}

// add key-value to Storage.
//  ensure that there is no the same key in Storage
func (s *Storage) add(key string, value interface{}, timenow int64) error {
	var e *list.Element

	// Check the last element of lruList expired; if so, reused it
	if e = s.lruList.Back(); e != nil && timenow > e.Expiration {
		delete(s.cache, e.Key)

		e.Key = key
		e.Value = value
		e.Expiration = timenow + s.maxAge

		s.cache[key] = s.lruList.MoveToFront(e)
		return nil
	}

	// reused the first element of freeList if it not empty
	if s.freeList.Len() > 0 {
		e = s.freeList.RemoveFront()

		e.Key = key
		e.Value = value
		e.Expiration = timenow + s.maxAge

		s.cache[key] = s.lruList.PushFront(e)
		return nil
	}

	// Check whether s.lruList.Len()+s.freeList.Len() has reached the maximum of int.
	// since s.freeList.Len() == 0, so we get
	// s.lruList.Len() == s.lruList.Len()+s.freeList.Len()
	if s.lruList.Len()<<1 == -2 {
		return ErrNotStored
	}

	// now create new
	e = &list.Element{
		Key:        key,
		Value:      value,
		Expiration: timenow + s.maxAge,
	}
	s.cache[key] = s.lruList.PushFront(e)

	return nil
}

// remove Element e from Storage.lruList
//  ensure that e != nil and e is an element of list lruList.
func (s *Storage) remove(e *list.Element) {
	delete(s.cache, e.Key)
	e.Key = ""
	e.Value = nil

	totalLen := s.lruList.Len() + s.freeList.Len() // does not overflow, see Storage.add
	// A purpose of this case
	// s.lruList.Len() < (s.capacity-s.capacity/5)
	// is to prevent jitter
	if totalLen > s.capacity && s.lruList.Len() < (s.capacity-s.capacity/5) {
		s.lruList.Remove(e)
	} else {
		s.freeList.PushFront(s.lruList.Remove(e))
	}
}

// Add key-value to Storage.
//  if there already exists a item with the same key, then return ErrNotStored,
//  otherwise it returns nil.
func (s *Storage) Add(key string, value interface{}) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	timenow := time.Now().Unix()

	if e, hit := s.cache[key]; hit {
		if timenow > e.Expiration {
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

// Set key-value, unconditional
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

// Get the element with key. if there is no such element with the key it returns ErrNotFound
func (s *Storage) Get(key string) (interface{}, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if e, hit := s.cache[key]; hit {
		if timenow := time.Now().Unix(); timenow > e.Expiration {
			s.remove(e)
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

// Delete the element with key. if there is no such element, Delete is a no-op.
func (s *Storage) Delete(key string) (err error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if e, hit := s.cache[key]; hit {
		s.remove(e)
		return
	}
	return
}

func (s *Storage) gc() {
	s.mu.Lock()
	defer s.mu.Unlock()

	timenow := time.Now().Unix()

	for e := s.lruList.Back(); e != nil; e = s.lruList.Back() {
		if timenow > e.Expiration {
			delete(s.cache, e.Key)
			e.Key = ""
			e.Value = nil
			s.freeList.PushFront(s.lruList.Remove(e))
		} else {
			break
		}
	}

	totalLen := s.lruList.Len() + s.freeList.Len() // does not overflow, see Storage.add
	// A purpose of this case
	// s.lruList.Len() < (s.capacity-s.capacity/5)
	// is to prevent jitter
	if totalLen > s.capacity && s.lruList.Len() < (s.capacity-s.capacity/5) {
		// s.freeList.Len() > shouldRemoveNum, why?
		for shouldRemoveNum := totalLen - s.capacity; shouldRemoveNum > 0; shouldRemoveNum-- {
			s.freeList.RemoveFront()
		}
	}
}
