# Pre-Batch-1 Human Input Checklist

**Status:** Human input requested before Batch 1

**Recorded:** 2026-07-28

**Authority:** [Terraform-first production launch human-blocked steps][human-steps]

**Execution order:** [Terraform-first production launch roadmap][roadmap]

**Standing authorization:** [Two-window first-production-launch
authorization][two-window]

## Purpose

Collect the few production-launch decisions and access confirmations that only
the human can supply. Mayor/Gas Town completes the technical preparation,
implementation, plan review, and verification work.

In this document, Batch 1 means the next bounded Window 1 autonomous execution
batch after Slice 2. Its exact resource actions remain constrained by the
separate slice plans and saved-plan policy gates; this checklist does not
broaden them.

This file is tracked and must be treated as public. It is safe to edit remotely
only when the response contains no credential, private identifier, personal
address, recovery location, or production topology value.

## How To Use This Checklist

For each item:

1. read the explanation and recommended default;
2. put only the permitted value in the `Human response` line;
3. mark the checkbox when the decision or access confirmation is complete; and
4. keep every secret in the designated private system, never in this file,
   chat, email, GitHub issue, commit message, or screenshot.

`Confirmed privately` is a valid tracked response when the underlying value
must remain secret. Do not replace that phrase with the value.

## Human Decisions Safe To Record Here

### 1. Infrastructure Spending Ceiling

- [x] Monthly recurring ceiling approved
- [x] One-time launch ceiling approved

**Why it is needed:** Window 1 cannot create the private database tier,
replacement host, storage, or supporting infrastructure without a human-owned
maximum. The controller stops any saved plan whose estimated resource set
exceeds this boundary.

**Include:** AWS, public IPv4, database, compute, storage, backup, registry, and
Cloudflare costs that are part of the first-production stack.

**Recommended response format:**

```text
Monthly recurring ceiling: USD <amount>
One-time launch ceiling: USD <amount>
```

**Do not include:** billing account identifiers, payment-card information,
invoices, or screenshots.

**Human response:**

```text
Monthly recurring ceiling: USD 100
One-time launch ceiling: USD 200
```

### 2. Rollback Decision Owner And Close Condition

- [x] Rollback decision owner selected
- [x] Minimum rollback window selected
- [x] Measurable close condition approved

**Why it is needed:** Mayor may preserve rollback resources but may not decide
when deleting them becomes acceptable. Closing the window can make rollback
impossible and therefore remains human-owned.

**Recommended default:**

- owner: `Human operator`;
- minimum window: seven consecutive days after public cutover; and
- close only after login, signup gating, scrape, AI evaluation, daily briefing,
  archive/history, backup, restore rehearsal, monitoring, and browser checks
  pass with no unresolved Critical or P1 incident.

**Safe response format:**

```text
Rollback owner: Human operator
Minimum window: <number> consecutive days after public cutover
Close condition: <sanitized measurable condition>
```

Do not record a personal phone number, email address, or private escalation
channel.

**Human response:**

```text
Rollback owner: `Human operator`;
Minimum window: seven consecutive days after public cutover; and
Close condition: close only after login, signup gating, scrape, AI evaluation, daily briefing, archive/history, backup, restore rehearsal, monitoring, and browser checks pass with no unresolved Critical or P1 incident.
```

### 3. OCI Container Registry Choice

- [x] Registry selected
- [x] Human confirms they can restore access if existing authentication fails
- [x] Image visibility selected

**Why it is needed:** Batch 1 publishes one immutable `linux/arm64` image and
the replacement host pulls that exact digest. A mutable tag alone is not
acceptable deployment evidence.

**Recommended default:** GitHub Container Registry at `ghcr.io`, private image,
immutable digest recorded privately and in value-blind verification evidence.

**Safe response format:**

```text
Registry: ghcr.io | other
Image visibility: private | public
Can restore publish/pull access if needed: yes | no
```

Do not record a token, password, registry login command containing a secret, or
private repository identifier.

**Human response:**

```text
Registry: I'll set up a GitHub container registry account.
Image visibility: Private Image
Can restore publish/pull access if needed: Yes
```

### 4. Cloudflare Account And Zone Access

- [ ] Human confirms access to the intended Cloudflare account
- [ ] Human confirms access to the intended DNS zone
- [ ] Human can authorize Origin CA and DNS work if existing access is
      insufficient

**Why it is needed:** Mayor can prepare and review edge automation, but cannot
invent account access. Cloudflare Origin CA and DNS changes are required before
the Window 2 public cutover packet can be completed.

**Safe response format:**

```text
Correct Cloudflare account accessible: yes | no
Correct DNS zone accessible: yes | no
Can authorize Origin CA and DNS work if prompted: yes | no
```

