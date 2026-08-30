"""Refuse a NEW dispatch when the HOST cannot safely take another agent.

Ported from ``daemon/internal/pressure`` -- see the archived
``docs/archive/daemon/internal/pressure/pressure.go`` and ``getloadavg.go``
(agent-supervisor#627 retired the daemon, agent-supervisor#630 archived it,
agent-supervisor#643 ported this one package forward). That package was
itself STOLEN FROM gastown's ``internal/daemon/pressure.go``: two tiers,
load per core and free memory, checked before every spawn.

WHY THIS EXISTS: on 2026-08-21 the shell supervisor ran ~26 concurrent
agents, drove 1-minute load to 27 and swap to 8.7GB of 10.2GB, and made the
operator's Mac unresponsive to typing. Twice. He had to hard-restart the
machine both times. A supervisor that makes its operator's machine unusable
has failed, whatever it merged.

ONE DELIBERATE DIVERGENCE FROM GASTOWN, carried forward unchanged from the
Go source: their thresholds default to zero, which disables the check
entirely. Ours default ON. A protection that ships disabled did not protect
anyone, and this estate has the receipts (agent-supervisor#500: free memory
measured as low as 79M on an 18GB machine with 4 build lanes running).

FAIL CLOSED ON AN UNREADABLE METRIC. If load average or free memory cannot
be read, :func:`check` reports NOT ok -- never a silent "clean" pass. This
is this estate's own standing rule (a check that cannot see must not report
clean; see ``AGENTS.md``'s "absence is a typed value"), stated in the Go
source's own comments and already the convention throughout this codebase.

THIS IS NOT THE ONLY IMPLEMENTATION, and that is a deliberate, checked
decision, not an oversight. ``scripts/supervisor/host-pressure.sh`` ported
the same design into bash first (agent-supervisor#502, merged 2026-08-22,
three days before this module) and is the gate already wired into
``dispatch.sh`` -- the live, tmux-native dispatch path -- with its own
mutation-checked suite (``tests/supervisor/test_host_pressure.sh``,
``tests/supervisor/test_dispatch_host_pressure.sh``). This module does not
replace that gate or shell out to it: rewriting a tested, incident-motivated
safety check to make room for a second implementation of the same numbers
is a worse trade than leaving it alone, and host-pressure.sh's own comment
already names "two independent implementations of the same numbers" as a
real, tracked cost (there, between the archived Go binary and bash) -- so a
third one over dispatch.sh specifically was not added here.

What this module actually is: the Python-side port agent-supervisor#643
asked for, and the enforced gate for the two dispatch entry points that,
measured directly while doing this port, had NO host-pressure check of
EITHER kind -- ``dispatch-claude-print.sh`` and ``dispatch-pi-rpc.sh``, both
of which start a new agent process the same way ``dispatch.sh`` does. Those
two now call this module's CLI the same way ``dispatch.sh`` calls
``host-pressure.sh`` -- see their own "host pressure" comments.

THE THIRD GATE, added by agent-estate#904: ``host-pressure.sh``'s session /
work-in-flight cap (`#826`/`#899`) had no equivalent here at all -- this
module's own ``Result`` carried only load and free memory, so
``dispatch-claude-print.sh`` and ``dispatch-pi-rpc.sh`` could dispatch past
any work-in-flight ceiling as long as load/mem looked fine. Fixed the same
way the rest of this file already treats ``count-work-in-flight.sh``: SHELL
OUT to it (see :func:`_default_inflight_count`) rather than port its
lane-state classification a second time -- a second implementation of that
fail-closed classifier is a second thing to keep correct, and
`#831`/`#899` is already the story of getting it wrong once. Reads the same
``SUPERVISOR_MAX_AGENT_SESSIONS`` env var ``host-pressure.sh`` reads, so the
two gates cannot drift to different limits.
"""

from __future__ import annotations

import os
import re
import subprocess
import sys
from dataclasses import dataclass


# Defaults are deliberately protective, matching pressure.go's Default() and
# host-pressure.sh's own env-var defaults exactly -- 3.0 load/core is
# gastown's own documented recommendation; 1.5GB free is above the ~3%-free
# state the Mac was in when it stopped responding. 0 disables that one check.
DEFAULT_MAX_LOAD_PER_CORE = 3.0
DEFAULT_MIN_FREE_MEM_GB = 1.5

# Same default host-pressure.sh's own MAX_AGENT_SESSIONS uses -- one source
# for both gates (agent-estate#904). The cap value itself is the Director's
# call, not this module's; changing it here without also changing
# host-pressure.sh's default would recreate the exact drift #904 exists to
# close.
DEFAULT_MAX_AGENT_SESSIONS = 20


class PressureReadError(Exception):
    """A metric could not be read at all -- distinct from "read and over"."""


@dataclass(frozen=True)
class Limits:
    max_load_per_core: float = DEFAULT_MAX_LOAD_PER_CORE
    min_free_mem_gb: float = DEFAULT_MIN_FREE_MEM_GB
    max_agent_sessions: int = DEFAULT_MAX_AGENT_SESSIONS


