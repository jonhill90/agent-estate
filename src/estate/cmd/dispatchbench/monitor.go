package main

// THE HARNESS WATCHES THE WORKER. The worker does not watch itself.
//
// agent-estate#1002's first attempt put the floor check inside the thing
// being measured, and the thing being measured was the thing that ran away.
// Here the floor is sampled by this file, on its own goroutines, and it can
// cancel the turn's context -- the worker has no vote.
//
// The gauge is internal/pressure, the estate's own. Not a second reader of
// vm_stat: two gauges that disagree about one host is exactly the shape of
// the failure #999 fixed. pressure.Host exists so this harness can ask the
// host-only half of that gate without being refused for being the in-flight
// turn it already is.
//
// Samples are streamed to a file as they are taken and never accumulated in
// memory. The first attempt's binary reached 1753MB; a benchmark that has to
// be measured for its own footprint has no business holding its samples.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jonhill90/agent-estate/estate/internal/pressure"
)

// benchLimits are DELIBERATELY STRICTER than pressure.Default(). The dispatch
// gate's job is to refuse the last turn that would hurt; this benchmark's job
// is to stop long before that, because it is running unattended against a host
// that was taken down twice today. 2048MB against the gate's 512MB leaves the
// gate's own margin intact underneath.
type benchLimits struct {
	MinFreeMemMB         float64
	MaxSwapoutsPerSample float64
	// MaxWorkerRSSMB is a runaway detector, not a resource limit. A stateless
	// worker measured on live lanes sits at 430-500MB; the first attempt's
	// benchmark process reached 1753MB. Anything above this cap is not a
	// heavy turn, it is the failure repeating.
	MaxWorkerRSSMB float64
}

func defaultBenchLimits() benchLimits {
	return benchLimits{MinFreeMemMB: 2048, MaxSwapoutsPerSample: 1, MaxWorkerRSSMB: 2000}
}

// asPressureLimits neutralises every limit pressure.Host applies EXCEPT the
// two this harness gates on, so a refusal here is always one of ours and
// never an unrelated one arriving under our name.
func (l benchLimits) asPressureLimits() pressure.Limits {
	return pressure.Limits{
		MaxLoadPerCore:       1e9,
		MinFreeMemMB:         l.MinFreeMemMB,
		MaxSwapoutsPerSample: l.MaxSwapoutsPerSample,
		MaxWorktrees:         1e9,
		MaxInFlight:          1e9,
	}
}

type sample struct {
	T            string  `json:"t"`
	Arm          string  `json:"arm"`
	Turn         int     `json:"turn"`
	WorkerRSSMB  float64 `json:"worker_rss_mb"`
	WorkerProcs  int     `json:"worker_procs"`
	HostRSSMB    float64 `json:"host_rss_mb"`
	HostProcs    int     `json:"host_procs"`
	FreeMemMB    float64 `json:"free_mem_mb,omitempty"`
	SwapoutRate  float64 `json:"swapouts_per_2s,omitempty"`
	PressureOK   *bool   `json:"pressure_ok,omitempty"`
	PressureWhy  string  `json:"pressure_why,omitempty"`
	MeasureError string  `json:"measure_error,omitempty"`
}

// aggregate is the bounded summary kept in memory: four scalars per arm-turn,
// so a hundred turns costs a few hundred bytes.
type aggregate struct {
	PeakWorkerRSSMB float64 `json:"peak_worker_rss_mb"`
	PeakHostRSSMB   float64 `json:"peak_host_rss_mb"`
	MinFreeMemMB    float64 `json:"min_free_mem_mb"`
	MaxSwapoutRate  float64 `json:"max_swapouts_per_2s"`
	Samples         int     `json:"samples"`
}

func (a *aggregate) add(s sample) {
	a.Samples++
	if s.WorkerRSSMB > a.PeakWorkerRSSMB {
		a.PeakWorkerRSSMB = s.WorkerRSSMB
	}
	if s.HostRSSMB > a.PeakHostRSSMB {
		a.PeakHostRSSMB = s.HostRSSMB
	}
	if s.FreeMemMB > 0 && (a.MinFreeMemMB == 0 || s.FreeMemMB < a.MinFreeMemMB) {
		a.MinFreeMemMB = s.FreeMemMB
	}
	if s.SwapoutRate > a.MaxSwapoutRate {
		a.MaxSwapoutRate = s.SwapoutRate
	}
}

