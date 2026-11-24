# WON Language Specification: Concurrency Model

> **Specification Version**: Draft 0.1  
> **Status**: `SPECIFIED` (Under Architectural Review)  
> **Module**: Structured Concurrency & Thread Safety  
> **Date**: November 2026

---

## 1. Concurrency Principles

WON's concurrency design is governed by four core tenets:
1. **Zero Global Interpreter Lock (Zero-GIL)**: High-performance, concurrent execution utilizing 100% of available CPU cores across native OS threads and lightweight tasks.
2. **Compile-Time Data-Race Freedom**: In safe WON code, data races are provably impossible at compile time through the ownership and trait system.
3. **Structured Concurrency by Default**: Background tasks are bound strictly to lexical scopes (`scope`). A task **MUST NOT** outlive the scope that created it, completely eliminating dangling thread leaks and orphaned processes.
4. **Explicit Sharing & Synchronization**: Shared mutable memory across threads is impossible without explicit, compiler-verified synchronization primitives (`Mutex`, `RwLock`, `Atomic`).

---

## 2. Structured Concurrency and Task Scopes

In traditional unstructured concurrency (e.g., C `pthread_create`, Go `go func()`), tasks can execute detached in the background, frequently causing hidden memory corruption, use-after-free of stack variables, and difficult-to-trace deadlocks.

WON mandates **Structured Concurrency**:

```won
fn fetch_all(urls: &[str]) -> Result[Vec[str], NetworkError]:
    scope s:
        var handles = Vec::new()
        for url in urls:
            let task = s.spawn:
                download_endpoint(url)
            handles.push(task)

        # All spawned tasks are guaranteed to have completed or cancelled 
        # before the `scope` block exits.
        var results = Vec::new()
        for h in handles:
            results.push(h.join()?)
        Result::Ok(results)
```

### 2.1 The Scope Nursery (`scope s:`)
- A `scope` block creates an execution nursery.
- Tasks spawned within the scope (`s.spawn: ...`) are registered with the nursery.
- When control reaches the end of the `scope` block, the current thread suspends until all registered child tasks finish executing.
- **Safety Invariant**: Because child tasks cannot outlive the parent `scope`, child tasks **MAY borrow immutable references (`&T`) from the enclosing stack frame**, completely avoiding unnecessary heap clones.

### 2.2 Task Failure and Cancellation Propagation
- If any child task in a scope encounters an unhandled error or panic, the nursery triggers cooperative cancellation across all remaining sibling tasks.
- Resources owned by cancelled tasks are unwound and dropped deterministically.
- The parent scope collects the error and returns it to the caller.

---

## 3. Thread Safety: The `Send` and `Sync` Capabilities

Thread safety is enforced at compile time using two marker traits automatically implemented by the compiler:

```
                            Thread Safety Traits
                                      │
              ┌───────────────────────┴───────────────────────┐
              ▼                                               ▼
         Send Trait                                      Sync Trait
   Ownership can be safely                         References (&T) can be safely
  transferred across threads                     shared concurrently across threads
```

### 3.1 The `Send` Trait
- Indicates that ownership of a value can be transferred safely from one task/thread to another.
- Primitives (`i64`, `f64`, `bool`), unique owned collections (`String`, `Vec[T]`), and `Arc[T]` implement `Send`.
- Raw pointers (`*const T`), non-thread-safe reference counters (`Shared[T]`), and local borrow references do **NOT** implement `Send`.

### 3.2 The `Sync` Trait
- Indicates that it is safe for multiple threads to access immutable references (`&T`) to the same value simultaneously.
- A type `T` is `Sync` if and only if `&T` is `Send`.
- Types with interior mutability (such as `Cell[T]`) are **NOT** `Sync`. Synchronized wrappers (`Mutex[T]`, `RwLock[T]`) are `Sync` when `T: Send`.

### 3.3 Task Boundary Enforcement
The `s.spawn:` primitive requires that any value captured by value implement `Send`, and any value captured by reference implement `Sync`:
```won
# Compile-time check on spawn
fn spawn[F: Fn() -> R + Send, R: Send](&self, f: F) -> TaskHandle[R]
```

---

## 4. Communication and Synchronization

WON supports both message-passing and shared-memory concurrency paradigms.

### 4.1 Typed Channels (`Channel[T]`)
Message passing through bounded or unbounded FIFO queues:
```won
import std::sync::channel

fn pipeline():
    let (tx, rx) = channel[i32](capacity: 100)

    scope s:
        s.spawn:
            for i in 0..10:
                tx.send(i).unwrap()
            tx.close()

        s.spawn:
            while let Option::Some(val) = rx.recv():
                println("Received: {val}")
```

### 4.2 Shared Memory: `Mutex[T]` and `RwLock[T]`
When shared state is necessary:
- A `Mutex[T]` encapsulates the protected data.
- The data is accessible only by acquiring an RAII guard (`let guard = lock.lock()`).
- When the guard goes out of scope, the mutex is unlocked automatically, preventing lock leaks.

```won
struct SharedCounter:
    inner: Arc[Mutex[u64]]

impl SharedCounter:
    fn increment(&self):
        var guard = self.inner.lock()
        *guard += 1
        # Mutex unlocked automatically here
```

### 4.3 Atomic Primitives
Lock-free primitives utilizing native hardware atomic instructions:
- `AtomicBool`, `AtomicI32`, `AtomicI64`, `AtomicUsize`.
- Provide sequentially consistent (`SeqCst`), acquire/release (`AcqRel`), and relaxed (`Relaxed`) memory orderings.

---

## 5. Runtime Architecture and Scheduler Requirements

1. **M:N Work-Stealing Scheduler**:
   - The standard WON runtime executes $M$ lightweight tasks across $N$ OS worker threads (where $N$ defaults to the number of logical CPU cores).
   - Each worker thread maintains a local double-ended task queue (deque). Idle threads steal tasks from busy threads, ensuring balanced CPU saturation.
2. **Non-Blocking I/O Integration**:
   - The runtime integrates directly with OS-native event demultiplexers (`epoll` on Linux, `kqueue` on macOS/BSD, `IOCP` on Windows).
   - Network and file descriptors suspend the calling task without blocking the underlying OS thread.
3. **Zero Mandatory Runtime for Standalone CLI**:
   - Programs that do not utilize `scope` or `spawn` link without the multi-threaded task scheduler, producing minimal single-file binaries with near-zero startup overhead.