@dataclass(frozen=True)
class Result:
    ok: bool
    load_per_core: float
    free_mem_gb: float
    reason: str

    def __str__(self) -> str:  # matches pressure.go's Result.String()
        return (
            f"load/core={self.load_per_core:.2f} freeMem={self.free_mem_gb:.2f}GB "
            f"ok={self.ok} {self.reason}"
        )


def default_limits() -> Limits:
    return Limits()


def limits_from_env() -> Limits:
    """Same override convention as host-pressure.sh: SUPERVISOR_MAX_LOAD_PER_CORE
    / SUPERVISOR_MIN_FREE_MEM_GB / SUPERVISOR_MAX_AGENT_SESSIONS, 0 disables
    that one check. SUPERVISOR_MAX_AGENT_SESSIONS is the exact same env var
    host-pressure.sh's own MAX_AGENT_SESSIONS reads (agent-estate#904) --
    one name, one source of truth, so the two gates cannot enforce different
    limits."""
    return Limits(
        max_load_per_core=float(os.environ.get("SUPERVISOR_MAX_LOAD_PER_CORE", DEFAULT_MAX_LOAD_PER_CORE)),
        min_free_mem_gb=float(os.environ.get("SUPERVISOR_MIN_FREE_MEM_GB", DEFAULT_MIN_FREE_MEM_GB)),
        max_agent_sessions=int(os.environ.get("SUPERVISOR_MAX_AGENT_SESSIONS", DEFAULT_MAX_AGENT_SESSIONS)),
    )


def _default_load1() -> float:
    """1-minute load average. Prefers the real syscall (Python exposes one
    directly, unlike Go's stdlib); falls back to parsing `uptime`'s output,
    same fallback pressure.go's own load1() used and for the same reason --
    a platform (or a sandboxed/containerized one) without the syscall still
    needs a real answer, not a crash."""
    try:
        return os.getloadavg()[0]
    except (OSError, AttributeError) as exc:
        pass
    try:
        out = subprocess.run(
            ["/usr/bin/uptime"], capture_output=True, text=True, timeout=5, check=True
        ).stdout
    except Exception as exc:  # noqa: BLE001 -- any failure here is "could not read"
        raise PressureReadError(f"could not read load average: {exc}") from exc
    i = out.rfind(":")
    if i < 0:
        raise PressureReadError(f"could not read load average: unparseable uptime: {out!r}")
    fields = re.split(r"[,\s]+", out[i + 1:].strip())
    if not fields or not fields[0]:
        raise PressureReadError(f"could not read load average: unparseable load: {out!r}")
    try:
        return float(fields[0])
    except ValueError as exc:
        raise PressureReadError(f"could not read load average: unparseable load: {out!r}") from exc


def _default_free_mem_gb() -> float:
    """free + inactive + speculative pages on Darwin (vm_stat), the same
    three categories pressure.go's freeMemGB() sums -- these three, not
    "free" alone, are what macOS actually treats as reclaimable without
    swapping. On Linux, /proc/meminfo's own MemAvailable is already the
    kernel's own equivalent estimate (free + reclaimable) -- the same
    choice host-pressure.sh's bash sibling made, for the same reason:
    reimplementing the free+inactive+cached arithmetic by hand on Linux
    would undercount and make this gate refuse on a healthy host."""
    if sys.platform == "darwin":
        try:
            out = subprocess.run(
                ["/usr/bin/vm_stat"], capture_output=True, text=True, timeout=5, check=True
            ).stdout
        except Exception as exc:  # noqa: BLE001
            raise PressureReadError(f"could not read free memory: {exc}") from exc
        pagesize = os.sysconf("SC_PAGE_SIZE") if hasattr(os, "sysconf") else 4096
        pages = 0.0
        found = False
        for line in out.splitlines():
            for key in ("Pages free:", "Pages inactive:", "Pages speculative:"):
                if line.startswith(key):
                    value = line[len(key):].strip().rstrip(".")
                    try:
                        pages += float(value)
                        found = True
                    except ValueError as exc:
                        raise PressureReadError(f"could not read free memory: {exc}") from exc
        if not found:
            raise PressureReadError("could not read free memory: vm_stat returned no usable page counts")
        return pages * pagesize / (1024 ** 3)

    # Linux (and anything else with /proc/meminfo, e.g. CI): MemAvailable.
    try:
        with open("/proc/meminfo", "r", encoding="utf-8") as fh:
            meminfo = fh.read()
    except OSError as exc:
        raise PressureReadError(f"could not read free memory: {exc}") from exc
    match = re.search(r"^MemAvailable:\s+(\d+)\s*kB", meminfo, re.MULTILINE)
    if not match:
        raise PressureReadError("could not read free memory: MemAvailable not found in /proc/meminfo")
    return float(match.group(1)) * 1024 / (1024 ** 3)


