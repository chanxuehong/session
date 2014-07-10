// session implements a simple memory-based session container.
// @link        https://github.com/chanxuehong/session for the canonical source repository
// @license     https://github.com/chanxuehong/session/blob/master/LICENSE
// @authors     chanxuehong(chanxuehong@gmail.com)

// session implements a simple memory-based session container.
// version: 1.0.0
//
//  NOTE: Suggestion is the number of cached elements does not exceed 100,000,
//  because a large number of elements for runtime.GC() is a burden.
//  More than 100,000 can consider memcache, redis ...
//
package session
