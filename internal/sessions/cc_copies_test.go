package sessions

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// testdata/claude-forks is Claude Code 2.1.312's own files, from a sandbox
// run with a fake API, trimmed of its request records and attachments'
// text: original.jsonl is a session of two prompts, fork-on.jsonl is
// `claude --resume <it> --fork-session` with a third prompt, and
// fork-other.jsonl another such fork with a different third prompt. Both
// forks start with every message of the original again, under the same
// uuids and their own sessionId.
//
// compacted.jsonl is another session of that run, of six prompts, with a
// /compact after the fourth, a /rewind that restored the conversation to
// before the sixth and sent a prompt again, and a /rewind that summarized
// from the fifth, all of it in the one file. compacted-branch.jsonl is a
// /branch of it made then, which Claude Code starts from the first
// compaction's summary: only that and what follows, under the same uuids.
// compacted.jsonl went on with "rw prompt eight" after the branch, and
// compacted-branch-on.jsonl is the branch after `claude --resume <it> -p`
// went on with "the branch goes on". compacted-branch-last.jsonl is a
// /branch made after "rw prompt eight", holding the session's last message.
const (
	ccOriginal  = "fb0bf5a9-e23f-4c29-b71c-a184c38bb005"
	ccForkOn    = "7e99b4bb-7d93-4b3c-9453-901a1a9298d1"
	ccForkOther = "c2141c85-7cf5-46c0-a389-d584029233fb"
	ccCompacted = "7b1e0c2a-3f4d-4e5a-8b6c-1d2e3f4a5b6c"
	ccBranch    = "e68bf017-47a8-4a3a-8ea7-053006ca6201"
	// compacted-branch-last.jsonl: a /branch made after "rw prompt eight"
	ccLastBranch = "edc82b93-a527-4e55-b00a-89fcbf31c8cf"
	// subagent.jsonl is a session whose first prompt ran a subagent
	// (subagent-agent.jsonl, in the session's own subagents folder), and
	// subagent-fork.jsonl `claude --resume <it> --fork-session` with a
	// prompt of its own; Claude Code 2.1.312 left the subagent's file in
	// the first session's folder only. Each reply is 1000 in, 100 out.
	ccSubagent     = "cdf0c391-6ae3-4273-b9be-7c5532e57d7c"
	ccSubagentFork = "3a87a3d2-c7ee-4d3c-b559-ff8bdbac43a5"
)

// ccForkFixture writes the fixtures named (by file, to the id it is saved
// as) into dir/projects/-w-proj, oldest first, each a minute after the last
// and all of them over a minute old, so none is still being written.
// A copy saved under another id is the same file with its sessionId
// rewritten, as /branch or a background move leaves one before a turn.
func ccForkFixture(t *testing.T, dir string, files ...[2]string) {
	t.Helper()
	proj := filepath.Join(dir, "projects", "-w-proj")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	at := time.Now().Add(-time.Hour)
	for _, f := range files {
		b, err := os.ReadFile(filepath.Join("testdata", "claude-forks", f[0]))
		if err != nil {
			t.Fatal(err)
		}
		var from string
		switch f[0] {
		case "original.jsonl":
			from = ccOriginal
		case "fork-on.jsonl":
			from = ccForkOn
		case "compacted.jsonl":
			from = ccCompacted
		case "compacted-branch.jsonl", "compacted-branch-on.jsonl":
			from = ccBranch
		case "compacted-branch-last.jsonl":
			from = ccLastBranch
		case "subagent.jsonl":
			from = ccSubagent
		case "subagent-fork.jsonl":
			from = ccSubagentFork
		default:
			from = ccForkOther
		}
		b = []byte(strings.ReplaceAll(string(b), from, f[1]))
		p := filepath.Join(proj, f[1]+".jsonl")
		if err := os.WriteFile(p, b, 0o644); err != nil {
			t.Fatal(err)
		}
		at = at.Add(time.Minute)
		if err := os.Chtimes(p, at, at); err != nil {
			t.Fatal(err)
		}
	}
	Reset()
}

// ccRows is the ids of agent's sessions in the fixture's project, sorted.
func ccRows(agent string) []string {
	var ids []string
	for _, m := range ListAgent(agent) {
		if m.Cwd == "/w/proj" {
			ids = append(ids, m.ID)
		}
	}
	slices.Sort(ids)
	return ids
}

func ccPrompts(t *testing.T, agent, id string) []string {
	t.Helper()
	s, ok := Find(agent, id)
	if !ok {
		t.Fatalf("Find(%s, %s) found nothing", agent, id)
	}
	tr, err := TranscriptOf(s)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, p := range tr.Parts {
		if p.Role == "user" && p.Kind == "text" {
			out = append(out, p.Text)
		}
	}
	return out
}

