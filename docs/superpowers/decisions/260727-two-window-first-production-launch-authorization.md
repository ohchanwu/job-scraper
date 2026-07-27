# Two-Window First-Production-Launch Authorization

**Status:** Approved

**Recorded:** 2026-07-27

## Decision

Jobcron's first production launch uses two human authorization windows instead
of repeated approval of each saved Terraform plan.

### Window 1: Bounded Standing Authorization

The human supplies the inputs only they control and authorizes Mayor to execute
Slices 2 through 5 in dependency order. Planning, implementation, and
independent review may be front-loaded, but a state-changing apply may proceed
unattended only when its saved plan:

- passed independent review;
- contains no destroy or replace action;
- contains only resource addresses and actions on that slice's explicit
  allow-list;
- exposes no private or sensitive value in tracked output;
- stays within the human-approved spending ceiling;
- preserves the old EC2 instance, old RDS instance, existing EIP association,
  and required rollback materials;
- follows an unambiguous live discovery result that satisfies the documented
  deterministic selection contract;
- has current credentials available; and
- passes verification and recovery checks.

These are controller policy gates, not requests for a human to approve each
plan instance. Regenerating a plan reruns the policy gates and independent
review; it does not by itself require another human response.

### Window 2: Public Cutover

The final EIP, DNS, and public-traffic cutover always remains a separate human
gate. Mayor presents one consolidated packet containing the exact cutover
scope, private-path verification result, rollback readiness, value-blind
change summary, and stop conditions. Cutover starts only after the human
explicitly approves that packet.

## Stop And Return To The Human

Standing authorization stops on ambiguity, drift, a missing or stale
credential, an unexpected action or resource address, policy broadening, a
spending-limit violation, failed verification, uncertain recovery, any destroy
or replace action, or any need to close the rollback window. Work resumes only
after the human resolves the exceptional condition or grants new bounded
authority.

## Human-Only Responsibilities

The human retains responsibility for:

- cloud-account access that Mayor does not hold;
- registry access and credentials;
- the maximum approved infrastructure spend;
- ownership of rollback decisions and the condition that ends the rollback
  window; and
- the explicit Window 2 cutover response.

Mayor may never infer these values, broaden the policy, approve spending,
enable public traffic, delete rollback resources, or close the rollback window.

## Timing

Task 3's two security fixes are internal: independent agents review them and
execution continues without human approval of the fixes themselves. If AWS
reauthentication is required, the next expected human action is value-blind AWS
SSO device approval. Otherwise, the next expected human action is the
consolidated Window 1 input packet if human-controlled inputs remain missing.
After SSO approval and any required Window 1 inputs, Window 2 cutover is the
next planned approval. A stop condition is an exceptional human-facing
interruption.

## Rationale

Independent review and machine-checkable action/address policies provide the
safety boundary that repeated approval of opaque plan instances was intended
to supply. One bounded policy authorization reduces needless interruptions
without relaxing dependency order, recovery readiness, publication safety, or
the prohibition on destructive changes.

## Supersession Scope

This decision supersedes repeated per-plan human approvals in the active
first-production-launch roadmap, human-blocked steps specification, and Slice 2
specification and implementation plan. It does not supersede Slice 1's
completed evidence, any technical safeguard, a slice's explicit allow-list,
the Slice 1 through 6 apply order, or the human-only Window 2 cutover and
rollback-close decisions.
