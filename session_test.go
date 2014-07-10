// session implements a simple memory-based session container.
// @link        https://github.com/chanxuehong/session for the canonical source repository
// @license     https://github.com/chanxuehong/session/blob/master/LICENSE
// @authors     chanxuehong(chanxuehong@gmail.com)

package session

import (
	"bytes"
	"testing"
	"time"
)

// 转换 Storage 到数组, 要求存储的都是 byte 类型
func convert2array(s *Storage) []byte {
	if len(s.cache) != s.lruList.Len() {
		panic("len(s.cache) != s.lruList.Len()")
	}

	arr := make([]byte, s.lruList.Len())
	for i, e := 0, s.lruList.Front(); e != nil; i, e = i+1, e.Next() {
		if s.cache[e.Key] != e {
			panic("s.cache[e.Key] != e")
		}
		arr[i] = e.Value.(byte)
	}

	return arr
}

// 常规添加
func TestStorageAdd1(t *testing.T) {
	s := New(1, 10)

	if err := s.Add("1", byte(1)); err != nil {
		t.Error(err)
		return
	}
	if err := s.Add("2", byte(2)); err != nil {
		t.Error(err)
		return
	}
	if err := s.Add("3", byte(3)); err != nil {
		t.Error(err)
		return
	}
	if err := s.Add("3", byte(3)); err != nil {
		if err != ErrNotStored {
			t.Error("错误类型应该是 ErrNotStored")
			return
		}
	} else {
		t.Error("添加重复的字段应该出错")
		return
	}
	if err := s.Add("4", byte(4)); err != nil {
		t.Error(err)
		return
	}

	have := convert2array(s)
	want := []byte{4, 3, 2, 1}
	if !bytes.Equal(have, want) {
		t.Error("have:", have, "want:", want)
		return
	}
}

// 过期情况下增加
func TestStorageAdd2(t *testing.T) {
	s := New(1, 10)

	if err := s.Add("1", byte(1)); err != nil {
		t.Error(err)
		return
	}
	if err := s.Add("2", byte(2)); err != nil {
		t.Error(err)
		return
	}
	if err := s.Add("3", byte(3)); err != nil {
		t.Error(err)
		return
	}

	time.Sleep(time.Second * 2)

	if err := s.Add("1", byte(1)); err != nil {
		t.Error(err)
		return
	}
	if err := s.Add("4", byte(4)); err != nil {
		t.Error(err)
		return
	}

	have := convert2array(s)
	want := []byte{4, 1, 3}
	if !bytes.Equal(have, want) {
		t.Error("have:", have, "want:", want)
		return
	}
}

// 常规删除
func TestStorageDelete1(t *testing.T) {
	s := New(1, 10)

	if err := s.Add("1", byte(1)); err != nil {
		t.Error(err)
		return
	}
	if err := s.Add("2", byte(2)); err != nil {
		t.Error(err)
		return
	}
	if err := s.Add("3", byte(3)); err != nil {
		t.Error(err)
		return
	}
	if err := s.Delete("4"); err != nil {
		if err != ErrNotFound {
			t.Error("错误类型应该是 ErrNotFound")
			return
		}
	} else {
		t.Error("添加重复的字段应该出错")
		return
	}
	if err := s.Delete("2"); err != nil {
		t.Error(err)
		return
	}

	have := convert2array(s)
	want := []byte{3, 1}
	if !bytes.Equal(have, want) {
		t.Error("have:", have, "want:", want)
		return
	}
}

// 过期删除
func TestStorageDelete2(t *testing.T) {
	s := New(1, 10)

	if err := s.Add("1", byte(1)); err != nil {
		t.Error(err)
		return
	}
	if err := s.Add("2", byte(2)); err != nil {
		t.Error(err)
		return
	}
	if err := s.Add("3", byte(3)); err != nil {
		t.Error(err)
		return
	}

	time.Sleep(time.Second * 2)

	if err := s.Add("4", byte(4)); err != nil {
		t.Error(err)
		return
	}
	if err := s.Delete("2"); err != nil {
		if err != ErrNotFound {
			t.Error("错误类型应该是 ErrNotFound")
			return
		}
	} else {
		t.Error("添加重复的字段应该出错")
		return
	}

	have := convert2array(s)
	want := []byte{4, 3}
	if !bytes.Equal(have, want) {
		t.Error("have:", have, "want:", want)
		return
	}
}

// 常规获取
func TestStorageGet1(t *testing.T) {
	s := New(1, 10)

	if err := s.Add("1", byte(1)); err != nil {
		t.Error(err)
		return
	}
	if err := s.Add("2", byte(2)); err != nil {
		t.Error(err)
		return
	}
	if err := s.Add("3", byte(3)); err != nil {
		t.Error(err)
		return
	}
	if _, err := s.Get("4"); err != nil {
		if err != ErrNotFound {
			t.Error("错误类型应该是 ErrNotFound")
			return
		}
	} else {
		t.Error("查找没有的字段应该出错")
		return
	}
	if v, err := s.Get("1"); err != nil {
		t.Error(err)
		return
	} else {
		if n := v.(byte); n != 1 {
			t.Error("have:", n, "want:", 1)
			return
		}
	}
	if v, err := s.Get("2"); err != nil {
		t.Error(err)
		return
	} else {
		if n := v.(byte); n != 2 {
			t.Error("have:", n, "want:", 2)
			return
		}
	}

	have := convert2array(s)
	want := []byte{2, 1, 3}
	if !bytes.Equal(have, want) {
		t.Error("have:", have, "want:", want)
		return
	}
}