type monitor struct {
	lim  benchLimits
	sink *os.File
	enc  *json.Encoder

	mu      sync.Mutex
	arm     string
	turn    int
	rootPid int
	agg     map[string]*aggregate
	overall aggregate

	abortOnce sync.Once
	abortWhy  string
	cancel    context.CancelFunc
	stop      chan struct{}
	wg        sync.WaitGroup
}

func newMonitor(path string, lim benchLimits) (*monitor, error) {
	f, err := os.Create(path)
	if err != nil {
		return nil, fmt.Errorf("open sample sink: %w", err)
	}
	return &monitor{lim: lim, sink: f, enc: json.NewEncoder(f), agg: map[string]*aggregate{}, stop: make(chan struct{})}, nil
}

// Start runs the two sampling loops. cancel is the run's own cancel func: the
// monitor is the ONLY thing in this program that calls it, and calling it is
// how a floor breach reaches the worker.
func (m *monitor) Start(cancel context.CancelFunc) {
	m.cancel = cancel
	m.wg.Add(2)
	go m.rssLoop()
	go m.pressureLoop()
}

func (m *monitor) Close() aggregate {
	close(m.stop)
	m.wg.Wait()
	m.sink.Close()
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.overall
}

// Watch names what the sampler should attribute the next samples to. rootPid
// 0 means "no worker right now" -- between turns -- and is recorded as a
// worker footprint of zero rather than skipped, so the gaps are visible in
// the sample file.
func (m *monitor) Watch(arm string, turn int, rootPid int) {
	m.mu.Lock()
	m.arm, m.turn, m.rootPid = arm, turn, rootPid
	m.mu.Unlock()
}

func (m *monitor) Aggregate(arm string, turn int) aggregate {
	m.mu.Lock()
	defer m.mu.Unlock()
	if a, ok := m.agg[aggKey(arm, turn)]; ok {
		return *a
	}
	return aggregate{}
}

func aggKey(arm string, turn int) string { return arm + "/" + strconv.Itoa(turn) }

func (m *monitor) record(s sample) {
	m.mu.Lock()
	s.Arm, s.Turn = m.arm, m.turn
	k := aggKey(s.Arm, s.Turn)
	a, ok := m.agg[k]
	if !ok {
		a = &aggregate{}
		m.agg[k] = a
	}
	a.add(s)
	m.overall.add(s)
	m.mu.Unlock()
	_ = m.enc.Encode(s) // a sample we cannot write is not worth aborting a run over
}

// abort is the whole point of this file: it cancels the run and remembers
// why, exactly once.
func (m *monitor) abort(why string) {
	m.abortOnce.Do(func() {
		m.mu.Lock()
		m.abortWhy = why
		m.mu.Unlock()
		fmt.Fprintf(os.Stderr, "\nABORT: %s\n", why)
		if m.cancel != nil {
			m.cancel()
		}
	})
}

func (m *monitor) AbortReason() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.abortWhy
}

// rssLoop samples memory a second at a time. One `ps` call yields both the
// worker's own tree and the host-wide total, so the two figures are always
// the same instant rather than two instants compared.
func (m *monitor) rssLoop() {
	defer m.wg.Done()
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		select {
		case <-m.stop:
			return
		case <-t.C:
			m.mu.Lock()
			root := m.rootPid
			m.mu.Unlock()

			procs, err := readProcs()
			if err != nil {
				m.record(sample{T: nowStamp(), MeasureError: "ps: " + err.Error()})
				// A memory gauge that cannot be read is not permission to keep
				// running an unattended benchmark on this host.
				m.abort("could not measure process memory: " + err.Error())
				return
			}
			workerMB, workerN := subtreeRSSMB(procs, root)
			hostMB := 0.0
			for _, p := range procs {
				hostMB += p.rssMB
			}
			s := sample{T: nowStamp(), WorkerRSSMB: workerMB, WorkerProcs: workerN, HostRSSMB: hostMB, HostProcs: len(procs)}
			m.record(s)
			if root != 0 && workerMB > m.lim.MaxWorkerRSSMB {
				m.abort(fmt.Sprintf("worker tree reached %.0fMB, above the runaway cap of %.0fMB", workerMB, m.lim.MaxWorkerRSSMB))
				return
			}
		}
	}
}

