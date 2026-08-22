#!/usr/bin/env bash
# host-pressure.sh -- refuse a NEW dispatch when the host cannot safely take
# it. Exit 0: within limits, proceed. Exit 1: over threshold, refused. Exit
# 2: could not measure at all, refused -- an instrument that cannot see a
# thing must never report clean (this estate's own stated failure mode; see
# AGENTS.md's "absence is a typed value" rule and daemon/internal/pressure's
# identical Check() contract).
#
# WHY THIS EXISTS (corpus directive it-ef548e51e71daebe, prompt
# mp-7ab612e1d1fd6a30, 2026-08-21 16:24; agent-supervisor#500's own
# confirmation that neither #397 nor #476 -- both quota-reporting fixes,
# both predating the directive -- touch this half of the complaint at all):
# Jon, blunt, that the supervisor's own resource usage was making his
# machine unusable while he could not even type. #500 measured free memory
# as low as 79M on an 18GB machine with 4 build lanes running, and named the
# actual gap directly: "No concurrent-process cap or load-based dispatch
# gate exists anywhere in this estate." This script is that gate, called
# from `dispatch.sh` -- the one place in the shell/tmux control plane that
# actually starts a NEW agent process -- not from `director-loop.sh`, which
# only sends a tmux nudge to an EXISTING pane and adds no new load itself.
#
# THRESHOLDS ARE NOT INVENTED HERE. They mirror
# `daemon/internal/pressure.Default()` (agent-supervisor#499, merged the
# same day this was written): 3.0 load/core is gastown's own documented
# recommendation (pressure.go's own comment); 1.5GB free is above the
# ~3%-free state that forced an actual restart. Two independent
# implementations of the same numbers is a real, named cost, not an
# oversight: `daemon`'s own gate (`cmd/supervisord batch`) only fires inside
# the Go control plane, which nothing in this estate's actual dispatch path
# invokes today (`daemon/README.md`'s own "Not done yet" list, and #499's PR
# body, both say the shell/tmux path is what is still live) -- so a bash
# gate is what the LIVE path needs, and reusing the Go binary as a
# subprocess would make every dispatch depend on a `daemon` build existing
# on PATH, which is not yet true anywhere real. If either threshold ever
# changes, both files need the edit; this comment and pressure.go's own are
# the cross-reference that keeps that from being missed silently.
#
# NOTE ON PROVENANCE: `next-priority.md`'s own brief for this fix names the
# reusable logic as living in "check.sh's old host-pressure gate." No file
# by that name exists anywhere in this repo's history, agent-dotfiles's
# history, this machine's crontab, or its LaunchAgents (`git log --all
# --full-history -- '*/check.sh' 'check.sh'` returns nothing in either
# repo) -- checked, not assumed, before writing anything. The threshold
# values described (3.0 load/core) match `daemon/internal/pressure.go`
# exactly, including its own citation of the same external source
# (gastown), so that package -- not a phantom `check.sh` -- is treated here
# as the reusable logic the brief was actually pointing at.
#
# Usage: host-pressure.sh
#   Prints one line (the verdict and why) to stdout, always.
#   Exit 0 / 1 / 2 as above.
#
# Override via environment (0 disables that one check, matching
# pressure.Limits' own "0 disables" doc comment):
#   SUPERVISOR_MAX_LOAD_PER_CORE  (default 3.0)
#   SUPERVISOR_MIN_FREE_MEM_GB    (default 1.5)
set -uo pipefail

MAX_LOAD_PER_CORE="${SUPERVISOR_MAX_LOAD_PER_CORE:-3.0}"
MIN_FREE_MEM_GB="${SUPERVISOR_MIN_FREE_MEM_GB:-1.5}"

# _load1: darwin's 1-minute load average via sysctl. `vm.loadavg` prints
# Darwin is where this estate's actual dispatch runs (Jon's own Mac); Linux
# is where this repo's CI (.github/workflows/*.yml: ubuntu-latest, every
# workflow) runs this script's own tests and, incidentally, dispatch.sh's
# giant existing suite. Both are real targets, not one real and one
# CI-only-humoured: `uname` picked once, not re-detected per call.
# HOST_PRESSURE_TEST_OS overrides it -- a test seam, same shape as
# GH_ISSUES/QUOTA_GATE/LANES_FIXTURE elsewhere in this repo's own test
# suites: test_host_pressure.sh runs on whatever CI host it's given and
# needs to exercise BOTH branches deterministically regardless of which one
# `uname` would actually pick there. Never read by anything except this
# line; production callers (dispatch.sh) never set it.
_OS="${HOST_PRESSURE_TEST_OS:-$(uname -s 2>/dev/null)}"

