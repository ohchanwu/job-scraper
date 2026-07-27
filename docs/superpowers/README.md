# Superpowers Work Index

Read this file before opening plans, specifications, or implementation reports.
Future sessions should load only the active files listed below and the concise
decision records needed for the current task.

## Active Work

- [Terraform AWS foundation and Cloudflare ingress automation][terraform-aws-foundation]
- [Terraform-first production launch human-blocked steps][terraform-launch-human-steps]
- [Terraform-first production launch roadmap][terraform-launch-roadmap]
- [Terraform Slice 2: canonical VPC and EIP adoption][terraform-slice-2-spec]
- [Terraform Slice 2 implementation][terraform-slice-2-plan]
- [PostgreSQL local convergence and per-user AI credentials](specs/260714-postgresql-local-convergence-user-ai-credentials.md)

## Recently Archived

- [Terraform Slice 1: identity, state bootstrap, and CI][terraform-slice-1-plan]
- [Terraform Slice 1 verification][terraform-slice-1-verification]
- [Superseded first production human steps][archived-first-production-human-steps]
- [PostgreSQL account-mutation clock source][account-mutation-clock-plan]
- [Contextual dealbreaker match-provenance contract][dealbreaker-match-provenance-spec]
- [Contextual dealbreaker match-provenance implementation][dealbreaker-match-provenance-plan]
- [AI re-rate blocker surfacing](archive/2026-07-25-ai-rerate-blocker-surfacing/260725-ai-rerate-blocker-surfacing-plan.md)
- [Multi-user account expansion specification][multi-user-account-spec]
- [Multi-user account expansion implementation][multi-user-account-plan]
- [Contextual dealbreaker validation specification][contextual-dealbreaker-spec]
- [Contextual dealbreaker validation implementation][contextual-dealbreaker-plan]
- [Ponytail codebase reduction campaign][ponytail-campaign-plan]
- [Ponytail reduction candidate ledger][ponytail-campaign-ledger]
- [Ponytail campaign verification][ponytail-campaign-verification]
- [Daangn canonical role URLs][daangn-canonical-spec]
- [Daangn canonical role URL implementation][daangn-canonical-plan]
- [PostgreSQL convergence Slice 4 plan](archive/2026-07-16-postgresql-convergence-slice-4/260715-postgresql-convergence-slice-4-verified-sqlite-import.md)
- [PostgreSQL convergence Slice 4 verification](archive/2026-07-16-postgresql-convergence-slice-4/260715-postgresql-convergence-slice-4-verification.md)
- [PostgreSQL convergence Slice 3 plan](archive/2026-07-15-postgresql-convergence-slice-3/260715-postgresql-convergence-slice-3-local-postgresql-bootstrap.md)
- [PostgreSQL convergence Slice 3 verification](archive/2026-07-15-postgresql-convergence-slice-3/260715-postgresql-convergence-slice-3-verification.md)
- [PostgreSQL convergence Slice 2 plan](archive/2026-07-15-postgresql-convergence-slice-2/260715-postgresql-convergence-slice-2-user-scoped-ai-runtime.md)
- [PostgreSQL convergence Slice 2 verification](archive/2026-07-15-postgresql-convergence-slice-2/260715-postgresql-convergence-slice-2-verification.md)
- [PostgreSQL credential foundation: Slice 1 implementation plan](archive/2026-07-14-postgresql-credential-foundation/260714-postgresql-credential-foundation-implementation-plan.md)
- [PostgreSQL credential foundation: Slice 1 verification](archive/2026-07-14-postgresql-credential-foundation/260714-postgresql-credential-foundation-verification.md)
- [Alpha pre-launch fixes specification](archive/2026-07-13-alpha-pre-launch-fixes/260713-alpha-pre-launch-fixes.md)
- [Alpha pre-launch fixes implementation plan](archive/2026-07-13-alpha-pre-launch-fixes/260713-alpha-pre-launch-fixes-implementation-plan.md)
- [Alpha pre-launch fixes verification](archive/2026-07-13-alpha-pre-launch-fixes/260713-alpha-pre-launch-fixes-verification.md)
- [README deployment status refresh design](archive/2026-07-13-readme-deployment-status-refresh/260713-readme-deployment-status-refresh.md)
- [README deployment status refresh implementation plan](archive/2026-07-13-readme-deployment-status-refresh/260713-readme-deployment-status-refresh-implementation-plan.md)
- [Alpha milestone A polishes specification](archive/2026-07-12-alpha-milestone-a-polishes/260712-alpha-milestone-a-polishes.md)
- [Alpha milestone A polishes implementation plan](archive/2026-07-12-alpha-milestone-a-polishes/260712-alpha-milestone-a-polishes-implementation-plan.md)
- [Alpha milestone A polishes verification](archive/2026-07-12-alpha-milestone-a-polishes/260712-alpha-milestone-a-polishes-verification.md)
- [Interactive local preview, first-run guidance, and navigation](archive/2026-07-11-interactive-preview-navigation/260711-interactive-preview-navigation.md)
- [Integrated verification report](archive/2026-07-11-interactive-preview-navigation/260711-interactive-preview-navigation-verification.md)