var ccCopyIDs = []string{
	"11111111-1111-4111-8111-111111111111",
	"22222222-2222-4222-8222-222222222222",
	"33333333-3333-4333-8333-333333333333",
}

// Neo (#feedback): one Claude conversation listed as ten rows of the same
// title, size and message count, one per copy Claude Code had made of it.
// The copies and the session they were forked from are one row, the
// session that holds all of them, and it reads whole.
func TestClaudeForkCopiesAreOneRow(t *testing.T) {
	claude, _ := setup(t)
	before := agentCount("claude")
	files := [][2]string{{"original.jsonl", ccOriginal}}
	for _, id := range ccCopyIDs {
		files = append(files, [2]string{"original.jsonl", id})
	}
	files = append(files, [2]string{"fork-on.jsonl", ccForkOn})
	ccForkFixture(t, claude, files...)

	if got := ccRows("claude"); !slices.Equal(got, []string{ccForkOn}) {
		t.Fatalf("rows = %v, want only the fork that holds them all, %s", got, ccForkOn)
	}
	var listed []string
	for _, s := range List(0) {
		if s.Agent == "claude" && s.Cwd == "/w/proj" {
			listed = append(listed, s.ID)
		}
	}
	if !slices.Equal(listed, []string{ccForkOn}) {
		t.Errorf("List rows = %v, want [%s]", listed, ccForkOn)
	}
	if got := agentCount("claude"); got != before+1 {
		t.Errorf("Claude Code's count = %d, want %d: one conversation", got, before+1)
	}
	want := []string{"first prompt", "second prompt resumed", "third prompt forked"}
	if got := ccPrompts(t, "claude", ccForkOn); !slices.Equal(got, want) {
		t.Errorf("prompts = %q, want %q", got, want)
	}

	// what the conversation spent is counted once: three replies of 10 in,
	// 5 out, not again for each copy of the first two
	var tk Tokens
	for _, s := range StatsFor(0).Sessions {
		if s.Agent == "claude" && s.Cwd == "/w/proj" {
			tk.Input += s.Input
			tk.Output += s.Output
		}
	}
	if tk.Input != 30 || tk.Output != 15 {
		t.Errorf("tokens = %d in, %d out; want 30 in, 15 out", tk.Input, tk.Output)
	}
}

// A fork that went on with a turn of its own is a session of its own, as
// Claude Code's /resume lists it; the session both were forked from is in
// each of them.
func TestClaudeForkThatWentOnIsItsOwnRow(t *testing.T) {
	claude, _ := setup(t)
	ccForkFixture(t, claude,
		[2]string{"original.jsonl", ccOriginal},
		[2]string{"fork-on.jsonl", ccForkOn},
		[2]string{"fork-other.jsonl", ccForkOther})
	want := []string{ccForkOther, ccForkOn}
	slices.Sort(want)
	if got := ccRows("claude"); !slices.Equal(got, want) {
		t.Fatalf("rows = %v, want the two forks %v", got, want)
	}
	if got := ccPrompts(t, "claude", ccForkOther); !slices.Equal(got, []string{"first prompt", "second prompt resumed", "a different turn"}) {
		t.Errorf("the other fork's prompts = %q", got)
	}
}

// Neo (#feedback, after v0.1.1803): with /compact and /rewind the copies
// were rows again. A copy Claude Code makes after a compaction starts with
// the compaction's summary, not the conversation's first message, and it
// is the conversation's copy all the same.
func TestClaudeCopyAfterCompactIsOneRow(t *testing.T) {
	claude, _ := setup(t)
	ccForkFixture(t, claude,
		[2]string{"compacted-branch.jsonl", ccBranch},
		[2]string{"compacted-branch.jsonl", ccCopyIDs[0]},
		[2]string{"compacted.jsonl", ccCompacted})
	if got := ccRows("claude"); !slices.Equal(got, []string{ccCompacted}) {
		t.Fatalf("rows = %v, want only the session the branches were made of, %s", got, ccCompacted)
	}
	if got := ccPrompts(t, "claude", ccCompacted); !slices.Contains(got, "rw prompt one") {
		t.Errorf("the row's prompts = %q, want the conversation from its first", got)
	}
	var copies []string
	for _, m := range ListAgent("claude") {
		if m.ID == ccCompacted {
			copies = m.Copies
		}
	}
	want := []string{ccCopyIDs[0], ccBranch}
	slices.Sort(want)
	if !slices.Equal(copies, want) {
		t.Errorf("copies = %v, want %v", copies, want)
	}
	mu.Lock()
	var compacts []string
	for p, st := range cache {
		if strings.HasSuffix(p, ccCompacted+".jsonl") {
			compacts = st.Compacts
		}
	}
	mu.Unlock()
	if !slices.Equal(compacts, []string{"970fe039-320a-4dfd-a458-e7461c071a30", "777236a8-c3ff-4729-a182-170a8705bf5e"}) {
		t.Errorf("the session's compactions = %v, want /compact's summary and /rewind's", compacts)
	}
}

