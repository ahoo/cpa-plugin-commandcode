package plugin

import (
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

// usageProbe records whether the host calls HandleUsage for this plugin's
// executor, and what the record carried. It is a diagnostic for deciding where
// token/timing statistics must be produced: if the host never calls it, the
// executor has to publish usage itself.
//
// Writes go to a file (not stdout) because a c-shared plugin cannot safely log
// through the host's logger. The path comes from COMMANDCODE_USAGE_PROBE; when
// unset the probe is a no-op so this file can ship without side effects.
var usageProbe = &probe{
	path: os.Getenv("COMMANDCODE_USAGE_PROBE"),
	seen: map[string]int{},
}

type probe struct {
	path string
	mu   sync.Mutex
	seen map[string]int
}

// startup writes a marker at plugin construction so the probe file's existence
// proves the write path works. Without it, a missing file is ambiguous: it
// could mean the host never called HandleUsage, or that the probe could not
// write at all.
func (p *probe) startup() {
	if p == nil || p.path == "" {
		return
	}
	p.append("STARTUP probe armed path=" + p.path + "\n")
}

func (p *probe) append(line string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	f, err := os.OpenFile(p.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.WriteString(line)
}

// record logs one HandleUsage call. Reaching this means the host does publish
// usage for plugin-owned executors, and the record carries the token and timing
// counters -- so the plugin only has to forward them.
func (p *probe) record(record pluginapi.UsageRecord) {
	if p == nil || p.path == "" {
		return
	}
	p.mu.Lock()
	p.seen[record.Model]++
	p.mu.Unlock()
	p.append(time.Now().Format(time.RFC3339Nano) +
		" CALLED model=" + record.Model +
		" provider=" + record.Provider +
		" alias=" + record.Alias +
		" executor=" + record.ExecutorType +
		" failed=" + strconv.FormatBool(record.Failed) +
		" in=" + strconv.FormatInt(record.Detail.InputTokens, 10) +
		" out=" + strconv.FormatInt(record.Detail.OutputTokens, 10) +
		" reasoning=" + strconv.FormatInt(record.Detail.ReasoningTokens, 10) +
		" cached=" + strconv.FormatInt(record.Detail.CachedTokens, 10) +
		" latency=" + record.Latency.String() +
		" ttft=" + record.TTFT.String() +
		"\n")
}
