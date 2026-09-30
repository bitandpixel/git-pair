# modified file diff preview

 for the preview, a single file diff probably doesn't need all the git diff context at the top. We know what file we're looking at. With that as context, could we strip out some of the unnecessary git diff boilerplate, and then more easily splice in the reviewer-added lines with the `<- you` annotation? I think the native git diff rendering for directories makes sense where we need context because we're viewing one or more of multiple file diffs.

and maybe for both modified-file and new-file preview, the reviewer-authored additions could be rendered in blue to further dilineate. and purple would denote reviewer-authored modifications of under-review additions. i.e. if the reviewer deletes a line that would have been green, it becomes purple.
