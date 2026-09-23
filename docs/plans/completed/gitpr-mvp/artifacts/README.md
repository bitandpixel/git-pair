# The gate scripts moved

The four scripts that lived here are the repository's live verification gates. They are not plan
artifacts, so they no longer sit under a completed plan. Run them from `scripts/gates/`:

| This plan names | Run this |
| --- | --- |
| `artifacts/e2e-29.sh` | `bash scripts/gates/e2e-29.sh` |
| `artifacts/pty-walkthrough.sh` | `bash scripts/gates/pty-walkthrough.sh` |
| `artifacts/pty-tui.py`, `artifacts/pty-plain.py` | helpers of the walkthrough, not run by hand |

`mise run gates` builds the binary and runs both scripts. README's Development section says what
each gate covers.

The plan rows and the audit rows below still name the old path. Read them as the new one. This note
is the translation, so the commands in those records stay runnable.