// A copy made after a compaction and written last is still the copy: the
// row is the session that holds the conversation from its first message,
// not the newest file, which reads from the summary on.
func TestClaudeNewerCopyAfterCompactIsNotTheRow(t *testing.T) {
	claude, _ := setup(t)
	ccForkFixture(t, claude,
		[2]string{"compacted.jsonl", ccCompacted},
		[2]string{"compacted-branch-last.jsonl", ccLastBranch})
	if got := ccRows("claude"); !slices.Equal(got, []string{ccCompacted}) {
		t.Fatalf("rows = %v, want the session that holds it all, %s", got, ccCompacted)
	}
}

// A branch made after a compaction that went on with a turn of its own is
// a row of its own, beside the session it was made of.
func TestClaudeBranchAfterCompactThatWentOnIsItsOwnRow(t *testing.T) {
	claude, _ := setup(t)
	ccForkFixture(t, claude,
		[2]string{"compacted.jsonl", ccCompacted},
		[2]string{"compacted-branch.jsonl", ccCopyIDs[0]},
		[2]string{"compacted-branch-on.jsonl", ccBranch})
	want := []string{ccCompacted, ccBranch}
	slices.Sort(want)
	if got := ccRows("claude"); !slices.Equal(got, want) {
		t.Fatalf("rows = %v, want the session and the branch that went on, %v", got, want)
	}
}

// Deleting the conversation takes its copies along, or the next list
// would show the newest of them in its place; restoring brings them back.
func TestClaudeDeleteTakesCopiesAlong(t *testing.T) {
	claude, _ := setup(t)
	ccForkFixture(t, claude,
		[2]string{"original.jsonl", ccOriginal},
		[2]string{"original.jsonl", ccCopyIDs[0]},
		[2]string{"fork-on.jsonl", ccForkOn})
	if got := ccRows("claude"); !slices.Equal(got, []string{ccForkOn}) {
		t.Fatalf("rows = %v", got)
	}
	tr, err := Delete("claude", ccForkOn)
	if err != nil {
		t.Fatal(err)
	}
	if got := ccRows("claude"); len(got) != 0 {
		t.Errorf("after delete, rows = %v: a copy came back", got)
	}
	proj := filepath.Join(claude, "projects", "-w-proj")
	for _, id := range []string{ccOriginal, ccCopyIDs[0], ccForkOn} {
		if _, err := os.Stat(filepath.Join(proj, id+".jsonl")); !os.IsNotExist(err) {
			t.Errorf("%s left behind: %v", id, err)
		}
	}
	if _, err := Restore(tr.Key); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{ccOriginal, ccCopyIDs[0], ccForkOn} {
		if _, err := os.Stat(filepath.Join(proj, id+".jsonl")); err != nil {
			t.Errorf("%s not restored: %v", id, err)
		}
	}
	if got := ccRows("claude"); !slices.Equal(got, []string{ccForkOn}) {
		t.Errorf("after restore, rows = %v", got)
	}
}

// Qoder writes Claude Code's files, and its copies are one row too.
func TestQoderForkCopiesAreOneRow(t *testing.T) {
	for _, agent := range []string{"qoder", "qoder-cn"} {
		t.Run(agent, func(t *testing.T) {
			setup(t)
			ccForkFixture(t, QoderDir(agent),
				[2]string{"original.jsonl", ccOriginal},
				[2]string{"original.jsonl", ccCopyIDs[0]},
				[2]string{"fork-on.jsonl", ccForkOn})
			if got := ccRows(agent); !slices.Equal(got, []string{ccForkOn}) {
				t.Fatalf("rows = %v, want [%s]", got, ccForkOn)
			}
		})
	}
}

func agentCount(agent string) int {
	for _, a := range Agents() {
		if a.Agent == agent {
			return a.Count
		}
	}
	return 0
}