// 过期获取
func TestStorageGet2(t *testing.T) {
	s := New(1, 10)

	if err := s.Add("1", byte(1)); err != nil {
		t.Error(err)
		return
	}
	if err := s.Add("2", byte(2)); err != nil {
		t.Error(err)
		return
	}

	time.Sleep(time.Second * 2)

	if err := s.Add("3", byte(3)); err != nil {
		t.Error(err)
		return
	}

	if _, err := s.Get("2"); err != nil {
		if err != ErrNotFound {
			t.Error("错误类型应该是 ErrNotFound")
			return
		}
	} else {
		t.Error("查找过期的字段应该出错")
		return
	}
	if v, err := s.Get("3"); err != nil {
		t.Error(err)
		return
	} else {
		if n := v.(byte); n != 3 {
			t.Error("have:", n, "want:", 3)
			return
		}
	}

	have := convert2array(s)
	want := []byte{3}
	if !bytes.Equal(have, want) {
		t.Error("have:", have, "want:", want)
		return
	}
}

// 常规设置
func TestStorageSet1(t *testing.T) {
	s := New(1, 10)

	if err := s.Add("1", byte(1)); err != nil {
		t.Error(err)
		return
	}
	if err := s.Add("2", byte(2)); err != nil {
		t.Error(err)
		return
	}
	if err := s.Add("3", byte(3)); err != nil {
		t.Error(err)
		return
	}
	if err := s.Set("1", byte(11)); err != nil {
		t.Error(err)
		return
	}
	if err := s.Set("4", byte(4)); err != nil {
		t.Error(err)
		return
	}

	have := convert2array(s)
	want := []byte{4, 11, 3, 2}
	if !bytes.Equal(have, want) {
		t.Error("have:", have, "want:", want)
		return
	}
}

// 过期设置
func TestStorageSet2(t *testing.T) {
	s := New(1, 10)

	if err := s.Add("1", byte(1)); err != nil {
		t.Error(err)
		return
	}
	if err := s.Add("2", byte(2)); err != nil {
		t.Error(err)
		return
	}

	time.Sleep(time.Second * 2)

	if err := s.Set("3", byte(3)); err != nil {
		t.Error(err)
		return
	}
	if err := s.Set("2", byte(22)); err != nil {
		t.Error(err)
		return
	}

	have := convert2array(s)
	want := []byte{22, 3}
	if !bytes.Equal(have, want) {
		t.Error("have:", have, "want:", want)
		return
	}
}

func TestStorageGC(t *testing.T) {
	s := New(4, 5)

	if err := s.Add("1", byte(1)); err != nil {
		t.Error(err)
		return
	}
	if err := s.Add("2", byte(2)); err != nil {
		t.Error(err)
		return
	}

	time.Sleep(time.Second * 3)

	if err := s.Add("3", byte(3)); err != nil {
		t.Error(err)
		return
	}
	if err := s.Add("4", byte(4)); err != nil {
		t.Error(err)
		return
	}

	have := convert2array(s)
	want := []byte{4, 3, 2, 1}
	if !bytes.Equal(have, want) {
		t.Error("have:", have, "want:", want)
		return
	}

	time.Sleep(time.Second * 3)

	have = convert2array(s)
	want = []byte{4, 3}
	if !bytes.Equal(have, want) {
		t.Error("have:", have, "want:", want)
		return
	}
}

func TestStorageSetGCInterval(t *testing.T) {
	s := New(4, 100)

	if err := s.Add("1", byte(1)); err != nil {
		t.Error(err)
		return
	}
	if err := s.Add("2", byte(2)); err != nil {
		t.Error(err)
		return
	}

	time.Sleep(time.Second * 5)

	s.SetGCInterval(5)

	have := convert2array(s)
	want := []byte{}
	if !bytes.Equal(have, want) {
		t.Error("have:", have, "want:", want)
		return
	}

	// see TestStorageGC

	if err := s.Add("1", byte(1)); err != nil {
		t.Error(err)
		return
	}
	if err := s.Add("2", byte(2)); err != nil {
		t.Error(err)
		return
	}

	time.Sleep(time.Second * 3)

	if err := s.Add("3", byte(3)); err != nil {
		t.Error(err)
		return
	}
	if err := s.Add("4", byte(4)); err != nil {
		t.Error(err)
		return
	}

	have = convert2array(s)
	want = []byte{4, 3, 2, 1}
	if !bytes.Equal(have, want) {
		t.Error("have:", have, "want:", want)
		return
	}

	time.Sleep(time.Second * 3)

	have = convert2array(s)
	want = []byte{4, 3}
	if !bytes.Equal(have, want) {
		t.Error("have:", have, "want:", want)
		return
	}
}
