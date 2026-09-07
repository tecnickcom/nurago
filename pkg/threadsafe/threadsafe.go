/*
Package threadsafe defines lock interfaces for building reusable, goroutine-safe
data structures and helpers without hard-coding a concrete lock type.

It defines two lock interfaces:

  - [Locker]: the write-lock contract (`Lock` and `Unlock`), a defined type
    sharing the method set of [sync.Locker].
  - [RLocker]: the read-lock contract (`RLock` and `RUnlock`) used by
    read/write synchronization patterns.

These interfaces are embedded or referenced by concurrent containers and utility
types used across multiple goroutines.

# Usage

See the examples in:
  - github.com/tecnickcom/nurago/pkg/threadsafe/tsmap
  - github.com/tecnickcom/nurago/pkg/threadsafe/tsslice

Read helpers in dependent packages accept [RLocker], which a plain [sync.Mutex]
does not satisfy (it has no `RLock`/`RUnlock`). This forces callers that need
shared read access to supply an [sync.RWMutex]-like type at compile time, rather
than serializing every read behind an exclusive lock. A [sync.RWMutex] satisfies
both [Locker] and [RLocker], so the same lock instance can drive read and write
helpers.

These interfaces only describe the locking contract; they do not enforce it.
All access to the protected data must funnel through the helper functions using
the same lock instance, otherwise concurrent reads and writes are not safe.

# When To Use

  - You are writing a goroutine-safe type and want the caller to choose
    sync.Mutex or sync.RWMutex.
  - Read-heavy code should be able to declare that it only needs a shared
    lock.
*/
package threadsafe

import (
	"sync"
)

// Locker defines exclusive lock semantics with Lock and Unlock methods.
type Locker sync.Locker

// RLocker defines shared read-lock semantics with RLock and RUnlock methods.
type RLocker interface {
	RLock()
	RUnlock()
}
