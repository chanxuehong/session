// version: 1.2.2

// session 实现了一个简单的基于内存的 local lru cache(不同于 cache 的地方是容量不封顶).
//
//  NOTE: 建议缓存的对象不要超过 10w, 因为大量对象对于 runtime.GC() 来说是个负担.
//  超过 10w 可以考虑 memcache, redis...
//
// 没有持久化, 没有考虑 key-value 的 value 版本问题.
//
// 整个 session 的过期时间统一设置, 不同的业务用多个 Storage 实例.
//
// Storage 实例是并发安全的, 所以在应用程序里只用一个 Storage 实例就 OK 了.
//
//  注意:
//  不像 memcache, redis 等远程 cache, session 只是一个 local cache(同一个进程空间),
//  所以无需对存入的对象做"序列化", 这就是说如果存入的 value 是个指针类型, 那么读取出来的
//  value 还是这个指针, 而不是指针指向的对象的序列化结果.
package session

//
// 实现原理:
// 下面是数据结构拓扑图, 设置 cache:map[string]*list.Element 是为了
// 把查找时间复杂度从 O(n) 降至 O(1)
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
// item 的生命周期统一设置, 初始化后不能修改. 对于 Add, Set 和 Get 操作都会把相应的 item 移至
// lruList 的 front, 这样操作的结果就是 lruList 的 front 最新, lruList 的 back 最老.
//
// 出于内存复用的考虑, 设置一个 capacity 参数, 内存中至少保存 capacity 个 item, 用来复用.
// Storage.lruList.Len()+Storage.freeList.Len() >= Storage.capacity.
// 对于增加 item, 优先从 lruList 的 back 复用, 不能复用则从 freeList 上不用, 再不能则新增.
// 对于删除 item 或者 Storage.gc(), 分两种情况:
//  1: 总的 item 数 <= capacity, 直接把 item 的内存从 lruList 上摘下来接到 freeList 上;
//  2: 总的 item 数 > capacity,
//   如果 lruList.Len() 小于 capacity 的 80%, 则物理删除(对于 GC 则把总的 item 数截短到 capacity);
//   否则还是把 item 的内存从 lruList 上摘下来接到 freeList 上.
//   这样做的考虑是防止在 capacity 这个点频繁的申请和释放内存.
//
// 对于复用 item 有两种方案, 优先从 freeList 复用, 或者优先从 lruList 复用. 两种方案在内存
// 占用方面是一样的(稳定到 capacity 的情况下, why?). 区别只是复用的性能有稍微的差别,
// 对于 Storage.gc() 频繁调用的情况下, 优先从 freeList 复用的命中率要高些,
// 对于 Storage.gc() 不频繁调用的情况下, 优先从 lruList 复用的命中率要高些.
// 这些性能差别不大, 只是多几个CPU指令而已(if判断). 但是对于 Storage.gc() 性能影响差别就大了,
// 优先复用 lruList 的方案相当于每次都可能做了一个 item 的 gc() 操作, 长期下来需要 gc() 的 item
// 就比较少, 并且对于 Storage.gc() 调用也不需要那么频繁; 而优先复用 freeList 如果 Storage.gc()
// 不频繁调用的话, 命中率会下降, 并且 Storage.gc() 的性能损耗也比较大.
//
// 所以这里采用优先复用 lruList 方案.
//
// freeList 的增加删除都是从头部操作, 这样对 runtime.GC 更加友好.
//
// freeList的作用
// 如果没有 freeList, 则:
// 1 复用的时候会重复 delete(cache, key), 因为 lruList 的 back 有可能是 Delete 得到的
// 2 gc() 每次都会从 lruList 的 back 开始扫描
// 当然 freeList 也不是必须, 也可以用别的算法实现.
