# Source watching

`NewWatcher` uses the shared filesystem observer. Existing builder dependencies
retain named-file selection and the legacy directory exclusions. Optional
`WatchTree` values declare directory interest even before a directory exists.
Their `Exclude` callback owns unconditional output exclusions. Editor-like names
such as `.tmp` and `.bak` are delivered for explicit trees because they can be
compiler inputs; only legacy directory dependency selection retains transient-name
filtering. An exact named-file dependency observes its explicitly selected file
even with such a suffix. A Git ignore rule
is not an output rule, because a compiler can consume ignored assets.

A missing tree keeps a nonrecursive subscription on its nearest existing parent.
Created or replaced subtrees are registered recursively before invalidation is
sent. Remove and rename retain logical interest. Selected roots must be resolved
directories, not symlinks; symlink leaves are not traversed. Consumers needing
symlink targets must select those targets explicitly and own link-change policy.

`Change.Rescan` requires full owner input discovery, including when no leaf
change was observed during a directory replacement. `Change.Err` means observation
is incomplete. `Watcher.Err()` retains the current filesystem coverage error;
it is safe to query concurrently. Neither a nil error nor successful subscription
recovery proves a valid build or application readiness.

Registration errors retain previous successful subscriptions and retry with
backoff from 100 milliseconds to a 5-second ceiling. Overflow follows the same
resubscription and rescan path. A closed underlying event/error stream is terminal
and visible; a new watcher is required. The owner still validates its full input
selection after successful recovery. There is no source-build polling loop.

`Start` owns channel closure and releases descriptors on cancellation. Base
owns the lifetime beyond an Init RPC, logs degraded observation and suppresses
pending rebuild callbacks while `Watcher.Err()` reports degraded coverage.
Ordinary sibling changes cannot resume callbacks. Successful registration clears
the error and emits a full rescan; Base checks coverage again after debounce. Base retains
rescan across its debounce window. Existing Pause/Resume behavior is unchanged:
ordinary events while paused are not queued; consumers must arrange rescan when
using that mode during a build.

Real filesystem tests cover atomic save, missing/recreated roots, populated
rename-in, deletion, explicit exclusions, traversal permission failure and repair,
closed resources, and cancellation. Actual kernel overflow generation is not
portable and is not claimed by those tests.
