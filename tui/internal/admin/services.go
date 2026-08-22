package admin

import (
	"bytes"
	"os/exec"
	"strings"
)

// DockerExecRunner builds a DockerRunner over a real docker binary --
// mirrors internal/board.ExecRunner's own shape exactly (name the binary,
// return a func running it), the one real implementation cmd/keelson
// would wire in.
func DockerExecRunner(name string) DockerRunner {
	return func(args []string) ([]byte, error) {
		out, err := exec.Command(name, args...).Output()
		if err != nil {
			if ee, ok := err.(*exec.ExitError); ok {
				return nil, &execError{name: name, err: err, stderr: string(ee.Stderr)}
			}
			return nil, err
		}
		return out, nil
	}
}

// execError matches internal/board.ExecRunner's own error shape (name,
// underlying error, stderr) so a docker failure (daemon not running, no
// docker installed) renders as visibly as a ledger/gh failure does
// elsewhere in this module.
type execError struct {
	name   string
	err    error
	stderr string
}

func (e *execError) Error() string {
	if e.stderr == "" {
		return e.name + ": " + e.err.Error()
	}
	return e.name + ": " + e.err.Error() + ": " + e.stderr
}

// dockerPSFormat asks docker for exactly the three fields Service has,
// tab-separated -- no JSON parsing needed for three plain strings, and no
// dependency on docker's `--format json` (added in later docker CLI
// versions this box may not have) when `--format` template strings have
// worked since docker 1.13.
const dockerPSFormat = "{{.Names}}\t{{.Image}}\t{{.Status}}"

// fetchServices lists every container docker knows about (running or
// stopped, `-a`) -- a stopped container this estate depends on is exactly
// the kind of fact an admin view exists to surface, not something to
// filter out by only asking for running ones.
func fetchServices(run DockerRunner) ([]Service, error) {
	out, err := run([]string{"ps", "-a", "--format", dockerPSFormat})
	if err != nil {
		return nil, err
	}
	return parseDockerPS(out), nil
}

func parseDockerPS(out []byte) []Service {
	var services []Service
	lines := bytes.Split(bytes.TrimRight(out, "\n"), []byte("\n"))
	for _, line := range lines {
		if len(line) == 0 {
			continue
		}
		fields := strings.Split(string(line), "\t")
		s := Service{Name: fields[0]}
		if len(fields) > 1 {
			s.Image = fields[1]
		}
		if len(fields) > 2 {
			s.Status = fields[2]
		}
		services = append(services, s)
	}
	return services
}
