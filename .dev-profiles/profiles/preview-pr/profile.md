# preview-pr

A preview environment is a property of how this repository deploys, not of how a change gets reviewed. This profile
carries the one behaviour that follows from having one: open a draft pull request, purely to make the preview deploy,
and never treat it as the review or the merge.

Adopt it when a pull request is what makes a preview appear. Decline it when there is no preview environment — a
package, a library, a CLI — and nothing else in the workflow notices, because no other profile needs it and no
installed copy is left behind.
