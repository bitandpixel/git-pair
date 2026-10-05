// In-process memoization of git reads.
//
// A read-only command asks git the same question many times. One `git pair queue` measured 629 git
// subprocesses for 12 branches and 41 landed changesets, and 42 of those were the same
// `rev-parse refs/remotes/origin/main` — once per changeset, because each chain derivation resolves the
// destination ref it was handed. At that density the process spawn, not the git work, is what a command
// spends its time on.
//
// The memo answers the repeated calls from the first one. It lives at the subprocess boundary rather than
// at each call site for two reasons: the repeats cross packages (`changeset`, `lifecycle`, `cli` all ask
// for the same tips), and a rule a reader can check in one place is easier to trust than one spread over
// the surfaces that happen to benefit.
//
// What may be memoized is decided by `classify`, and the test is narrow: the output must follow from the
// arguments and the object database alone. Anything that reads the working tree or the index is answered
// fresh, and anything that is not a recognized read clears the memo — the conservative default, so a
// newly added write cannot serve a stale answer from before it ran.
package git

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"

	"gitpair/internal/factcache"
)

// readClass is how run treats one invocation.
type readClass int

const (
	// pureRead output follows from its arguments and the object database, so the same arguments
	// give the same answer for the life of the process. Refs appear in the arguments, and a command
	// that moves a ref is a write, so "for the life of the process" is not a claim that refs are
	// frozen — it is a claim that nothing has moved one since the last entry was kept.
	pureRead readClass = iota
	// liveRead reads the working tree, the index, or the filesystem around the repository. It is
	// answered fresh and leaves the memo alone: asking whether the tree is clean does not invalidate
	// what a commit is.
	liveRead
	// mutation changes the repository. Everything kept before it is suspect, so it clears the memo.
	mutation
)

// classify sorts one git invocation into the three classes.
//
// The default is `mutation`, not `pureRead`. A command this function does not recognize is therefore
// treated as changing the repository: the cost of getting that wrong once is a slow command, and the
// cost of getting the other direction wrong is `status` reporting a review that is not there.
func classify(args []string) readClass {
	if len(args) == 0 {
		return mutation
	}
	switch args[0] {
	case "rev-parse", "rev-list", "merge-base", "log", "ls-tree", "cat-file", "show", "for-each-ref":
		return pureRead
	case "diff":
		// `diff` is pure with two named revisions and about the working tree without them, and
		// `--cached` is about the index. Distinguished here because both spellings are in use.
		if hasFlag(args, "--cached") || hasFlag(args, "--staged") || !hasTwoRevisions(args) {
			return liveRead
		}
		return pureRead
	case "merge-tree":
		// `merge-tree --write-tree` writes objects and no ref: two commits and an object database give one
		// answer, so the same invocation never needs asking twice. It is here rather than under `diff`
		// because it is a different question — what a merge would produce, not what two commits differ by.
		return pureRead
	case "patch-id":
		// `patch-id` reads a patch on stdin and answers about it. Two fields make the answer — the patch on
		// the input and the flags — both of which the memo key carries, so the same bytes never need asking
		// twice. Without this case the default is `mutation`, and every identity measurement would clear the
		// memo the read before it was taken.
		return pureRead
	case "symbolic-ref":
		// `symbolic-ref --quiet --short HEAD` reads HEAD; `symbolic-ref <ref> <sha>` writes one.
		if hasFlag(args, "--short") || hasFlag(args, "--quiet") {
			return pureRead
		}
		return mutation
	case "status", "worktree", "var", "remote":
		// `remote` and `var` read config, which git-pair never writes. They are asked once per
		// command, so memoizing them buys nothing worth the wider classification.
		return liveRead
	default:
		return mutation
	}
}

func hasFlag(args []string, flag string) bool {
	for _, a := range args[1:] {
		if a == flag {
			return true
		}
	}
	return false
}

// hasTwoRevisions reports whether a `diff` invocation names two revisions rather than falling back to
// the working tree. Only flags and the two positional revisions are in the argument lists git-pair
// builds for the pure form, so counting the non-flag arguments is enough here; it is not a general
// revision-expression parser and does not need to be.
func hasTwoRevisions(args []string) bool {
	n := 0
	for _, a := range args[1:] {
		if strings.HasPrefix(a, "-") || a == "--" {
			continue
		}
		n++
	}
	return n >= 2
}

// memoEntry is one kept answer. A git subprocess that ran and exited non-zero still answered the
// question — `rev-parse --verify --quiet` exits 1 to report that a path is absent, which is the answer
// `CarriesDir` wants — so failures are kept alongside successes. What is never kept is an invocation
// that did not run: git missing, a cancelled context. Those say something about the machine rather than
// about the repository, and retrying them is correct.
type memoEntry struct {
	stdout string
	stderr string
	code   int
	failed bool
}

