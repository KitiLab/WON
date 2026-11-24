# WON Language Specification: Concurrency Model

> **Specification Version**: Draft 0.1  
> **Status**: `SPECIFIED` (Under Architectural Review)  
> **Module**: Structured Concurrency & Thread Safety  
> **Date**: November 2026

---

## 1. Concurrency Principles

WON's concurrency design is governed by four core tenets:
1. **Zero Global Interpreter Lock (Zero-GIL)**: High-performance, concurrent execution enabling multi-core hardware scaling across native OS threads and lightweight tasks without a Global Interpreter Lock.
2. **Compile-Time Data-Race Freedom**: In safe WON code, data races are provably impossible at compile time through the ownership and trait system.
3. **Structured Concurrency by Default**: Background tasks are bound strictly to lexical scopes (`scope`). A task **MUST NOT** outlive the scope that created it, completely eliminating dangling thread leaks and orphaned processes.
4. **Explicit Sharing & Synchronization**: Shared mutable memory across threads is impossible without explicit, compiler-verified synchronization primitives (`Mutex`, `RwLock`, `Atomic`).

---

## 2. Structured Concurrency and Task Scopes

In traditional unstructured concurrency (e.g., C `pthread_create`, Go `go func()`), tasks can execute detached in the background, frequently causing hidden memory corruption, use-after-free of stack variables, and difficult-to-trace deadlocks.

WON mandates **Structured Concurrency**:

```won
fn fetch_all(urls: &[String]) -> Result[Vec[String], NetworkError]:
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
- Tasks spawned within the scope (`s.spawn: ...`) return a `TaskHandle[T]` where $T$ is the type of the trailing expression in the task body.
- When control reaches the end of the `scope` block, the current thread suspends until all registered child tasks finish executing, return, or enter a terminal cancelled state.
- **Borrowed Stack Capture Invariant**: Because child tasks cannot outlive the parent `scope`, child tasks **MAY borrow immutable references (`&T`) from the enclosing stack frame**, provided $T$ implements `Sync`. Borrowing `&mut T` across task boundaries is strictly rejected (`WON-CON-001`).

### 2.2 Task Handles, Cancellation, and Error Aggregation
1. **Task Handles (`TaskHandle[T]`)**:
   - `.join() -> Result[T, TaskError]`: Suspends until the specific child task finishes and yields its result.
   - `.cancel()`: Sends a cooperative cancellation signal to the task.
   - `.is_finished() -> bool`: Non-blocking check for task completion.
2. **Cooperative Cancellation**:
   - Cancellation in WON is cooperative. When a scope or task is cancelled, a cancellation token is set. Tasks periodically inspect cancellation state (via `s.is_cancelled()` or automatically during standard non-blocking I/O operations).
   - If a child task blocks indefinitely inside an uncooperative CPU loop or a foreign C blocking call, it cannot be forcibly terminated (preventing memory corruption); it runs until it yields, exits, or the parent process terminates.
3. **Multiple Child Errors**:
   - If multiple child tasks fail concurrently within a scope, the nursery aggregates all failures into an `AggregateTaskError(Vec[TaskError])`, ensuring no failure is silently masked.

### 2.3 Foreign C Blocking Calls
Standard WON I/O operations are non-blocking and integrated with OS event loops (`epoll`, `kqueue`). However, when calling a foreign C library that performs a synchronous blocking syscall (e.g., blocking `read()` on a raw socket), the underlying OS thread is occupied. The runtime adapts by expanding its OS worker thread pool up to a configurable maximum to prevent starving remaining runnable tasks.

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
# Canonical signature:
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
- **Closure Semantics**: When a channel is closed via `tx.close()`, all items remaining in the buffer can still be drained by `rx.recv()`. Once empty and closed, `rx.recv()` returns `Option::None`. Attempting to send on a closed channel returns `Result::Err(SendError::ChannelClosed)`.

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

### 4.3 Atomic Primitives and Memory Ordering
Lock-free primitives utilizing native hardware atomic instructions:
- `AtomicBool`, `AtomicI32`, `AtomicI64`, `AtomicUsize`.
- Provide sequentially consistent (`SeqCst`), acquire/release (`AcqRel`), and relaxed (`Relaxed`) memory orderings.

---

## 5. Runtime Architecture & Scheduler Model

> **Status**: `UNDER INVESTIGATION` (Implementation Architecture)

1. **Work-Stealing Scheduler**:
   - The proposed runtime architecture schedules $M$ lightweight tasks across $N$ OS worker threads (where $N$ defaults to logical CPU core count).
   - Idle worker threads steal tasks from the deques of busy threads.
   - *Note*: The M:N work-stealing scheduler is a planned runtime architecture, not an immutable language-level guarantee. Standalone single-threaded CLI tools link without the multi-threaded scheduler.
2. **Non-Blocking I/O Integration**:
   - The runtime integrates directly with OS-native event demultiplexers (`epoll` on Linux, `kqueue` on macOS/BSD, `IOCP` on Windows).
   - Network and file descriptors suspend the calling task without blocking the underlying OS worker thread.