// The gateway may know a copy's id from its requests: that is the listed
// session's, not a gateway-only session of its own.
func TestMergeExternalKnowsCopies(t *testing.T) {
	native := []Session{{Agent: "claude", ID: ccForkOn, Copies: []string{ccOriginal}}}
	out := MergeExternal(native, []ExternalSession{{Agent: "claude", ID: ccOriginal, Title: "x"}})
	if len(out) != 1 {
		t.Errorf("rows = %d, want 1: the copy came back from the gateway", len(out))
	}
}

// ccStatTokens is what the stats of every day give the fixture's project:
// the days' usage, as the overview's total sums it, and the sessions'.
func ccStatTokens(t *testing.T) (days, sessions Tokens) {
	t.Helper()
	st := StatsFor(0)
	for _, d := range st.Days {
		for _, u := range d.Usage {
			if u.Agent == "claude" && u.Cwd == "/w/proj" {
				days.Input += u.Input
				days.Output += u.Output
			}
		}
	}
	for _, s := range st.Sessions {
		if s.Agent == "claude" && s.Cwd == "/w/proj" {
			sessions.Input += s.Input
			sessions.Output += s.Output
		}
	}
	return days, sessions
}

// beautiful2674 (Discord): after an update the Usage page's whole-history
// total was ~200M tokens less. A session forked with a turn of its own
// holds the first session's last message, so the first is its copy and was
// left out of the stats whole, and with it its subagent's file, which
// Claude Code leaves in the first session's folder only. The conversation
// both hold is counted once, the subagent's reply too: 3 replies of the
// first session, its subagent's 1, and the fork's own 1.
func TestClaudeForkedSessionsSubagentIsCounted(t *testing.T) {
	claude, _ := setup(t)
	ccForkFixture(t, claude,
		[2]string{"subagent.jsonl", ccSubagent},
		[2]string{"subagent-fork.jsonl", ccSubagentFork})
	sub := filepath.Join(claude, "projects", "-w-proj", ccSubagent, "subagents")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join("testdata", "claude-forks", "subagent-agent.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(sub, "agent-a889d0f781e3c8503.jsonl")
	if err := os.WriteFile(p, b, 0o644); err != nil {
		t.Fatal(err)
	}
	at := time.Now().Add(-30 * time.Minute)
	if err := os.Chtimes(p, at, at); err != nil {
		t.Fatal(err)
	}
	Reset()
	if got := ccRows("claude"); !slices.Equal(got, []string{ccSubagentFork}) {
		t.Fatalf("rows = %v, want the fork, which holds the first session, %s", got, ccSubagentFork)
	}
	days, sessions := ccStatTokens(t)
	want := Tokens{Input: 5000, Output: 500}
	if days.Input != want.Input || days.Output != want.Output {
		t.Errorf("days' tokens = %d in, %d out; want %d in, %d out", days.Input, days.Output, want.Input, want.Output)
	}
	if sessions.Input != want.Input || sessions.Output != want.Output {
		t.Errorf("sessions' tokens = %d in, %d out; want %d in, %d out", sessions.Input, sessions.Output, want.Input, want.Output)
	}
}

// A /branch made after a compaction holds only the summary and what
// follows it. When the branch goes on and the session it was made of
// doesn't, the session is the branch's copy, and what it said before the
// compaction is in no other file: it is counted all the same. compacted.jsonl
// as it was when the branch was made (before "rw prompt eight") spent 7
// replies of 10 in; the branch holds one of them, after the summary, and
// went on with one of its own.
func TestClaudeCopyBeforeACompactionIsCounted(t *testing.T) {
	claude, _ := setup(t)
	ccForkFixture(t, claude,
		[2]string{"compacted.jsonl", ccCompacted},
		[2]string{"compacted-branch-on.jsonl", ccBranch})
	p := filepath.Join(claude, "projects", "-w-proj", ccCompacted+".jsonl")
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	i := strings.Index(string(b), "rw prompt eight")
	if i < 0 {
		t.Fatal("compacted.jsonl has no \"rw prompt eight\"")
	}
	b = b[:strings.LastIndexByte(string(b[:i]), '\n')+1]
	if err := os.WriteFile(p, b, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(p, fi.ModTime(), fi.ModTime()); err != nil {
		t.Fatal(err)
	}
	Reset()
	if got := ccRows("claude"); !slices.Equal(got, []string{ccBranch}) {
		t.Fatalf("rows = %v, want the branch that went on, %s", got, ccBranch)
	}
	days, sessions := ccStatTokens(t)
	if days.Input != 80 || sessions.Input != 80 {
		t.Errorf("tokens in = %d (days), %d (sessions); want 80: 70 of the session, 10 the branch's own", days.Input, sessions.Input)
	}
}
