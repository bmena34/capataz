---
name: capataz-fix-conflict
description: Resolve a merge that capataz left in a conflicted state after merging a parallel issue's branch back into the base, then complete the merge. Invoked when capataz dispatches a fresh session (in the primary checkout) after a branch merge conflicted. Also use when the user says "fix the conflicted merge" or "resolve this in-progress merge and commit".
---

# capataz-fix-conflict

You are a **fresh context**, deliberately started with no memory of how either side of this merge was written — so you can judge the right resolution from the code itself rather than inheriting either side's assumptions.

capataz runs parallel issues in isolated git worktrees, each on a branch named `capataz/<feature>-<NN>-<slug>`. When the operator triggers merge-back, capataz merges each branch into the base in the **primary checkout**. A clean merge just lands; a **conflicting** merge is left in its conflicted state, and capataz spawns you (this session, in the primary checkout) to resolve it.

The dispatch prompt names the branch:

> Use the /capataz-fix-conflict skill: a merge of branch `<BRANCH>` into the base in this repo is in progress and conflicted. Resolve the conflicts, run the build/tests, and commit the merge.

## Process

### 1. Orient

```sh
git status   # which files are conflicted, confirm the merge is in progress
git diff     # the conflict hunks
```

Understand **both** sides of every conflict — the base's version (what already landed) and the incoming branch's version — and what each was trying to achieve. Don't read the hunks in isolation; read the surrounding code so you know the intent behind each side.

The incoming branch `<BRANCH>` corresponds to an issue in `.scratch/`: the branch name (`capataz/<feature>-<NN>-<slug>`) and its commit message (which references `<feature>/NN`) point you at the issue file. Read that issue so you know what the incoming change was for.

### 2. Resolve each conflict

Resolve so **both intents are preserved** wherever possible — do not blindly pick a side. A conflict here usually means two changes to the same lines that were each individually correct; the right answer is normally a merge of both behaviors, not a discard of one. Only drop one side when the code makes it genuinely incompatible with the other, and note why in your report.

### 3. Verify

Run the project's build and tests until green:

```sh
go build ./... && go vet ./... && go test ./...
```

If something fails, it usually means your resolution dropped or double-applied behavior — go back to step 2. Don't complete the merge until checks pass.

### 4. Complete the merge

```sh
git add -A && git commit
```

This completes the in-progress merge — git already has the merge commit message staged, so no message editing is needed. **Never commit `.scratch/`.** It's the local-only backlog, ignored by the global git excludes file `capataz install` sets up. Never `git add -f` it, and never add `.scratch` to a repo's `.gitignore` — that line would get pushed. Do **not** start, resume, or trigger any **other** branch merges; you resolve this one merge only. capataz drives the merge-back sequence.

### 5. Report

Summarize what conflicted and how you resolved it: which files, what each side was doing, and why your resolution is right (especially if you had to favor one side).

Then record it as a comment on the offending issue's `.scratch` file — a conflict between two supposedly parallel-safe issues means their predicted **`Touches`** were wrong (the two overlapped where they were expected not to), which is worth capturing:

```sh
capataz issue comment .scratch/<feature>/issues/NN-<slug>.md \
  "Merge of <BRANCH> conflicted with the base in <files>; resolved by <how>. Touches misprediction: this issue overlapped <other issue/area> that it wasn't predicted to touch."
```

Flag the `Touches` misprediction explicitly so future parallel scheduling can account for the overlap.
