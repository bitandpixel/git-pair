# nudge user

Is there anywhere that nudges the user to set up this command? I'm thinking it could be a nudge on `git pair integration record` and/or `publish`.  It may also be useful to nudge the user on a read-path too.

## Author reply

`record` nudged in the first version and `publish` did not, so the answer before this round was: yes on one
write path, no on the other, no on the read path. Fixed on both.

Each surface names the half its own run is about, and each stays silent in a clone that already has the
line:

| Surface | What it says |
| --- | --- |
| `integration record` | the `configure:` line naming both keys — the two refspecs it did not write (already there in the first version) |
| `integration publish` | the `configure:` line naming `remote.<name>.push` only. A clone that just sent a pair by hand is the clone an ordinary push could have served, and the fetch half is not what this run did by hand |
| `status`, `queue` | the "nothing here can say whether a record reached `<remote>`… never fetched" note now ends with the `--fetch` that asks once *and* `git pair integration configure`, which stops the asking |

Three details worth your read:

- **The publish nudge prints only when the run sent or confirmed something.** "nothing to publish" plus a
  sales pitch about publishing would be the worst possible pairing, and a CI job that runs `publish` on
  every build would see it on every build. Same reason it stays off the failure path, where the run is
  already explaining a refusal.
- **`--json` gets no prose nudges.** The `record` hint has been human-surface-only since before this
  changeset, and `publish` follows it: a pipeline does not read the report, and `unpublished_note` is not
  a hint — it is the answer to the question that was asked, so the read-path remedy belongs in it. Tests
  assert both halves of that.
- **Nothing writes configuration as a side effect of a nudge.** The record's flag was removed for that
  reason, and the nudges keep the same line: name it, do not take it.

The read-path nudge is deliberately inside the existing sentence rather than a new hint of its own. That
note is the moment the absence costs something, and PRD §13's rule about it is "one sentence says so" — it
still says so in one sentence, with both remedies in it.

Tested by `TestTheSurfacesNudgeTowardConfiguring` (status, its `--json`, queue, publish, publish again after
configuring) and `TestPublishWithoutAnythingToSendDoesNotNudge`.

