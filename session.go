// session implements a simple memory-based session container.
// @link        https://github.com/chanxuehong/session for the canonical source repository
// @license     https://github.com/chanxuehong/session/blob/master/LICENSE
// @authors     chanxuehong(chanxuehong@gmail.com)

package session

import (
	"errors"
	"fmt"
	"github.com/chanxuehong/session/list"
	"sync"
	"time"
)

var (
	ErrNotFound  = errors.New("item not found")
	ErrNotStored = errors.New("item not stored")
)

const (
	// Set the below maximum limit is to prevent int64 overflow, for
	// Element.Expiration = time.Now().Unix() + Storage.maxAge
	// This can work under normal circumstances,
	// unless deliberately modified the system time to
	// 292277026536-12-05 02:18:07 +0000 UTC
	maxAgeLimit int64 = 60 * 60 * 24 * 365.2425 * 60 // seconds, 60 years
)

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
//   3. in the list lruList, the younger element is always
//      in front of the older elements;
//

// NOTE: Storage is safe for concurrent use by multiple goroutines.
type Storage struct {
	mu sync.Mutex

	lruList *list.List
	cache   map[string]*list.Element

	maxAge              int64 // element of lruList effective time
	gcIntervalResetChan chan time.Duration
}

// New returns an initialized Storage.
//  maxAge:     seconds, the max age of item in Storage; the maximum is 60 years, and can not be adjusted.
//  gcInterval: seconds, GC interval; the minimal is 1 second.
func New(maxAge, gcInterval int) *Storage {
	_maxAge := int64(maxAge)
	if _maxAge <= 0 {
		panic(fmt.Sprintf("maxAge must be > 0 and now == %d", _maxAge))
	} else if _maxAge > maxAgeLimit {
		panic(fmt.Sprintf("maxAge must be <= %d and now == %d", maxAgeLimit, _maxAge))
	}

	if gcInterval <= 0 {
		panic(fmt.Sprintf("gcInterval must be > 0 and now == %d", gcInterval))
	}

	s := &Storage{
		lruList:             list.New(),
		cache:               make(map[string]*list.Element, 64),
		maxAge:              _maxAge,
		gcIntervalResetChan: make(chan time.Duration), // synchronous channel
	}

	// new goroutine for gc service
	go func() {
		_gcInterval := time.Duration(gcInterval) * time.Second

	NEW_GC_INTERVAL:
		ticker := time.NewTicker(_gcInterval)
		for {
			select {
			case _gcInterval = <-s.gcIntervalResetChan:
				ticker.Stop()
				goto NEW_GC_INTERVAL
			case <-ticker.C:
				s.gc()
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

// Set the gc interval of Storage, seconds.
//  NOTE: if gcInterval <= 0, we do nothing;
//  after calling this method we will soon do a gc(), please avoid business peak.
func (s *Storage) SetGCInterval(gcInterval int) {
	if gcInterval > 0 {
		_gcInterval := time.Duration(gcInterval) * time.Second
		s.gcIntervalResetChan <- _gcInterval
		s.gc() // call gc() immediately
	}
}

// add key-value to Storage.
// ensure that there is no the same key in Storage
func (s *Storage) add(key string, value interface{}, timenow int64) (err error) {
	// Check the last element of lruList expired; if so, reused it
	if e := s.lruList.Back(); e != nil && timenow > e.Expiration {
		delete(s.cache, e.Key)

		e.Key = key
		e.Value = value
		e.Expiration = timenow + s.maxAge

		s.cache[key] = s.lruList.MoveToFront(e)
		return
	}

	// Check whether s.lruList.Len() has reached the maximum of int.
	if s.lruList.Len()<<1 == -2 {
		return ErrNotStored
	}

	// now create new
	e := &list.Element{
		Key:        key,
		Value:      value,
		Expiration: timenow + s.maxAge,
	}
	s.cache[key] = s.lruList.PushFront(e)
	return
}

// remove Element e from Storage.lruList.
// ensure that e != nil and e is an element of list lruList.
func (s *Storage) remove(e *list.Element) {
	delete(s.cache, e.Key)
	s.lruList.Remove(e)
}

// Add key-value to Storage.
// if there already exists a item with the same key, it returns ErrNotStored.
func (s *Storage) Add(key string, value interface{}) (err error) {
	timenow := time.Now().Unix()

	s.mu.Lock()
	if e, hit := s.cache[key]; hit {
		if timenow > e.Expiration {
			// e.Key = key
			e.Value = value
			e.Expiration = timenow + s.maxAge
			s.lruList.MoveToFront(e)

			s.mu.Unlock()
			return

		} else {
			err = ErrNotStored

			s.mu.Unlock()
			return
		}

	} else {
		err = s.add(key, value, timenow)

		s.mu.Unlock()
		return
	}
}

// Set key-value, unconditional
func (s *Storage) Set(key string, value interface{}) (err error) {
	timenow := time.Now().Unix()

	s.mu.Lock()
	if e, hit := s.cache[key]; hit {
		// e.Key = key
		e.Value = value
		e.Expiration = timenow + s.maxAge
		s.lruList.MoveToFront(e)

		s.mu.Unlock()
		return

	} else {
		err = s.add(key, value, timenow)

		s.mu.Unlock()
		return
	}
}

// Get the element with key.
// if there is no such element with the key or the element with the key expired
// it returns ErrNotFound.
func (s *Storage) Get(key string) (value interface{}, err error) {
	timenow := time.Now().Unix()

	s.mu.Lock()
	if e, hit := s.cache[key]; hit {
		if timenow > e.Expiration {
			s.remove(e)
			err = ErrNotFound

			s.mu.Unlock()
			return

		} else {
			value = e.Value

			e.Expiration = timenow + s.maxAge
			s.lruList.MoveToFront(e)

			s.mu.Unlock()
			return
		}

	} else {
		err = ErrNotFound

		s.mu.Unlock()
		return
	}
}

// Delete the element with key.
// if there is no such element with the key or the element with the key expired
// it returns ErrNotFound, normally you can ignore this error.
func (s *Storage) Delete(key string) (err error) {
	timenow := time.Now().Unix()

	s.mu.Lock()
	if e, hit := s.cache[key]; hit {
		s.remove(e)
		if timenow > e.Expiration {
			err = ErrNotFound
		}

		s.mu.Unlock()
		return

	} else {
		err = ErrNotFound

		s.mu.Unlock()
		return
	}
}

func (s *Storage) gc() {
	timenow := time.Now().Unix()

	s.mu.Lock()
	for e := s.lruList.Back(); e != nil; e = s.lruList.Back() {
		if timenow > e.Expiration {
			s.remove(e)
		} else {
			break
		}
	}
	s.mu.Unlock()
}