// memo is the kept answers, keyed by the exact argv (and stdin) of a pure read.
type memo struct {
	mu      sync.Mutex
	answers map[string]memoEntry
}

func (m *memo) get(key string) (memoEntry, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.answers[key]
	return e, ok
}

func (m *memo) put(key string, e memoEntry) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.answers == nil {
		m.answers = map[string]memoEntry{}
	}
	m.answers[key] = e
}

func (m *memo) clear() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.answers = nil
}

// state is the memo behind one Repo. It is behind a pointer so a `git.Repo` built by a struct literal
// (every test fixture does this) works without an initialization step.
//
// The two switches are atomic rather than plain bools under `Repo.mu` because the handle is shared: the
// review session turns the memo off on its event loop while git reads on other goroutines are asking
// whether it is on. The answers map has its own lock, which the `memo` methods hold.
type state struct {
	on      atomic.Bool
	factsOn atomic.Bool
	memo    memo
}

// Memoize turns the read memo on for this handle.
//
// It is off by default, and the default is deliberate rather than an optimization left unfinished. The
// memo's contract is "one command run": it is correct because a read-only command observes a repository it
// does not change, and exits. A handle that outlives that — a review session open for an hour, a poll loop
// waiting on a reviewer — is a handle across which refs really do move, and a memoized answer there is a
// reviewer looking at state that no longer exists. So the long-lived surfaces leave it off and say why.
//
// Off by default also protects code that has not been written yet: a `Repo` built by struct literal, or a
// new command that mutates mid-run, gets correct reads rather than fast ones.
//
// This does not touch the derived-fact cache, which keys on commit ids rather than on argument lists and so
// stays correct however long the handle lives. A long-lived surface that wants one off and the other on
// says so with the two calls.
func (r *Repo) Memoize(on bool) {
	s := r.st()
	s.on.Store(on)
	if !on {
		s.memo.clear()
	}
}

// Memoizing reports whether this handle is memoizing reads.
func (r *Repo) Memoizing() bool { return r.st().on.Load() }

// memoKey is the lookup key for one invocation: the class, the argv, and the stdin.
//
// Stdin belongs in the key because `cat-file --batch` takes the object ids there, so two batch reads with
// the same argv can ask for different objects.
func memoKey(args []string, stdin string) string {
	var b strings.Builder
	b.Grow(len(stdin) + 32)
	b.WriteString(stdin)
	b.WriteByte(0)
	for _, a := range args {
		b.WriteString(a)
		b.WriteByte(0)
	}
	return b.String()
}

// answer runs one invocation through the memo. It returns ok=false when the invocation was not served
// from the memo and the caller should run git.
func (r *Repo) answer(key string, class readClass) (memoEntry, bool) {
	s := r.st()
	if class == mutation {
		s.memo.clear()
		return memoEntry{}, false
	}
	if class != pureRead || !s.on.Load() {
		return memoEntry{}, false
	}
	if e, ok := s.memo.get(key); ok {
		return e, true
	}
	return memoEntry{}, false
}

func (r *Repo) st() *state {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.cache == nil {
		r.cache = &state{}
	}
	return r.cache
}

// FactCache turns derived-fact caching on for this handle.
//
// It is off until a command asks, so that a handle which never caches a fact never resolves the git
// directory, creates a directory, or writes a byte. `Facts` is the accessor, and it answers with a store
// that misses everything when caching is off, so a caller never has to ask first.
//
// Unlike the read memo, this one is safe for a handle that lives a long time: its keys are commit ids, and
// a commit does not change under a session that keeps reading it. A destination branch that moved produces
// a different commit, which is a different key, which is a fresh derivation. That is why the review session
// turns the memo off and leaves this on.
func (r *Repo) FactCache(on bool) { r.st().factsOn.Store(on) }

// disabledFacts answers every lookup as a miss. A store with caching off never touches its directory, so
// the empty path it is pointed at is never used.
var disabledFacts = factcache.New("")

// Facts returns this repository's derived-fact cache.
//
// It resolves the git directory on first use and remembers it, because naming the git directory is itself a
// git call and a handle that is never used for a cached fact should not pay for it. It returns
// disabledFacts when caching is off for this handle.
func (r *Repo) Facts() *factcache.Store {
	if !r.st().factsOn.Load() {
		return disabledFacts
	}
	r.factsMu.Lock()
	defer r.factsMu.Unlock()
	if r.facts == nil {
		gitDir, err := r.GitDir(context.Background())
		if err != nil || gitDir == "" {
			// No git directory means nowhere to keep anything, which is the same answer as caching being
			// off. A `Repo` aimed at a directory that is not a repository gets here.
			return disabledFacts
		}
		r.facts = factcache.New(gitDir)
		r.facts.Use(true)
	}
	return r.facts
}
