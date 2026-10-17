# Profile: planning

A repository can declare this profile alongside its development workflow. It defines how multi-step work is
planned, executed, and audited; it does not choose a branch, review, CI, or merge route. Use the repository's
other declared profiles for those decisions.

Create a plan when work spans sessions, systems, or significant decisions. Keep the plan as the current source
of truth, and keep audit records as historical evidence. Use small, verifiable milestones. Inside each milestone,
name independent workstreams and their handoffs so multiple agents can work without sharing writable files.
Do not split work merely to increase the agent count: coordination and integration must still leave a working
state at each milestone boundary.

The skills have separate entry points: `project-planning` creates a plan, `plan-execution` implements from one,
and `plan-audit` checks it on demand without implementing. A consumer adopts this profile by adding a
`[[profile]]` entry for `planning` with the current version, then running `dev-profiles install` and committing
the installed skills and pointer stanza. The profile supplies guidance; `doctor` checks the installed bytes,
not whether a plan's milestones are well designed.