Do not record the account ID, zone ID, API token, Origin CA certificate, private
key, exact hostname, or screenshots.

**Human response:**

```text
Correct Cloudflare account accessible: yes
Correct DNS zone accessible: yes
Can authorize Origin CA and DNS work if prompted: yes
```

### 5. Recovery-Copy Storage Class

- [ ] Off-repository recovery destination class selected
- [ ] Destination is independent of the deployment host
- [ ] Human confirms they can retrieve it during a recovery exercise

**Why it is needed:** The credential-encryption master key requires a separately
stored recovery copy. A copy on only the deployment host or only the MacBook
does not survive the corresponding device failure.

**Recommended default:** a trusted password manager or encrypted offline
storage controlled by the human.

**Safe response format:**

```text
Recovery storage class: password manager | encrypted offline storage | other
Independent of deployment host: yes | no
Human can retrieve during recovery: yes | no
```

Do not record the exact vault, item name, filesystem path, provider account,
recovery code, or secret value.

**Human response:**

```text
Recovery storage class:
Independent of deployment host:
Human can retrieve during recovery:
```

### 6. Existing Production Profile Reuse

- [ ] Human confirms whether the ignored local production profile remains
      current
- [ ] Any required corrections are made only in the ignored local profile
- [ ] Production password will be entered or generated through a private path

**Why it is needed:** The owner account and production settings must be known
before data import and browser verification. Their values are private and do
not belong in this tracked checklist.

The local source of truth is:

```text
.superpowers/profile/jobcron-profile.md
```

**Safe response format:**

```text
Reuse ignored local profile unchanged: yes | no
If no, profile will be corrected privately on the Mac: yes | no
```

Do not record the profile contents, owner identity, password, API key, signup
code, or sponsor identifier.

**Human response:**

```text
Reuse ignored local profile unchanged:
If no, profile will be corrected privately on the Mac:
```

## Private Human Actions

These actions may be completed at work, but only the completion state may be
recorded here. Never copy the underlying value into Git.

- [ ] Cloudflare access is ready, or the human knows how they will grant it
      privately when requested.
- [ ] Registry access is ready, or the human knows how they will restore it
      privately when requested.
- [ ] A secure destination exists for the credential-encryption recovery copy.
- [x] The human can perform value-blind AWS SSO approval if the short-lived
      session expires.
- [x] The human understands that Window 2 still requires a separate explicit
      approval before EIP attachment, DNS change, or public traffic.

## Already Approved Or Available

- [x] AWS IAM Identity Center administration and expected-region verification
- [x] Access-controlled local operator-log location
- [x] Cohort signup access code exists privately
- [x] Existing Anthropic credential approved for minimal paid verification
- [x] Slice 2 spending authority is limited to one unattached VPC-domain EIP
- [x] Slice 2 revised contract approved on 2026-07-28

## Mayor/Gas Town Work — No Human Action

Mayor/Gas Town must complete these items and may ask the human only if a
documented stop condition fires:

- select and verify the approved release commit;
- build and publish the immutable `linux/arm64` image;
- record and verify the image digest;
- finish Slice 2 under its exact `8 imports, 1 add, 0 change, 0 destroy`
  production-plan contract;
- choose Slice 3 private subnet CIDRs under the non-overlap policy;
- generate the production session secret and credential-encryption key
  privately;
- create the separate recovery copy without exposing its value;
- derive the sponsor user identifier from verified production data;
- create and verify the immutable database snapshot and matching WAL material;
- create private recovery, database-archive, log-archive, and verified-copy
  locations;
- produce exact saved plans and obtain independent reviews;
- stop if a plan contains an unexpected address, action, destroy, replacement,
  secret exposure, spending violation, or uncertain recovery path; and
- present the consolidated Window 2 cutover packet only after the replacement
  stack passes private-path verification.

## Batch 1 Entry Gate

Batch 1 may start only when:

1. all six human decision sections above are complete;
2. required private access exists without publishing its values;
3. Mayor-owned prerequisites are complete or covered by the Batch 1 execution
   plan;
4. the maximum spend covers the independently reviewed resource set;
5. rollback ownership and close conditions are explicit;
6. no secret or private identifier has entered tracked content; and
7. the exact Batch 1 saved plans pass their controller policy gates.

Completion of this checklist is standing authority only for the approved
Window 1 scope. It does not authorize EIP attachment, DNS changes, public
traffic, deletion of rollback resources, or closure of the rollback window.

[human-steps]:
  260726-terraform-first-production-launch-human-blocked-steps.md
[roadmap]:
  ../plans/260726-terraform-first-production-launch-roadmap.md
[two-window]:
  ../decisions/260727-two-window-first-production-launch-authorization.md