## Stable Decisions

- [No browser-driven scraping for v1.x](decisions/260606-no-browser-driven-scraping.md)
- [RDS production settings](decisions/260710-rds-production-settings.md)
- [Jobcron production and rename decisions](decisions/260711-jobcron-production.md)
- [Hosted-first product and local database convergence](decisions/260714-hosted-first-local-database-convergence.md)

## Context Policy

- `plans/` contains only work that is not implemented yet.
- `specs/` contains only designs that are still active or awaiting approval.
- `decisions/` contains short durable facts needed by future work.
- `archive/` contains completed evidence. Do not scan or load it unless an active
  plan or a human explicitly names a specific archived file.
- `.superpowers/sdd/` is ephemeral and must contain only the current execution's
  ignored briefs, reports, and progress ledger.
- When work completes, distill stable facts into `decisions/`, move verbose
  tracked artifacts to a dated archive workstream, and move ignored local-only
  artifacts to `.superpowers/archive/`.
- Git history is the authoritative fallback for old detail. Do not keep verbose
  completed reports active merely for discoverability.

[daangn-canonical-spec]:
  archive/2026-07-17-daangn-canonical-role-urls/260717-daangn-canonical-role-urls.md
[daangn-canonical-plan]:
  archive/2026-07-17-daangn-canonical-role-urls/260717-daangn-canonical-role-urls-plan.md
[ponytail-campaign-plan]:
  archive/2026-07-18-ponytail-codebase-reduction/260717-campaign-plan.md
[ponytail-campaign-ledger]:
  archive/2026-07-18-ponytail-codebase-reduction/260717-candidate-ledger.md
[ponytail-campaign-verification]:
  archive/2026-07-18-ponytail-codebase-reduction/260718-verification.md
[contextual-dealbreaker-spec]:
  archive/2026-07-18-contextual-dealbreaker-validation/260718-stage-1-contextual-dealbreaker-validation-and-exclusion-evidence.md
[contextual-dealbreaker-plan]:
  archive/2026-07-18-contextual-dealbreaker-validation/260718-stage-1-contextual-dealbreaker-validation-and-exclusion-evidence-plan.md
[dealbreaker-match-provenance-spec]:
  archive/2026-07-25-contextual-dealbreaker-match-provenance/260725-contextual-dealbreaker-match-provenance-contract.md
[dealbreaker-match-provenance-plan]:
  archive/2026-07-25-contextual-dealbreaker-match-provenance/260725-contextual-dealbreaker-match-provenance-implementation.md
[account-mutation-clock-plan]:
  archive/2026-07-26-postgresql-account-mutation-clock-source/260726-postgresql-account-mutation-clock-source.md
[terraform-aws-foundation]:
  specs/260719-terraform-aws-foundation-and-cloudflare-ingress-automation.md
[terraform-launch-human-steps]:
  specs/260726-terraform-first-production-launch-human-blocked-steps.md
[terraform-launch-roadmap]:
  plans/260726-terraform-first-production-launch-roadmap.md
[terraform-slice-2-spec]:
  specs/260727-terraform-slice-2-canonical-vpc-eip-adoption.md
[terraform-slice-2-plan]:
  plans/260727-terraform-slice-2-canonical-vpc-eip-adoption-implementation.md
[terraform-slice-1-plan]:
  archive/2026-07-26-terraform-slice-1/260726-terraform-slice-1-identity-state-bootstrap-ci.md
[terraform-slice-1-verification]:
  archive/2026-07-26-terraform-slice-1/260726-terraform-slice-1-verification.md
[archived-first-production-human-steps]:
  archive/2026-07-26-first-production-launch-human-blocked-steps/260716-first-production-launch-human-blocked-steps.md
[multi-user-account-spec]:
  archive/2026-07-22-multi-user-account-expansion/260715-multi-user-account-expansion.md
[multi-user-account-plan]:
  archive/2026-07-22-multi-user-account-expansion/260722-multi-user-account-expansion.md
