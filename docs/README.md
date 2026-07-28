# Documentation Index

Use this index instead of recursively loading the entire documentation tree.

## Architecture

- [Current system architecture](architecture.md)

## Product

- [Parked feature ideas](product/feature-ideas.md)
- [보류된 기능 아이디어](product/feature-ideas.ko.md)

## Scraping

- [Source catalog and roadmap](scraping/source-catalog.md)

## Research

- [Browser-driven and fingerprint-blocked scrapers](research/2026-06-06-browser-driven-scrapers.md)
- [Bundled local model versus bring-your-own-key AI](research/2026-06-09-local-model-bundled-vs-byok.md)
- [Job-platform comparison](research/job-platforms-comparison.md)
- [채용 플랫폼 비교](research/job-platforms-comparison.ko.md)

## Learnings

- [Brainstorming skill and autonomous-mode question gates][brainstorming-autonomous-gate]
- [Campaign scope must outlive slice completion][campaign-scope-outlives-slice]
- [Caller-managed convoys require Mayor closeout][caller-managed-convoy-closeout]
- [Local-only polecat work must not use an auto-submit formula][local-only-submit-formula]

## Implementation Work

- [Active Superpowers work and context policy](superpowers/README.md)
- [Two-window first-production-launch authorization][two-window-authorization]
- [Pre-Batch-1 human input checklist][pre-batch-1-checklist]
- [Pre-Batch-1 Window 1 authorization contract][pre-batch-1-contract]
- [Terraform Slice 4 replacement EC2 and transient runtime][terraform-slice-4]
- [Terraform Slice 5 Cloudflare prefix-list automation][terraform-slice-5]
- [Contextual dealbreaker match-provenance contract][dealbreaker-provenance-spec]
- [Contextual dealbreaker match-provenance implementation][dealbreaker-provenance-plan]
- [Stage 1 contextual dealbreaker validation and exclusion evidence][contextual-dealbreaker-spec]
- [Ponytail codebase reduction campaign][ponytail-campaign-plan]
- [Ponytail reduction candidate ledger][ponytail-campaign-ledger]
- [Ponytail campaign verification][ponytail-campaign-verification]
- [PostgreSQL local convergence and per-user AI credentials](superpowers/specs/260714-postgresql-local-convergence-user-ai-credentials.md)
- [Slice 5: First production deployment](superpowers/plans/260715-postgresql-convergence-slice-5-first-production-deployment.md)
- [Multi-user account expansion and public-signup follow-up](superpowers/archive/2026-07-22-multi-user-account-expansion/260715-multi-user-account-expansion.md)
- [PostgreSQL convergence Slice 4 verification](superpowers/archive/2026-07-16-postgresql-convergence-slice-4/260715-postgresql-convergence-slice-4-verification.md)
- [PostgreSQL convergence Slice 3 verification](superpowers/archive/2026-07-15-postgresql-convergence-slice-3/260715-postgresql-convergence-slice-3-verification.md)
- [PostgreSQL convergence Slice 2 verification](superpowers/archive/2026-07-15-postgresql-convergence-slice-2/260715-postgresql-convergence-slice-2-verification.md)
- [PostgreSQL credential foundation: Slice 1 verification](superpowers/archive/2026-07-14-postgresql-credential-foundation/260714-postgresql-credential-foundation-verification.md)
- [Alpha pre-launch fixes verification](superpowers/archive/2026-07-13-alpha-pre-launch-fixes/260713-alpha-pre-launch-fixes-verification.md)
- [Alpha milestone A polishes verification](superpowers/archive/2026-07-12-alpha-milestone-a-polishes/260712-alpha-milestone-a-polishes-verification.md)
- [No browser-driven scraping for v1.x](superpowers/decisions/260606-no-browser-driven-scraping.md)
- [RDS production settings](superpowers/decisions/260710-rds-production-settings.md)
- [Jobcron production and rename decisions](superpowers/decisions/260711-jobcron-production.md)
- [Hosted-first product and local database convergence](superpowers/decisions/260714-hosted-first-local-database-convergence.md)

## Deployment

Deployment configuration remains at the repository root because Docker,
Compose, Caddy, CI, and EC2 commands consume those paths directly.

- [Local PostgreSQL](../deploy/local/README.md)
- [Public demo deployment](../deploy/demo/README.md)
- [Public demo human guide](../deploy/demo/HUMAN_DEPLOY_GUIDE.md)
- [Production deployment](../deploy/production/README.md)
- [Production human guide](../deploy/production/HUMAN_DEPLOY_GUIDE.md)

## Assets

- `assets/screenshots/` contains images embedded by the root README files.

[brainstorming-autonomous-gate]: learnings/260717-brainstorming-autonomous-mode-question-gate.md
[campaign-scope-outlives-slice]: learnings/260718-campaign-scope-outlives-slice-completion.md
[caller-managed-convoy-closeout]: learnings/260718-caller-managed-convoy-closeout.md
[local-only-submit-formula]: learnings/260719-local-only-polecat-submit-formula.md
[two-window-authorization]:
  superpowers/decisions/260727-two-window-first-production-launch-authorization.md
[pre-batch-1-checklist]:
  superpowers/specs/260728-pre-batch-1-human-input-checklist.md
[pre-batch-1-contract]:
  superpowers/specs/260728-pre-batch-1-window-1-authorization-contract.md
[terraform-slice-4]:
  superpowers/plans/260728-terraform-slice-4-replacement-ec2-transient-runtime-implementation.md
[terraform-slice-5]:
  superpowers/plans/260728-terraform-slice-5-cloudflare-prefix-list-automation-implementation.md
[ponytail-campaign-plan]:
  superpowers/archive/2026-07-18-ponytail-codebase-reduction/260717-campaign-plan.md
[ponytail-campaign-ledger]:
  superpowers/archive/2026-07-18-ponytail-codebase-reduction/260717-candidate-ledger.md
[ponytail-campaign-verification]:
  superpowers/archive/2026-07-18-ponytail-codebase-reduction/260718-verification.md
[contextual-dealbreaker-spec]:
  superpowers/archive/2026-07-18-contextual-dealbreaker-validation/260718-stage-1-contextual-dealbreaker-validation-and-exclusion-evidence.md
[dealbreaker-provenance-spec]:
  superpowers/archive/2026-07-25-contextual-dealbreaker-match-provenance/260725-contextual-dealbreaker-match-provenance-contract.md
[dealbreaker-provenance-plan]:
  superpowers/archive/2026-07-25-contextual-dealbreaker-match-provenance/260725-contextual-dealbreaker-match-provenance-implementation.md