def _work_in_flight_script() -> str:
    """count-work-in-flight.sh, next to this module -- same "sibling script"
    resolution host-pressure.sh's own `$HERE/count-work-in-flight.sh` uses."""
    return os.path.join(os.path.dirname(os.path.abspath(__file__)), "count-work-in-flight.sh")


def _default_inflight_count() -> int:
    """Shells out to count-work-in-flight.sh -- agent-supervisor#899's merged,
    mutation-tested, fail-closed answer to "how many lanes are executing
    right now", the exact script host-pressure.sh's own session gate calls.
    Reused rather than re-derived (agent-estate#904): porting that lane-state
    classification into Python a second time would be a second
    implementation to keep correct, and #831/#899 is already the story of
    getting it wrong once with just one.

    Overridable via HOST_PRESSURE_WORK_IN_FLIGHT_SH for tests only, same
    seam shape count-work-in-flight.sh's own WORK_IN_FLIGHT_* env vars use --
    production code never sets it.
    """
    script = os.environ.get("HOST_PRESSURE_WORK_IN_FLIGHT_SH", _work_in_flight_script())
    if not os.access(script, os.X_OK):
        raise PressureReadError(
            f"could not read work-in-flight count (count-work-in-flight.sh missing or not executable at {script})"
        )
    try:
        result = subprocess.run(
            [script], capture_output=True, text=True, timeout=30
        )
    except Exception as exc:  # noqa: BLE001 -- any failure here is "could not read"
        raise PressureReadError(f"could not read work-in-flight count (count-work-in-flight.sh failed: {exc})") from exc
    if result.returncode != 0 or not result.stdout.strip():
        raise PressureReadError(
            f"could not read work-in-flight count (count-work-in-flight.sh exited {result.returncode})"
        )
    try:
        return int(result.stdout.strip())
    except ValueError as exc:
        raise PressureReadError(
            f"could not read work-in-flight count (count-work-in-flight.sh returned non-numeric output: {result.stdout.strip()!r})"
        ) from exc


def check(
    limits: Limits | None = None,
    *,
    load1=None,
    free_mem_gb=None,
    cpu_count=None,
    inflight_count=None,
) -> Result:
    """Reads the live host. An unreadable metric is NOT treated as healthy --
    the could-not-measure rule this estate learned the hard way: a check
    that cannot see must not report clean.

    load1 / free_mem_gb / cpu_count / inflight_count are injectable for
    tests -- they default to the real readers above. Each raises
    PressureReadError (or, for cpu_count, returns None/0) on an unreadable
    metric.
    """
    limits = limits or default_limits()
    load1 = load1 or _default_load1
    free_mem_gb = free_mem_gb or _default_free_mem_gb
    cpu_count = cpu_count or os.cpu_count
    inflight_count = inflight_count or _default_inflight_count

    load_per_core = 0.0
    free = 0.0

    if limits.max_load_per_core > 0:
        try:
            load = load1()
        except PressureReadError as exc:
            return Result(ok=False, load_per_core=0.0, free_mem_gb=0.0, reason=str(exc))
        cores = cpu_count()
        if not cores:
            return Result(
                ok=False, load_per_core=0.0, free_mem_gb=0.0,
                reason="could not read core count",
            )
        load_per_core = load / cores
        if load_per_core >= limits.max_load_per_core:
            return Result(
                ok=False, load_per_core=load_per_core, free_mem_gb=0.0,
                reason=f"load/core {load_per_core:.2f} >= {limits.max_load_per_core}",
            )

    if limits.min_free_mem_gb > 0:
        try:
            free = free_mem_gb()
        except PressureReadError as exc:
            return Result(ok=False, load_per_core=load_per_core, free_mem_gb=0.0, reason=str(exc))
        if free < limits.min_free_mem_gb:
            return Result(
                ok=False, load_per_core=load_per_core, free_mem_gb=free,
                reason=f"free memory {free:.2f}GB < {limits.min_free_mem_gb}GB",
            )

    if limits.max_agent_sessions > 0:
        try:
            inflight = inflight_count()
        except PressureReadError as exc:
            return Result(ok=False, load_per_core=load_per_core, free_mem_gb=free, reason=str(exc))
        if inflight >= limits.max_agent_sessions:
            return Result(
                ok=False, load_per_core=load_per_core, free_mem_gb=free,
                reason=f"work in flight {inflight} >= {limits.max_agent_sessions}",
            )

    return Result(ok=True, load_per_core=load_per_core, free_mem_gb=free, reason="within limits")


def main(argv=None) -> int:
    """CLI contract matches host-pressure.sh's exactly, so a caller (bash or
    Python) can treat either gate identically: one line to stdout, always;
    exit 0 within limits (proceed), 1 over a threshold (refused), 2 could
    not measure at all (refused -- never a silent clean pass)."""
    result = check(limits_from_env())
    if "could not read" in result.reason:
        print(f"host-pressure: {result.reason} -- refusing to guess whether the host is safe")
        return 2
    if not result.ok:
        print(f"host-pressure: {result.reason} -- refusing a new dispatch")
        return 1
    print(f"host-pressure: {result.reason}")
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