// pressureLoop asks the estate's own gate about the host. Each call blocks
// for pressure.SampleWindow() taking the swapout delta, so this loop runs at
// roughly that cadence by construction.
func (m *monitor) pressureLoop() {
	defer m.wg.Done()
	lim := m.lim.asPressureLimits()
	for {
		select {
		case <-m.stop:
			return
		default:
		}
		v := pressure.Host(lim)
		ok := v.OK
		s := sample{
			T:           nowStamp(),
			FreeMemMB:   v.Reading.FreeMemMB,
			SwapoutRate: v.Reading.SwapoutRate,
			PressureOK:  &ok,
			PressureWhy: strings.Join(v.Reasons, "; "),
		}
		m.record(s)
		if !ok {
			m.abort("host pressure refused mid-run: " + strings.Join(v.Reasons, "; "))
			return
		}
	}
}

// Preflight is the same gate, asked once before a turn starts. A run that
// cannot start is a partial result with its stopping condition named, which
// the brief for #1002 says is a legitimate finding.
func (m *monitor) Preflight() error {
	v := pressure.Host(m.lim.asPressureLimits())
	if !v.OK {
		return fmt.Errorf("host pressure refused before the turn: %s", strings.Join(v.Reasons, "; "))
	}
	return nil
}

func nowStamp() string { return time.Now().UTC().Format(time.RFC3339Nano) }

type procRow struct {
	pid, ppid int
	rssMB     float64
}

// readProcs reads every process on the host once. `ps` rather than
// libproc/sysctl for the same reason internal/pressure shells to vm_stat:
// the number is only useful if a human can reproduce it from a terminal.
func readProcs() ([]procRow, error) {
	out, err := exec.Command("ps", "-Ao", "pid=,ppid=,rss=").Output()
	if err != nil {
		return nil, err
	}
	var rows []procRow
	for _, ln := range strings.Split(string(out), "\n") {
		f := strings.Fields(ln)
		if len(f) != 3 {
			continue
		}
		pid, err1 := strconv.Atoi(f[0])
		ppid, err2 := strconv.Atoi(f[1])
		rssKB, err3 := strconv.ParseFloat(f[2], 64)
		if err1 != nil || err2 != nil || err3 != nil {
			continue
		}
		rows = append(rows, procRow{pid: pid, ppid: ppid, rssMB: rssKB / 1024})
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("ps returned no parseable rows")
	}
	return rows, nil
}

// subtreeRSSMB sums root and every descendant.
//
// A WORKER IS A TREE, NOT A PROCESS. `claude` spawns the tools it runs, and
// measuring only the root would report a turn shelling out to a compiler as
// costing nothing. This is also why the persistent arm's root is the pane's
// own process rather than the tmux server: the server is the harness's
// scaffolding, the pane's tree is the lane.
func subtreeRSSMB(procs []procRow, root int) (float64, int) {
	if root == 0 {
		return 0, 0
	}
	children := map[int][]int{}
	rss := map[int]float64{}
	for _, p := range procs {
		children[p.ppid] = append(children[p.ppid], p.pid)
		rss[p.pid] = p.rssMB
	}
	if _, ok := rss[root]; !ok {
		return 0, 0 // the worker has exited; not an error, just gone
	}
	total, n := 0.0, 0
	seen := map[int]bool{}
	queue := []int{root}
	for len(queue) > 0 {
		pid := queue[0]
		queue = queue[1:]
		if seen[pid] {
			continue // a ppid cycle cannot happen, but an unbounded loop here would be the runaway
		}
		seen[pid] = true
		total += rss[pid]
		n++
		queue = append(queue, children[pid]...)
	}
	return total, n
}
