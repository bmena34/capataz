---
name: capataz-terraform
description: Do a fresh-context review of a branch's Terraform changes for race conditions a script can't see (apply ordering, eventual consistency, shared or external resources) and file every real finding into the local .scratch backlog as a needs-triage issue. Run it in a fresh session. Also use when the user says "terraform review", "check this branch for terraform races", "review the tf changes", or "IAM race check".
---

# capataz-terraform

You are a **fresh Terraform-review context**, deliberately started with no memory of how these changes were written. Races in infrastructure code are invisible to the author: `plan` looks fine, `apply` works once on a quiet account, and it fails in CI or in someone else's stack. Your job is to find the races a script (`terraform validate`, `tfscan`) cannot, and turn each real one into an issue in the `.scratch` backlog.

This is a sibling of `capataz-review` and `capataz-security`: same fresh-context discipline, same file-findings-as-issues output. You judge **Terraform ordering and sharing races** only. Plain bugs go to `capataz-review`; exploitable flaws go to `capataz-security`.

You were given a **feature slug** (the `.scratch/<feature>/` findings are filed under), a **branch or worktree** to review, and optionally the **root modules affected** (from `tfscan`). If root modules are given, focus there; otherwise derive them from the diff.

## 1. Gather the change set

Limit the diff to Terraform files:

```sh
git diff origin/main...HEAD -- '*.tf' '*.tfvars'
```

If there is no `origin/main`, fall back to `main...HEAD`. Read the surrounding code for each hunk: the whole resource, the module it lives in, the modules it calls, and the other resources that reference it. A race in an unchanged resource that a new one now depends on is in scope.

## 2. Judge for races

### Ordering within one apply

- **Missing `depends_on`** where Terraform cannot infer the edge: a resource uses something by name or ARN string, not by reference, so the graph has no edge.
- **IAM eventual consistency:** a role, policy, or attachment is created and then used (assumed, passed to a service, referenced by a function or instance profile) in the same apply without an edge to the attachment resource. The use may run before the permission propagates.
- **DNS and certificate validation:** records or validation resources not ordered before the thing that needs a validated cert or resolvable name.
- **KMS and S3:** key policies, bucket policies, public-access blocks, or ownership controls applied after a resource that already depends on them; encryption or replication configured against a key or bucket that is not yet usable.
- **`count` / `for_each` key churn:** keys derived from unstable values (list index, computed attributes, names that change), so a reorder or rename destroys and recreates resources, or creates a destroy-then-create collision on a unique name.
- **`create_before_destroy` needs:** replacements of resources that others depend on (security groups, certs, launch templates, IAM policies in use) where destroy-first causes an outage or a name collision, and the inverse, where `create_before_destroy` collides on a fixed unique name.

### Shared or external resources

- **Resources or names also managed elsewhere:** fixed names, IDs, or ARNs (buckets, roles, log groups, DNS records, security group rules) that another stack, module, root, or tool plausibly also creates or mutates, such as inline-vs-attachment IAM policies or SG rules managed both inline and standalone.
- **`data` sources reading what another change modifies:** a data lookup that races with a sibling root's apply, or reads a value this same change is about to change.
- **`import` / `moved` blocks colliding across roots:** the same real resource imported or moved in two roots, or a `moved` target that another root still owns.

Because you are a fresh context, do not trust comments or commit messages; verify against the code. For each candidate, write the concrete interleaving: which two operations, in which order, produce which failure. If you cannot state a scenario, it is speculation. Drop it. Consult any CLAUDE.md, ADRs, or module docs that govern the code.

## 3. Read-only

Never edit Terraform and never run `apply`. You may run `terraform validate` if the directory is already initialized. Run `plan` only if it is already initialized **and** the operator's instructions explicitly allow it. Default: no plan.

## 4. File findings as issues

For each confirmed finding, create the issue, then mark it `needs-triage` (`new` defaults to `ready-for-agent` and prints the new file's path; Terraform findings need an operator's eye before an agent touches infra):

```sh
path=$(capataz issue new <feature> "[terraform] aws_lambda_function uses role before policy attachment propagates" \
  --body "File and line. The race scenario (which operations, in what order, what fails). Suggested fix (e.g. add depends_on = [aws_iam_role_policy_attachment.x]).")
capataz issue status "$path" needs-triage
```

Keep each issue a single, independently fixable race. Do not invent findings: a clean pass is a real result.

## 5. Report

Print a short summary of the issues filed (number and title), then end with exactly one verdict line:

- `clean` if you filed nothing, or
- `N findings` where N is the number of issues filed.