# "{ 4.57 5.17 5.39 }" -- the SECOND field (awk $2, after the opening brace)
# is the 1-minute figure, matching pressure.go's own load1() (its
# getloadavg() syscall's first element). Linux's own /proc/loadavg puts the
# 1-minute figure FIRST instead -- different kernel, different field order,
# both real interfaces rather than one true one and one guessed.
_load1() {
  if [ "$_OS" = "Darwin" ]; then
    sysctl -n vm.loadavg 2>/dev/null | awk '{print $2}'
  else
    awk '{print $1}' /proc/loadavg 2>/dev/null
  fi
}
_ncpu() {
  if [ "$_OS" = "Darwin" ]; then
    sysctl -n hw.ncpu 2>/dev/null
  else
    nproc 2>/dev/null
  fi
}

# _free_mem_gb: on Darwin, free + inactive + speculative pages -- the same
# three categories pressure.go's freeMemGB() sums (its own comment: these
# three, not "free" alone, are what macOS actually treats as reclaimable-
# without-swapping). Pagesize read live via sysctl rather than assumed 4096
# or 16384 -- measured different values across this estate's own Intel and
# Apple Silicon Macs. On Linux, /proc/meminfo's own MemAvailable is already
# the kernel's own equivalent estimate (free + reclaimable, its own
# documented definition since 3.14) -- reimplementing the free+inactive+
# cached arithmetic by hand would be recomputing what the kernel already
# publishes, and worse: MemFree alone (the Darwin script's literal
# analogue) undercounts on Linux by exactly the reclaimable-cache amount,
# which is large and would make this gate refuse constantly on a healthy
# Linux host -- the same "instrument reports a false constraint" failure
# this whole feature exists to avoid, just inverted.
_free_mem_gb() {
  if [ "$_OS" = "Darwin" ]; then
    local pagesize pages
    pagesize=$(sysctl -n hw.pagesize 2>/dev/null) || return 1
    pages=$(vm_stat 2>/dev/null | awk '
      /^Pages free:/        { f = $3 }
      /^Pages inactive:/    { i = $3 }
      /^Pages speculative:/ { s = $3 }
      END {
        gsub(/\./, "", f); gsub(/\./, "", i); gsub(/\./, "", s)
        if (f == "" || i == "" || s == "") { exit 1 }
        print f + i + s
      }')
    [ -n "$pages" ] || return 1
    awk -v p="$pages" -v ps="$pagesize" 'BEGIN { printf "%.2f", p * ps / 1024 / 1024 / 1024 }'
  else
    awk '/^MemAvailable:/ { if ($2 != "") { printf "%.2f", $2 / 1024 / 1024; found=1 } }
         END { if (!found) exit 1 }' /proc/meminfo 2>/dev/null
  fi
}

main() {
  if awk -v m="$MAX_LOAD_PER_CORE" 'BEGIN { exit !(m > 0) }'; then
    local load cores
    load=$(_load1)
    if [ -z "$load" ]; then
      echo "host-pressure: could not read load average (sysctl vm.loadavg failed) -- refusing to guess whether the host is safe"
      return 2
    fi
    cores=$(_ncpu)
    if [ -z "$cores" ] || [ "$cores" = 0 ]; then
      echo "host-pressure: could not read core count (sysctl hw.ncpu failed) -- refusing to guess whether the host is safe"
      return 2
    fi
    local loadpercore
    loadpercore=$(awk -v l="$load" -v c="$cores" 'BEGIN { printf "%.2f", l / c }')
    if awk -v l="$loadpercore" -v m="$MAX_LOAD_PER_CORE" 'BEGIN { exit !(l >= m) }'; then
      echo "host-pressure: load/core $loadpercore >= $MAX_LOAD_PER_CORE -- refusing a new dispatch"
      return 1
    fi
  fi

  if awk -v m="$MIN_FREE_MEM_GB" 'BEGIN { exit !(m > 0) }'; then
    local free
    free=$(_free_mem_gb)
    if [ -z "$free" ]; then
      echo "host-pressure: could not read free memory (vm_stat/sysctl failed) -- refusing to guess whether the host is safe"
      return 2
    fi
    if awk -v f="$free" -v m="$MIN_FREE_MEM_GB" 'BEGIN { exit !(f < m) }'; then
      echo "host-pressure: free memory ${free}GB < ${MIN_FREE_MEM_GB}GB -- refusing a new dispatch"
      return 1
    fi
  fi

  echo "host-pressure: within limits"
  return 0
}

main
exit $?
