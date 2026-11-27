#!/usr/bin/env bash
# Prints the packages test shard INDEX of TOTAL runs, one per line.
#
#   ./scripts/test_shard.sh 2 6
#   ./scripts/test_shard.sh --all 6  # one tab-separated shard/package row per package
#
# The split is deterministic: every shard computes the same partition from the
# same `go list`, so the shards together run every package exactly once
# (internal/ciguard's TestTestShardsPartitionEveryPackage holds that). It is a
# longest-processing-time-first packing: the packages below whose suites take
# tens of seconds are placed first, each on the least-loaded shard, and every
# other package then counts as one second.
#
# The weights only balance the shards. A stale or missing weight costs wall
# clock, never coverage: an unlisted package still lands on exactly one shard.
# Re-measure from a run's `go test -json` output (the Test jobs print the
# slowest packages in their step summary) when one shard dominates.
set -euo pipefail

index="${1:?usage: test_shard.sh INDEX|--all TOTAL}"
total="${2:?usage: test_shard.sh INDEX|--all TOTAL}"
if ! [[ "$total" =~ ^[1-9][0-9]*$ ]]; then
	echo "test_shard.sh: TOTAL must be a positive integer (got $total)" >&2
	exit 2
fi
if [[ "$index" != "--all" ]] && { ! [[ "$index" =~ ^[1-9][0-9]*$ ]] || ((index > total)); }; then
	echo "test_shard.sh: INDEX must be in 1..TOTAL (got $index of $total)" >&2
	exit 2
fi

# The tags the suite runs with; a package that only exists under them must be
# listed too.
tags="${TEST_TAGS:-sandbox_e2e,nix_required}"

cd "$(git rev-parse --show-toplevel)"

# Approximate package seconds under race detection and coverage. The
# v0.19.38 shard timings are the baseline, adjusted for fixture reuse and
# bounded concurrency in the runner and SDK tests. Refresh with CI artifacts.
weights='
github.com/codefly-dev/core/runners/base 124
github.com/codefly-dev/core/runners/golang 115
github.com/codefly-dev/core/runners/dockerrun 105
github.com/codefly-dev/core/sdk/dependencies 85
github.com/codefly-dev/core/agents/manager 47
github.com/codefly-dev/core/internal/ciguard 40
github.com/codefly-dev/core/toolbox/launch 39
github.com/codefly-dev/core/runners/python 38
github.com/codefly-dev/core/toolbox/session 31
github.com/codefly-dev/core/agents 29
github.com/codefly-dev/core/agents/services 29
github.com/codefly-dev/core/services 26
github.com/codefly-dev/core/code 25
github.com/codefly-dev/core/runners/rust 20
github.com/codefly-dev/core/contracts/module 19
github.com/codefly-dev/core/composition 18
github.com/codefly-dev/core/resources 12
'

go list -tags "$tags" ./... | WEIGHTS="$weights" awk -v index_="$index" -v total="$total" '
	BEGIN {
		n = split(ENVIRON["WEIGHTS"], lines, "\n")
		for (i = 1; i <= n; i++) {
			if (split(lines[i], f, " ") == 2) weight[f[1]] = f[2]
		}
	}
	{ pkgs[++count] = $0 }
	END {
		# Heaviest first; ties (and every unweighted package) keep go list order,
		# which is sorted, so the packing is the same on every shard.
		for (i = 1; i <= count; i++) order[i] = i
		for (i = 2; i <= count; i++) {
			j = i
			while (j > 1 && w(pkgs[order[j]]) > w(pkgs[order[j - 1]])) {
				t = order[j]; order[j] = order[j - 1]; order[j - 1] = t; j--
			}
		}
		for (s = 1; s <= total; s++) load[s] = 0
		for (i = 1; i <= count; i++) {
			best = 1
			for (s = 2; s <= total; s++) if (load[s] < load[best]) best = s
			load[best] += w(pkgs[order[i]])
			if (index_ == "--all") printf "%d\t%s\n", best, pkgs[order[i]]
			else if (best == index_) print pkgs[order[i]]
		}
	}
	function w(p) { return (p in weight) ? weight[p] : 1 }
'
