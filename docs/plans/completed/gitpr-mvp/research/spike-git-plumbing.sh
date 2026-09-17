#!/usr/bin/env bash
# Spike: validate the git plumbing git-pair needs, independent of implementation language.
# Run: bash docs/plans/completed/gitpr-mvp/research/spike-git-plumbing.sh
set -uo pipefail

T=$(mktemp -d /tmp/git-pair-spike.XXXXXX)
trap 'rm -rf "$T"' EXIT
cd "$T"
git init -q -b main .
git config user.email spike@example.com
git config user.name Spike
git config commit.gpgsign false

bar() { printf '\n===== %s =====\n' "$1"; }

# ---------------------------------------------------------------- scaffold
mkdir -p src
cat > src/service.ts <<'EOF'
export function createOffering(id: string) {
  const offering = load(id);
  return offering;
}

export function enroll(userId: string, offeringId: string) {
  return db.write(() => debit(userId));
}
EOF
echo "test" > README.md
git add -A && git commit -qm "initial implementation"

bar "1. trailer placeholders in git log --format"
git commit -q --allow-empty -m "review: booking-transaction

GitPR-Outcome: block
GitPR-Changeset: booking-transaction"
git log -1 --format='SUBJECT=%s
OUTCOME=[%(trailers:key=GitPR-Outcome,only,unfold)]
ALL=[%(trailers:only,unfold)]'
echo "--- empty commit created OK: $(git rev-list --count HEAD) commits"

bar "2. lifecycle enumeration over base..HEAD (two-dot)"
git switch -qc booking-transaction
echo "// touched" >> src/service.ts && git commit -qam "implementation: touch service"
git log --reverse --format='%h|%s|%(trailers:key=GitPR-Outcome,only,unfold)' main..HEAD

bar "3. three-dot diff span (changeset base...HEAD)"
git diff --stat main...HEAD
echo "merge-base = $(git merge-base main HEAD)"

bar "4. review-relative span review..HEAD"
R=$(git rev-parse HEAD~1)
echo "review commit = ${R:0:7}"
git diff --stat "${R}..HEAD"

bar "5. added lines of a review commit, -U0 hunk parsing"
git commit -q --allow-empty -m noop 2>/dev/null || true
# Build a realistic review commit that adds a source comment + ABOUT line.
mkdir -p changesets/booking-transaction
printf 'base: main\n' > changesets/booking-transaction/CHANGESET.yaml
printf '# About\n\n## Validation\n\n- unit tests\n' > changesets/booking-transaction/ABOUT.md
sed -i 's|return db.write(() => debit(userId));|// What happens if these execute concurrently?\n  return db.write(() => debit(userId));|' src/service.ts
git add -A && git commit -qm "review: booking-transaction

GitPR-Outcome: block
GitPR-Changeset: booking-transaction"
R2=$(git rev-parse HEAD)
git --no-pager diff --no-color -U0 "${R2}^" "${R2}"

bar "6. surviving additions detection (R2 vs HEAD)"
# Author responds: one review line removed, one left untouched, ABOUT untouched.
sed -i '/What happens if these execute concurrently?/d' src/service.ts
echo "// unrelated agent change" >> src/service.ts
git commit -qam "implementation: respond to review"
HEAD_SHA=$(git rev-parse HEAD)
python3 - "$R2" <<'PY'
import subprocess, sys, re, os
review = sys.argv[1]
def git(*a):
    return subprocess.run(["git", *a], capture_output=True, text=True, check=True).stdout

root = subprocess.run(["git", "rev-parse", "--verify", "-q", review + "^"],
                      capture_output=True).returncode != 0
diff = git("show", "--format=", "--no-color", "-U0", review) if root else \
       git("diff", "--no-color", "-U0", review + "^", review)

path = None
added = []          # (path, new_lineno_in_review, text)
for line in diff.splitlines():
    if line.startswith("diff --git "):
        path = line.split(" b/", 1)[1]
    elif line.startswith("@@"):
        m = re.search(r"\+(\d+)(?:,(\d+))?", line)
        new_start = int(m.group(1))
    elif line.startswith("+") and not line.startswith("+++"):
        text = line[1:]
        if text.strip():
            added.append((path, new_start, text))
        new_start += 1

print("review-added non-blank lines:", len(added))
cache = {}
surviving = []
for path, lineno, text in added:
    if path not in cache:
        try:
            cache[path] = git("show", f"HEAD:{path}").splitlines()
        except subprocess.CalledProcessError:
            cache[path] = None      # file deleted at HEAD
    blob = cache[path]
    if blob is not None and text in blob:
        surviving.append((path, blob.index(text) + 1, text))
for p, n, t in surviving:
    print(f"  {p}:{n}  {t!r}")
print("SURVIVING COUNT:", len(surviving))
PY

bar "7. refs/reviews lifecycle"
git update-ref "refs/reviews/booking-transaction" "$R2"
git update-ref "refs/reviews/archive/booking-transaction/$(git rev-parse --short "$R2")" "$R2"
git for-each-ref --format='%(refname) -> %(objectname:short)' refs/reviews
echo "--- reachability after branch rewrite (simulated squash):"
git checkout -q main
git branch -D booking-transaction >/dev/null 2>&1 || git branch -f booking-transaction main
git rev-list --count "refs/reviews/booking-transaction" 
git log --oneline -3 "refs/reviews/booking-transaction"

bar "8. branch -> changeset slug rule"
for b in feature/booking-transaction feature/x/y booking-transaction "feat/JIRA-123 Foo Bar"; do
  printf '%-32s -> %s\n' "$b" "$(printf '%s' "$b" | tr '/' '-' | tr -cs 'a-zA-Z0-9._-' '-' | sed 's/^-\+//; s/-\+$//')"
done

bar "9. for-each-ref based queue discovery + % (trailers) on arbitrary revs"
git for-each-ref --format='%(refname:short) %(objectname:short) %(committerdate:relative)' refs/reviews

bar "10. interpret-trailers round-trip (for constructing commits)"
printf 'review: approve booking-transaction\n' | \
  git interpret-trailers --trailer 'GitPR-Outcome=approve' --trailer 'GitPR-Changeset=booking-transaction'
